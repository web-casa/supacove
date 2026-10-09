package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Multipart thresholds. Objects at or below multipartThreshold use a single
// PutObject; larger ones stream as multipart with explicit abort on failure
// (protocol C).
const (
	multipartThreshold = 64 << 20 // 64 MiB
	partSize           = 16 << 20 // 16 MiB (S3 minimum part size is 5 MiB)
	uploadConcurrency  = 2
)

// Store is the S3-compatible Backend implementation covering AWS S3,
// Cloudflare R2 and Backblaze B2 through their S3 APIs.
type Store struct {
	client *s3.Client
	cfg    Config
	logger *slog.Logger
}

// compile-time assertion that the manager Uploader handles our reader types.

// New validates the config and builds the client.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		// Bounded retries: the jobs layer adds its own outer retry for the
		// whole upload, so the SDK should not retry too aggressively.
		RetryMaxAttempts: 3,
	}
	opts := []func(*s3.Options){
		func(o *s3.Options) {
			// Path-style addressing for custom endpoints (R2/B2/MinIO);
			// harmless for AWS.
			if cfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(cfg.Endpoint)
				o.UsePathStyle = true
			}
		},
	}
	client := s3.NewFromConfig(awsCfg, opts...)
	return &Store{client: client, cfg: cfg, logger: logger}, nil
}

// Config returns a copy of the validated configuration (SecretKey included;
// callers must never log it).
func (s *Store) Config() Config { return s.cfg }

// Prefix implements Backend.
func (s *Store) Prefix() string { return s.cfg.Prefix }

// Put implements Backend: single PUT below the multipart threshold,
// manager.Uploader multipart above. The Uploader owns the entire multipart
// lifecycle (create, parts, complete); on failure it aborts internally —
// but with the CALLER's context, which on shutdown is already canceled
// (LeavePartsOnError defaults to false) and the real upload ID is extracted
// from manager.MultiUploadFailure for the error record — our previous
// manual Create+Abort wrapper used a DIFFERENT upload ID than the Uploader,
// leaking parts on both success and failure (round-1 review P1-04).
// NOTE: on context cancellation the SDK's internal abort reuses the
// canceled ctx and silently no-ops; abortMultipart below compensates on a
// detached context. Without a bucket lifecycle rule for incomplete
// uploads, any abort that still fails leaves parts until manual cleanup.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if size >= 0 && size < multipartThreshold {
		_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(s.cfg.Bucket),
			Key:           aws.String(key),
			Body:          r,
			ContentLength: aws.Int64(size),
		})
		if err != nil {
			return fmt.Errorf("put object: %w", err)
		}
		return nil
	}
	//nolint:staticcheck // SA1019: feature/s3/manager is soft-deprecated in
	// favour of feature/s3/transfermanager; migration is tracked as a
	// follow-up — the uploader path is stable and fully exercised by the
	// MinIO integration test.
	uploader := manager.NewUploader(s.client, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = uploadConcurrency
	})
	out, err := uploader.Upload(ctx, &s3.PutObjectInput{ //nolint:staticcheck // SA1019: manager path, migration tracked as follow-up
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
		Body:   r,
	})
	if err != nil {
		var muf manager.MultiUploadFailure
		if errors.As(err, &muf) {
			// manager v1 aborts with the ALREADY-CANCELED ctx on shutdown,
			// so its internal cleanup silently no-ops and parts leak until
			// the bucket lifecycle runs. Abort again on a detached context
			// (best effort; the upload ID came from the failure itself).
			s.abortMultipart(muf.UploadID(), key)
			return fmt.Errorf("multipart upload %s: %w", muf.UploadID(), err)
		}
		// No upload ID in the error: sweep anything left incomplete under
		// this key (best effort).
		s.AbortIncomplete(ctx, key)
		return fmt.Errorf("multipart upload: %w", err)
	}
	_ = out // ETag not used; integrity comes from the C.1 read-back hash
	return nil
}

// abortMultipart best-effort abort on a detached context with its own
// deadline: the caller's context is usually the reason we are here.
func (s *Store) abortMultipart(uploadID, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 15*time.Second)
	defer cancel()
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.cfg.Bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		s.logger.Warn("multipart abort failed; parts persist until an "+
			"incomplete-upload lifecycle rule (if the bucket has one) reclaims them",
			"key", key, "upload_id", uploadID, "err", err)
	}
}

// AbortIncomplete aborts every in-flight multipart session for key on a
// detached context. Best effort; used after upload failures whose error
// carries no upload ID.
//
// Rescan-to-fixed-point instead of marker pagination: MinIO invalidates the
// key/upload-id markers once the previous page's sessions are aborted, so a
// marker walk skips same-key survivors (GLM review round 5: 101 sessions,
// 4/4 runs left exactly one behind). Relisting from the key after each
// aborted batch is correct on both AWS and MinIO; the loop ends when a page
// yields no session for this exact key, and a round cap plus the overall
// deadline bound pathological churn.
func (s *Store) AbortIncomplete(_ context.Context, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 30*time.Second)
	defer cancel()
	const maxRounds = 100 // 100 rounds x 100/page = 10k sessions; deadline still rules
	for round := 0; round < maxRounds; round++ {
		// NO markers at all, by design: AWS treats KeyMarker as
		// strictly-greater (a same-key marker would skip every session of
		// this key), and MinIO invalidates both markers after the previous
		// page's aborts. Relisting from scratch each round is correct on
		// both and ends when a page yields nothing for this exact key.
		out, err := s.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket:     aws.String(s.cfg.Bucket),
			Prefix:     aws.String(key),
			MaxUploads: aws.Int32(100),
		})
		if err != nil {
			s.logger.Warn("list multipart uploads failed", "key", key, "err", err)
			return
		}
		aborted := 0
		for _, u := range out.Uploads {
			if u.Key == nil || *u.Key != key || u.UploadId == nil {
				continue
			}
			if _, aerr := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.cfg.Bucket),
				Key:      u.Key,
				UploadId: u.UploadId,
			}); aerr != nil {
				s.logger.Warn("multipart abort failed; parts persist until an "+
					"incomplete-upload lifecycle rule (if the bucket has one) reclaims them",
					"key", key, "upload_id", *u.UploadId, "err", aerr)
			} else {
				aborted++
			}
		}
		if aborted == 0 {
			return // fixed point: nothing left for this exact key
		}
	}
	s.logger.Warn("multipart sweep hit the round cap; sessions may remain", "key", key)
}

var errNotFound = errors.New("object not found")

// IsNotFound reports whether err came from an absent object.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }

// Get implements Backend.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NoSuchKey
		if errors.As(err, &nf) {
			return nil, 0, fmt.Errorf("%w: %s", errNotFound, key)
		}
		return nil, 0, fmt.Errorf("get object: %w", err)
	}
	size := int64(-1)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return out.Body, size, nil
}

// Delete implements Backend. Absent keys are treated as success.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// List implements Backend with full pagination (>1000 objects).
func (s *Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.cfg.Bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, o := range page.Contents {
			obj := Object{Key: aws.ToString(o.Key)}
			if o.Size != nil {
				obj.Size = *o.Size
			}
			if o.LastModified != nil {
				obj.LastModified = *o.LastModified
			}
			out = append(out, obj)
		}
	}
	return out, nil
}

// Presign implements Backend.
func (s *Store) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	pc := s3.NewPresignClient(s.client)
	req, err := pc.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return req.URL, nil
}

// DiagnosticTest writes a canary into the diagnostic namespace, reads it
// back, verifies content, then deletes it — a live write/read/delete cycle
// fully separate from the backup namespace (dev-plan Phase 3 task 2).
func (s *Store) DiagnosticTest(ctx context.Context) (result error) {
	key := s.cfg.DiagnosticKey()
	body := fmt.Sprintf("supacove diagnostic %d", time.Now().UnixNano())
	if err := s.Put(ctx, key, strings.NewReader(body), int64(len(body))); err != nil {
		return fmt.Errorf("diagnostic write: %w", err)
	}
	// Once written, the canary is ours: clean it up on EVERY exit path so a
	// failed diagnostic never leaves billable objects behind (round-3 review
	// P2-06).
	// result is the NAMED return: a cleanup failure after a clean read-back
	// must still fail the test (it used to be assigned to a local variable
	// the function never returned).
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if derr := s.Delete(dctx, key); derr != nil {
			if result == nil {
				result = fmt.Errorf("diagnostic cleanup: could not delete the canary %s: %w", key, derr)
			} else {
				result = fmt.Errorf("%w; ALSO failed to clean up canary %s: %w", result, key, derr)
			}
		}
	}()
	rc, size, err := s.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("diagnostic read: %w", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("diagnostic read body: %w", err)
	}
	if size >= 0 && int64(len(got)) != size {
		result = fmt.Errorf("diagnostic read: size mismatch (%d vs %d)", len(got), size)
		return result
	}
	if string(got) != body {
		result = fmt.Errorf("diagnostic read: content mismatch")
		return result
	}
	return nil
}

// newStoreWithClient builds a Store over a caller-supplied client — the
// injection point for the pagination/abort regression test.
func newStoreWithClient(client *s3.Client, cfg Config, logger *slog.Logger) *Store {
	return &Store{client: client, cfg: cfg, logger: logger}
}
