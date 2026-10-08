package storage

import (
	"context"
	"io"
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

// TestDiagnosticTestReportsCleanupFailure: credentials that can write and
// read but not delete used to pass the diagnostic (the cleanup error was
// assigned to a variable the function never returned), leaving the canary
// behind and promising a retention that could never delete anything.
func TestDiagnosticTestReportsCleanupFailure(t *testing.T) {
	for _, denyDelete := range []bool{false, true} {
		var mu sync.Mutex
		var stored []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			switch r.Method {
			case http.MethodPut:
				stored, _ = io.ReadAll(r.Body)
			case http.MethodGet:
				_, _ = w.Write(stored) // httptest writer; test stub
			case http.MethodDelete:
				if denyDelete {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		client := s3.NewFromConfig(aws.Config{
			Region:      "test",
			Credentials: credentials.NewStaticCredentialsProvider("a", "b", ""),
		}, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(srv.URL)
			o.UsePathStyle = true
		})
		s := newStoreWithClient(client, Config{Bucket: "b"}, slog.New(slog.NewTextHandler(discard{}, nil)))
		err := s.DiagnosticTest(context.Background())
		srv.Close()
		switch {
		case denyDelete && err == nil:
			t.Fatal("a canary that cannot be deleted must fail the diagnostic")
		case denyDelete && !strings.Contains(err.Error(), "diagnostic cleanup"):
			t.Fatalf("cleanup failure not named: %v", err)
		case !denyDelete && err != nil:
			t.Fatalf("clean write/read/delete cycle failed: %v", err)
		}
	}
}
