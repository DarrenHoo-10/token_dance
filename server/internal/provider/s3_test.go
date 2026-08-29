package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tokendance/internal/config"
)

type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func newFakeS3Server(t *testing.T) (*httptest.Server, *fakeS3) {
	t.Helper()
	fake := &fakeS3{objects: make(map[string][]byte), types: make(map[string]string)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && r.URL.Query().Get("X-Amz-Signature") == "" {
			http.Error(w, "missing signature", http.StatusForbidden)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")
		fake.mu.Lock()
		defer fake.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fake.objects[key] = body
			fake.types[key] = r.Header.Get("Content-Type")
			hash := sha256.Sum256(body)
			w.Header().Set("ETag", `"`+hex.EncodeToString(hash[:])+`"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			body, ok := fake.objects[key]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			hash := sha256.Sum256(body)
			w.Header().Set("Content-Length", stringInt64(int64(len(body))))
			w.Header().Set("Content-Type", fake.types[key])
			w.Header().Set("ETag", `"`+hex.EncodeToString(hash[:])+`"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			body, ok := fake.objects[key]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		case http.MethodDelete:
			delete(fake.objects, key)
			delete(fake.types, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	}))
	return server, fake
}

func stringInt64(value int64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}

func testS3Options(endpoint string) S3Options {
	return S3Options{Endpoint: endpoint, Region: "us-east-1", Bucket: "test-bucket", AccessKey: "test-access", SecretKey: "test-secret", UsePathStyle: true}
}

func TestS3ObjectStorageIntegrationAndSharedKeys(t *testing.T) {
	server, _ := newFakeS3Server(t)
	defer server.Close()
	ctx := context.Background()
	writer, err := NewS3ObjectStorage(ctx, testS3Options(server.URL))
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}
	reader, err := NewS3ObjectStorage(ctx, testS3Options(server.URL))
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}

	key := "exports/user-1/report.csv"
	payload := []byte("date,tokens\n2026-08-30,42\n")
	if err := writer.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "text/csv"); err != nil {
		t.Fatalf("put: %v", err)
	}
	meta, err := reader.HeadObject(ctx, key)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if meta.Key != key || meta.Size != int64(len(payload)) || meta.ContentType != "text/csv" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	rc, err := reader.OpenObject(ctx, key)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %q", got)
	}

	uploadURL, err := writer.PresignUploadURL(ctx, "uploads/avatar.png", time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	request, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader([]byte("avatar")))
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("presigned put failed: status=%v err=%v", responseStatus(response), err)
	}
	_ = response.Body.Close()
	downloadURL, err := reader.PresignDownloadURL(ctx, "uploads/avatar.png", time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	response, err = http.Get(downloadURL)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("presigned get failed: status=%v err=%v", responseStatus(response), err)
	}
	downloaded, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(downloaded) != "avatar" {
		t.Fatalf("unexpected presigned download: %q", downloaded)
	}

	if err := reader.DeleteObject(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := writer.HeadObject(ctx, key); err != ErrObjectNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func responseStatus(response *http.Response) any {
	if response == nil {
		return nil
	}
	return response.StatusCode
}

func TestObjectStorageConstructorRejectsMemoryInProduction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Environment = "production"
	cfg.ObjectProvider = "memory"
	if storage, err := NewObjectStorage(cfg); err == nil || storage != nil {
		t.Fatal("production selected memory object storage")
	}

	server, _ := newFakeS3Server(t)
	defer server.Close()
	cfg.ObjectProvider = "s3"
	cfg.ObjectEndpoint = server.URL
	cfg.ObjectRegion = "us-east-1"
	cfg.ObjectBucket = "test-bucket"
	cfg.ObjectAccessKey = "access"
	cfg.ObjectSecretKey = "secret"
	storage, err := NewObjectStorage(cfg)
	if err != nil {
		t.Fatalf("production S3 constructor failed: %v", err)
	}
	if _, ok := storage.(*MemoryObjectStorage); ok {
		t.Fatal("production selected memory object storage")
	}
}
