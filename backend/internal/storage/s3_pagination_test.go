package storage

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Review R2-1 / GLM-r5 regression: the sweep must clear EVERY in-flight
// session of a key. The stub models both provider behaviors observed:
//   - AWS: marker pagination over stable listings;
//   - MinIO: key/upload-id markers are invalidated once the previous page's
//     sessions are aborted, so the only correct client strategy is the
//     rescan-from-key fixed-point the production code implements.
func TestAbortIncompletePaginatesSameKey(t *testing.T) {
	const sessions = 101 // exceeds one page (MaxUploads=100)
	var mu sync.Mutex
	aborted := map[string]bool{}
	// page 1 = ids 1..100, page 2 = id 101
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		switch {
		case r.Method == http.MethodGet && q.Has("uploads"):
			// Any marker in the request means the client fell back to
			// pagination, which loses same-key sessions on the real
			// providers (AWS: same-key KeyMarker = strictly greater;
			// MinIO: markers invalidated after in-page aborts). The stub
			// models that failure mode as an empty result set.
			if q.Get("key-marker") != "" || q.Get("upload-id-marker") != "" {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = fmt.Fprint(w, `<?xml version="1.0"?><ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>b</Bucket><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`) // httptest writer; test stub
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>b</Bucket>`)
			// Alive sessions in creation order — page 1 of 100.
			shown, truncated := 0, false
			for i := 1; i <= sessions; i++ {
				if aborted[fmt.Sprintf("u%03d", i)] {
					continue
				}
				if shown == 100 {
					truncated = true
					break
				}
				fmt.Fprintf(&b, "<Upload><Key>dest/backups/x.dump.age</Key><UploadId>u%03d</UploadId></Upload>", i)
				shown++
			}
			_ = truncated // markers are ignored by the client by design
			b.WriteString("<IsTruncated>false</IsTruncated></ListMultipartUploadsResult>")
			_, _ = fmt.Fprint(w, b.String()) // httptest writer; test stub
		case r.Method == http.MethodDelete && q.Get("uploadId") != "":
			aborted[q.Get("uploadId")] = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	client := s3.NewFromConfig(aws.Config{
		Region:      "test",
		Credentials: credentials.NewStaticCredentialsProvider("a", "b", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
	})
	s := newStoreWithClient(client, Config{Bucket: "b"}, slog.New(slog.NewTextHandler(discard{}, nil)))
	s.AbortIncomplete(context.Background(), "dest/backups/x.dump.age")

	mu.Lock()
	defer mu.Unlock()
	if len(aborted) != sessions {
		t.Fatalf("aborted %d of %d in-flight sessions; same-key page 2 was skipped (R2-1)", len(aborted), sessions)
	}
	if !aborted["u101"] {
		t.Error("the session on page 2 (u101) was not aborted")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
