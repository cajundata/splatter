package spaces

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config carries the Spaces connection settings (spec §7 env vars).
type Config struct {
	Endpoint  string // SPLATTER_S3_ENDPOINT, e.g. https://nyc3.digitaloceanspaces.com
	Bucket    string // SPLATTER_S3_BUCKET
	AccessKey string // SPLATTER_S3_ACCESS_KEY
	SecretKey string // SPLATTER_S3_SECRET_KEY
	Region    string // SPLATTER_S3_REGION, optional
}

// FromEnv builds Config from the SPLATTER_S3_* variables. The error
// names every missing variable at once. Region defaults to the first
// hostname label of the endpoint (nyc3.digitaloceanspaces.com → nyc3),
// falling back to us-east-1.
func FromEnv() (Config, error) {
	cfg := Config{
		Endpoint:  os.Getenv("SPLATTER_S3_ENDPOINT"),
		Bucket:    os.Getenv("SPLATTER_S3_BUCKET"),
		AccessKey: os.Getenv("SPLATTER_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("SPLATTER_S3_SECRET_KEY"),
		Region:    os.Getenv("SPLATTER_S3_REGION"),
	}
	var missing []string
	for _, v := range []struct{ name, val string }{
		{"SPLATTER_S3_ENDPOINT", cfg.Endpoint},
		{"SPLATTER_S3_BUCKET", cfg.Bucket},
		{"SPLATTER_S3_ACCESS_KEY", cfg.AccessKey},
		{"SPLATTER_S3_SECRET_KEY", cfg.SecretKey},
	} {
		if v.val == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("spaces sync not configured: missing %s", strings.Join(missing, ", "))
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return Config{}, fmt.Errorf("SPLATTER_S3_ENDPOINT %q: must be an http(s) URL", cfg.Endpoint)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
		host := u.Hostname()
		if i := strings.Index(host, "."); i > 0 {
			cfg.Region = host[:i]
		}
	}
	return cfg, nil
}
