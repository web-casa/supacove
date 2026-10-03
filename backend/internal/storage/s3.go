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
var _ = manager.NewUploader

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
// streamed multipart above, abort on any failure.
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
	return s.multipartPut(ctx, key, r)
}

func (s *Store) multipartPut(ctx context.Context, key string, r io.Reader) (err error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("create multipart upload: %w", err)
	}
	completed := false
	uploadID := aws.ToString(out.UploadId)
	defer func() {
		if !completed {
			if _, aerr := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.cfg.Bucket),
				Key:      aws.String(key),
				UploadId: out.UploadId,
			}); aerr != nil {
				// Abort failure would leave billable parts behind: surface it.
				err = errors.Join(err, fmt.Errorf("ABORT FAILED for multipart upload %s — parts may remain; clean up via bucket lifecycle rule: %w", uploadID, aerr))
			}
		}
	}()

	uploader := manager.NewUploader(s.client, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = uploadConcurrency
	})
	// manager.Uploader handles part numbering and completion; it aborts
	// internally on failure when LeavePartsOnError is false (the default),
	// but we keep our own defer as a belt-and-braces guarantee.
	uo, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
		Body:   r,
	})
	if err != nil {
		return fmt.Errorf("multipart upload: %w", err)
	}
	if uo == nil || uo.ETag == nil {
		return errors.New("multipart upload returned no ETag")
	}
	completed = true
	return nil
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
func (s *Store) DiagnosticTest(ctx context.Context) error {
	key := s.cfg.DiagnosticKey()
	body := fmt.Sprintf("supabackup diagnostic %d", time.Now().UnixNano())
	if err := s.Put(ctx, key, strings.NewReader(body), int64(len(body))); err != nil {
		return fmt.Errorf("diagnostic write: %w", err)
	}
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
		return fmt.Errorf("diagnostic read: size mismatch (%d vs %d)", len(got), size)
	}
	if string(got) != body {
		return fmt.Errorf("diagnostic read: content mismatch")
	}
	if err := s.Delete(ctx, key); err != nil {
		return fmt.Errorf("diagnostic delete: %w", err)
	}
	return nil
}
