package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldWorkspaceCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	res, err := ScaffoldWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".gitattributes", ".gitignore", "providers.yaml", "pricing.yaml", "projects"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if len(res.Existed) != 0 || len(res.Created) == 0 {
		t.Fatalf("fresh scaffold: %#v", res)
	}
	ga, _ := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if !strings.Contains(string(ga), "eol=lf") {
		t.Fatalf(".gitattributes must force LF: %q", ga)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), "projects/*/runs/*/images/") {
		t.Fatalf(".gitignore must ignore run images: %q", gi)
	}
	py, _ := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if !strings.Contains(string(py), "version: 1") {
		t.Fatalf("providers.yaml must declare version: %q", py)
	}
}

func TestScaffoldWorkspaceIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	// Re-run in the same dir is idempotent: everything reported existed.
	res, err := ScaffoldWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 || len(res.Existed) == 0 {
		t.Fatalf("re-run: %#v", res)
	}
	// Scaffolding nested under an existing workspace is refused.
	nested := filepath.Join(dir, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldWorkspace(nested); err == nil {
		t.Fatal("want refusal nested under existing workspace")
	}
}

func TestScaffoldWorkspaceNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	custom := []byte("version: 1\n# my profiles\n")
	if err := os.WriteFile(filepath.Join(dir, "providers.yaml"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ScaffoldWorkspace(dir) // partial dir: has providers.yaml, no projects/
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if string(got) != string(custom) {
		t.Fatal("existing file was overwritten")
	}
	found := false
	for _, p := range res.Existed {
		if p == "providers.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("providers.yaml should be reported as existed: %#v", res)
	}
}

func TestFindRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "projects", "gradient-descent", "briefs")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := FindRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir may contain symlinks on darwin; compare resolved paths.
	wantRoot, _ := filepath.EvalSymlinks(dir)
	gotRoot, _ := filepath.EvalSymlinks(root)
	if gotRoot != wantRoot {
		t.Fatalf("want %s, got %s", wantRoot, gotRoot)
	}
	if _, err := FindRoot(t.TempDir()); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("want ErrNoWorkspace, got %v", err)
	}
}

func TestScaffoldProject(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	res, err := ScaffoldProject(dir, "gradient-descent")
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"briefs", "critiques", "packages"} {
		if _, err := os.Stat(filepath.Join(dir, "projects", "gradient-descent", sub, ".gitkeep")); err != nil {
			t.Fatalf("missing %s/.gitkeep: %v", sub, err)
		}
	}
	if len(res.Created) == 0 {
		t.Fatalf("fresh project: %#v", res)
	}
	// Idempotent re-run reports existed, errors nothing.
	res2, err := ScaffoldProject(dir, "gradient-descent")
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Created) != 0 || len(res2.Existed) == 0 {
		t.Fatalf("re-run: %#v", res2)
	}
	// Bad names and non-workspace roots error.
	if _, err := ScaffoldProject(dir, "Bad Name"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("want ErrInvalidName, got %v", err)
	}
	if _, err := ScaffoldProject(t.TempDir(), "ok"); err == nil {
		t.Fatal("want error outside workspace")
	}
}
