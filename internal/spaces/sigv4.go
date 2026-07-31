// Package spaces is a minimal S3-compatible client for DigitalOcean
// Spaces blob sync: SigV4 signing plus HEAD/PUT/GET on flat keys. It
// knows nothing about the filesystem or the workspace.
package spaces

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// emptyPayloadSHA256 is sha256("") — the payload hash for bodyless
// requests (GET, HEAD).
const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Sign adds x-amz-date, x-amz-content-sha256, and Authorization SigV4
// headers to req, signing host plus every header already set. The URL
// path must not need URI encoding beyond EscapedPath (splatter keys are
// hex, so this always holds); the query string must already be in
// canonical form (splatter sends none).
func Sign(req *http.Request, accessKey, secretKey, region, payloadSHA256 string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadSHA256)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	names := []string{"host"}
	for name := range req.Header {
		names = append(names, strings.ToLower(name))
	}
	sort.Strings(names)

	var canonHeaders strings.Builder
	for _, n := range names {
		v := host
		if n != "host" {
			v = strings.TrimSpace(req.Header.Get(n))
		}
		canonHeaders.WriteString(n)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(v)
		canonHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadSHA256,
	}, "\n")

	scope := dateStamp + "/" + region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonical)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	key = hmacSHA256(key, []byte(region))
	key = hmacSHA256(key, []byte("s3"))
	key = hmacSHA256(key, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+
			", SignedHeaders="+signedHeaders+
			", Signature="+signature)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
