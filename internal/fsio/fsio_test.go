package fsio

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendRecordProducesOneParseableLinePerCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	for i := 0; i < 3; i++ {
		if err := AppendRecord(path, map[string]int{"n": i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]int
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %d not valid JSON: %v", lines, err)
		}
		if m["n"] != lines {
			t.Fatalf("line %d: got n=%d", lines, m["n"])
		}
		lines++
	}
	if lines != 3 {
		t.Fatalf("want 3 lines, got %d", lines)
	}
}

func TestAppendRecordRejectsUnmarshalableValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	if err := AppendRecord(path, make(chan int)); err == nil {
		t.Fatal("want error for unmarshalable value")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file must not be created when marshal fails")
	}
}

func TestReplaceFileOverExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.html")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestReplaceFileLeavesNoTempLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.html")
	if err := ReplaceFile(path, []byte("content")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("want only sheet.html, got %s", strings.Join(names, ", "))
	}
}

func TestReplaceFileProducesReadableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sheet.html")
	if err := ReplaceFile(path, []byte("content")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("want 0644, got %o", got)
	}
}
