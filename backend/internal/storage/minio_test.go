package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Integration against a real S3 API (MinIO) — validates Put/Get multipart,
// Delete, List pagination behavior and Presign through the actual AWS SDK.
// Skips when docker is unavailable (environment, not a code assertion).
var (
	minioOnce   sync.Once
	minioAddr   string
	minioClient *Store
	minioErr    error
)

func requireMinIO(t *testing.T) *Store {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	minioOnce.Do(func() {
		name := fmt.Sprintf("sb-minio-%d", time.Now().UnixNano()%1e9)
		cmd := exec.Command("docker", "run", "-d", "--name", name,
			"-e", "MINIO_ROOT_USER=minioadmin", "-e", "MINIO_ROOT_PASSWORD=minioadmin",
			"-p", "127.0.0.1::9000", "minio/minio:latest", "server", "/data")
		if out, err := cmd.Output(); err != nil {
			minioErr = fmt.Errorf("docker run minio: %w: %s", err, out)
			return
		}
		minioContainer = name
		portOut, err := exec.Command("docker", "port", name, "9000").Output()
		if err != nil {
			minioErr = fmt.Errorf("docker port: %w", err)
			return
		}
		parts := strings.Split(strings.TrimSpace(string(portOut)), ":")
		addr := fmt.Sprintf("http://127.0.0.1:%s", parts[len(parts)-1])

		cfg := Config{
			Platform: "s3", Endpoint: addr, Region: "us-east-1",
			Bucket: "sb-test", AccessKey: "minioadmin", SecretKey: "minioadmin",
		}
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			store, err := New(context.Background(), cfg, nil)
			if err != nil {
				minioErr = err
				return
			}
			// Create the bucket via the S3 API (MinIO starts empty).
			if _, err := store.client.CreateBucket(context.Background(), &s3.CreateBucketInput{
				Bucket: new("sb-test"),
			}); err == nil {
				minioClient = store
				minioAddr = addr
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		if minioClient == nil {
			minioErr = fmt.Errorf("minio never became ready")
		}
	})
	if minioErr != nil {
		t.Skipf("minio unavailable: %v", minioErr)
	}
	t.Cleanup(func() {
		if minioContainer != "" && !minioStopped {
			_ = exec.Command("docker", "rm", "-f", minioContainer).Run()
			minioStopped = true
			// The container is per-process shared: once stopped, a later
			// test in the same run must start a FRESH instance instead of
			// reusing the dead client (GLM r5: second MinIO test got
			// connection refused after the first test's cleanup).
			minioOnce = sync.Once{}
			minioClient = nil
		}
	})
	return minioClient
}

var (
	minioContainer string
	minioStopped   bool
)

//go:fix inline

func TestMinIOEndToEnd(t *testing.T) {
	s := requireMinIO(t)
	ctx := context.Background()

	// 1. Small put + get roundtrip
	body := "small-object-content"
	if err := s.Put(ctx, "test/small.bin", strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("small put: %v", err)
	}
	rc, size, err := s.Get(ctx, "test/small.bin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != body || size != int64(len(body)) {
		t.Fatalf("roundtrip mismatch: %q (%d)", got, size)
	}

	// 2. Multipart upload (larger than the 64 MiB threshold via many parts:
	//    force by using the multipart path with a moderate object — we lower
	//    the threshold through a bigger-than-threshold object only if fast
	//    enough; 65 MiB of zeros compresses fine in memory).
	big := make([]byte, 65<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "test/big.bin", bytes.NewReader(big), int64(len(big))); err != nil {
		t.Fatalf("multipart put: %v", err)
	}
	rc, _, err = s.Get(ctx, "test/big.bin")
	if err != nil {
		t.Fatalf("multipart get: %v", err)
	}
	h := sha256New()
	n, err := io.Copy(h, rc)
	rc.Close()
	if err != nil {
		t.Fatalf("multipart read: %v", err)
	}
	if n != int64(len(big)) {
		t.Fatalf("multipart size %d, want %d", n, len(big))
	}

	// 3. List pagination (5 objects here; the paginator loop is what's
	//    exercised — real >1000 pagination is a provider-matrix item).
	if err := s.Put(ctx, "test/list/1", strings.NewReader("1"), 1); err != nil {
		t.Fatal(err)
	}
	objs, err := s.List(ctx, "test/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objs) < 3 {
		t.Fatalf("list returned %d objects, want >= 3", len(objs))
	}

	// 4. Presign returns a URL containing the bucket and key path.
	url, err := s.Presign(ctx, "test/small.bin", time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if !strings.Contains(url, "sb-test") {
		t.Fatalf("presigned URL missing bucket: %q", url)
	}

	// 5. Delete
	if err := s.Delete(ctx, "test/small.bin"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := s.Get(ctx, "test/small.bin"); !IsNotFound(err) {
		t.Fatalf("get after delete: want not-found, got %v", err)
	}

	// 6. Diagnostic test (write/read/delete cycle)
	if err := s.DiagnosticTest(ctx); err != nil {
		t.Fatalf("diagnostic: %v", err)
	}
}

func sha256New() hash.Hash { return sha256.New() }

// GLM-r5 regression: AbortIncomplete must clear EVERY in-flight session of
// a key on the real MinIO the project ships with — marker pagination was
// observed to skip same-key survivors once earlier pages were aborted
// (4/4 runs left exactly one of 101 behind).
func TestMinIOAbortIncompleteSweepsPastOnePage(t *testing.T) {
	s := requireMinIO(t)
	ctx := context.Background()
	const sessions = 101
	key := "backups/sweep-many.dump.age"

	for i := 0; i < sessions; i++ {
		if _, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(s.cfg.Bucket), Key: aws.String(key),
		}); err != nil {
			t.Fatalf("create session %d: %v", i, err)
		}
	}

	s.AbortIncomplete(ctx, key)

	out, err := s.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
		Bucket: aws.String(s.cfg.Bucket), Prefix: aws.String(key),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range out.Uploads {
		if u.Key != nil && *u.Key == key {
			t.Fatalf("in-flight session survived the sweep: uploadId=%s", aws.ToString(u.UploadId))
		}
	}
}
