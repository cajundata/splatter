package spaces

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound distinguishes a missing remote blob from transport errors.
var ErrNotFound = errors.New("not found")

// Client performs signed HEAD/PUT/GET calls against one bucket using
// path-style addressing: <endpoint>/<bucket>/<key>.
type Client struct {
	cfg  Config
	http *http.Client
	now  func() time.Time
}

func New(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 5 * time.Minute},
		now:  time.Now,
	}
}

func (c *Client) do(ctx context.Context, method, key string, body []byte, payloadSHA256, contentType string) (*http.Response, error) {
	url := strings.TrimSuffix(c.cfg.Endpoint, "/") + "/" + c.cfg.Bucket + "/" + key
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	Sign(req, c.cfg.AccessKey, c.cfg.SecretKey, c.cfg.Region, payloadSHA256, c.now())
	return c.http.Do(req)
}

// Head reports whether key exists. 404 is (false, nil); any other
// non-200 (including 403, which S3 can return for auth problems) is an
// error — treating it as absent would make push re-upload forever.
func (c *Client) Head(ctx context.Context, key string) (bool, error) {
	resp, err := c.do(ctx, http.MethodHead, key, nil, emptyPayloadSHA256, "")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, httpErr("HEAD", key, resp)
	}
}

// Put uploads body under key. payloadSHA256 is body's lowercase hex
// sha256 — for splatter blobs it equals the key's hash segment.
func (c *Client) Put(ctx context.Context, key string, body []byte, payloadSHA256 string) error {
	resp, err := c.do(ctx, http.MethodPut, key, body, payloadSHA256, "image/png")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpErr("PUT", key, resp)
	}
	return nil
}

// Get streams key's content. Missing keys wrap ErrNotFound.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, http.MethodGet, key, nil, emptyPayloadSHA256, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %w", key, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, httpErr("GET", key, resp)
	}
	return resp.Body, nil
}

// httpErr renders a non-2xx response with a bounded body excerpt.
func httpErr(op, key string, resp *http.Response) error {
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(excerpt))
	if msg == "" {
		return fmt.Errorf("%s %s: %s", op, key, resp.Status)
	}
	return fmt.Errorf("%s %s: %s: %s", op, key, resp.Status, msg)
}
