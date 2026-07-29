package spaces

import (
	"net/http"
	"testing"
	"time"
)

// AWS's published S3 SigV4 GET-object example: known inputs, known
// Authorization header. The vector is authoritative.
func TestSignMatchesAWSGetObjectExample(t *testing.T) {
	req, err := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	Sign(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"us-east-1", emptyPayloadSHA256, now)

	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
	if got := req.Header.Get("x-amz-date"); got != "20130524T000000Z" {
		t.Fatalf("x-amz-date: %q", got)
	}
	if got := req.Header.Get("x-amz-content-sha256"); got != emptyPayloadSHA256 {
		t.Fatalf("x-amz-content-sha256: %q", got)
	}
}

// Signing must be deterministic: same inputs, same signature.
func TestSignDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sig := func() string {
		req, _ := http.NewRequest("PUT", "https://nyc3.digitaloceanspaces.com/bkt/blobs/abc123", nil)
		req.Header.Set("Content-Type", "image/png")
		Sign(req, "AK", "SK", "nyc3", "deadbeef", now)
		return req.Header.Get("Authorization")
	}
	if a, b := sig(), sig(); a != b {
		t.Fatalf("not deterministic:\n%s\n%s", a, b)
	}
}
