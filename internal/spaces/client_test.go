package spaces

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cajundata/splatter/internal/spaces/spacestest"
)

func testClient(t *testing.T) (*Client, *spacestest.Server) {
	t.Helper()
	srv := spacestest.New(t)
	c := New(Config{Endpoint: srv.URL, Bucket: srv.Bucket,
		AccessKey: srv.AccessKey, SecretKey: "test-secret", Region: "test-1"})
	return c, srv
}

func TestPutHeadGetRoundTrip(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()
	body := []byte("png-bytes")

	exists, err := c.Head(ctx, "blobs/abc")
	if err != nil || exists {
		t.Fatalf("pre-put head: exists=%v err=%v", exists, err)
	}
	if err := c.Put(ctx, "blobs/abc", body, "irrelevant-for-fake"); err != nil {
		t.Fatal(err)
	}
	exists, err = c.Head(ctx, "blobs/abc")
	if err != nil || !exists {
		t.Fatalf("post-put head: exists=%v err=%v", exists, err)
	}
	rc, err := c.Get(ctx, "blobs/abc")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(body) {
		t.Fatalf("get: %q", got)
	}
	if stored, _ := srv.Get("blobs/abc"); string(stored) != string(body) {
		t.Fatalf("stored: %q", stored)
	}
}

func TestGetMissingWrapsErrNotFound(t *testing.T) {
	c, _ := testClient(t)
	_, err := c.Get(context.Background(), "blobs/nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Wrong access key → fake returns 403 → Head must surface an error,
// never report absent (403-as-missing would make push re-upload forever).
func TestHeadAuthFailureIsError(t *testing.T) {
	srv := spacestest.New(t)
	c := New(Config{Endpoint: srv.URL, Bucket: srv.Bucket,
		AccessKey: "WRONGKEY", SecretKey: "s", Region: "r"})
	if _, err := c.Head(context.Background(), "blobs/abc"); err == nil {
		t.Fatal("want error on 403")
	}
}
