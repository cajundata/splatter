package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusCounts(t *testing.T) {
	root := buildFixture(t)
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("want 1 project, got %#v", statuses)
	}
	s := statuses[0]
	if s.Name != "gradient-descent" || s.Briefs != 1 || s.Runs != 1 || s.Verdicts != 1 || s.Critiques != 1 {
		t.Fatalf("bad counts: %#v", s)
	}
}

func TestStatusEmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 0 {
		t.Fatalf("want no projects, got %#v", statuses)
	}
}

func TestStatusIgnoresNonDirRunEntries(t *testing.T) {
	root := buildFixture(t)
	proj := filepath.Join(root, "projects", "gradient-descent")
	// a stray file matching r_* must not count as a run
	if err := os.WriteFile(filepath.Join(proj, "runs", "r_stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].Runs != 1 {
		t.Fatalf("want 1 run, got %d", statuses[0].Runs)
	}
}

func TestStatusCountsMissingImages(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldProject(root, "alpha"); err != nil {
		t.Fatal(err)
	}
	// one referenced image, not written to disk
	writeRunFixture(t, root, "alpha", "r_0001", "c_01_0", shaOf("gone"))
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].MissingImages != 1 {
		t.Fatalf("bad statuses: %+v", statuses)
	}
}
