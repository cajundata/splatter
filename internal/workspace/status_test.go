package workspace

import (
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
