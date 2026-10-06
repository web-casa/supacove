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

// Review R2-1 regression: ListMultipartUploads pagination must carry BOTH
// markers, or S3 skips same-key sessions on later pages. The stub emulates
// the documented behavior: with only a key marker it returns uploads whose
// key is strictly greater; only key+upload-id marker continues the same key.
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
			keyMarker := q.Get("key-marker")
			uploadMarker := q.Get("upload-id-marker")
			w.Header().Set("Content-Type", "application/xml")
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>b</Bucket>`)
			start, count, truncated := 1, sessions, false
			if keyMarker != "" && uploadMarker == "" {
				// S3 semantics without an upload-id marker: only keys
				// strictly greater than the marker — same-key uploads are
				// skipped entirely.
				start, count, truncated = sessions+1, 0, false
			} else if keyMarker != "" && uploadMarker != "" {
				start, count, truncated = 101, 1, false
			} else if count > 100 {
				count = 100
				truncated = true
			}
			for i := start; i < start+count; i++ {
				_, _ = fmt.Fprintf(&b, "<Upload><Key>dest/backups/x.dump.age</Key><UploadId>u%03d</UploadId></Upload>", i) // strings.Builder never fails
			}
			if truncated {
				b.WriteString("<IsTruncated>true</IsTruncated><NextKeyMarker>dest/backups/x.dump.age</NextKeyMarker><NextUploadIdMarker>u100</NextUploadIdMarker>")
			} else {
				b.WriteString("<IsTruncated>false</IsTruncated>")
			}
			b.WriteString("</ListMultipartUploadsResult>")
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
