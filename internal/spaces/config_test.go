package spaces

import (
	"strings"
	"testing"
)

func setAllEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SPLATTER_S3_ENDPOINT", "https://nyc3.digitaloceanspaces.com")
	t.Setenv("SPLATTER_S3_BUCKET", "splats")
	t.Setenv("SPLATTER_S3_ACCESS_KEY", "AK")
	t.Setenv("SPLATTER_S3_SECRET_KEY", "SK")
	t.Setenv("SPLATTER_S3_REGION", "")
}

func TestFromEnvComplete(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_REGION", "ams3")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Endpoint: "https://nyc3.digitaloceanspaces.com", Bucket: "splats",
		AccessKey: "AK", SecretKey: "SK", Region: "ams3"}
	if cfg != want {
		t.Fatalf("got %+v want %+v", cfg, want)
	}
}

func TestFromEnvDerivesRegionFromEndpoint(t *testing.T) {
	setAllEnv(t)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "nyc3" {
		t.Fatalf("region: got %q want nyc3", cfg.Region)
	}
}

func TestFromEnvRegionFallback(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_ENDPOINT", "https://localhost:9000")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("region: got %q want us-east-1", cfg.Region)
	}
}

// The error must name every missing var at once, not just the first.
func TestFromEnvNamesAllMissingVars(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_BUCKET", "")
	t.Setenv("SPLATTER_S3_SECRET_KEY", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("want error")
	}
	for _, name := range []string{"SPLATTER_S3_BUCKET", "SPLATTER_S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q should name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), "SPLATTER_S3_ENDPOINT") {
		t.Fatalf("error %q names a var that is set", err)
	}
}

func TestFromEnvRejectsBadEndpoint(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_ENDPOINT", "nyc3.digitaloceanspaces.com") // no scheme
	if _, err := FromEnv(); err == nil {
		t.Fatal("want error for endpoint without scheme")
	}
}
