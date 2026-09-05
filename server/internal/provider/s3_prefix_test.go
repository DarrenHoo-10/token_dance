package provider

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestS3PrefixIsolatesSharedBucket(t *testing.T) {
	server, fake := newFakeS3Server(t)
	defer server.Close()
	opts := testS3Options(server.URL)
	opts.Prefix = "/token-dance/"
	storage, err := NewS3ObjectStorage(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "users/example/avatar.png"
	if err := storage.PutObject(ctx, key, bytes.NewReader([]byte("image")), 5, "image/png"); err != nil {
		t.Fatal(err)
	}
	if string(fake.objects["token-dance/"+key]) != "image" {
		t.Fatal("object escaped application prefix")
	}
	meta, err := storage.HeadObject(ctx, key)
	if err != nil || meta.Key != key {
		t.Fatalf("unexpected metadata: %v %v", meta, err)
	}
	body, err := storage.OpenObject(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := io.ReadAll(body)
	body.Close()
	if string(contents) != "image" {
		t.Fatal("prefixed read failed")
	}
	for _, sign := range []func(context.Context, string, time.Duration) (string, error){storage.PresignUploadURL, storage.PresignDownloadURL} {
		signed, err := sign(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(signed)
		if !strings.HasSuffix(parsed.Path, "/token-dance/"+key) {
			t.Fatal("presigned URL escaped prefix")
		}
	}
	fake.objects[key] = []byte("another application")
	if err := storage.DeleteObject(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.objects["token-dance/"+key]; ok {
		t.Fatal("prefixed object not deleted")
	}
	if string(fake.objects[key]) != "another application" {
		t.Fatal("unrelated object changed")
	}
}
