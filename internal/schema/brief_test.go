package schema

import (
	"strings"
	"testing"
)

const briefDoc = `---
id: b_001
project: gradient-descent
concept: one-line concept statement
mood: [technical, austere]
motifs: [contour lines, sparse grids]
exclusions: [brains, circuit-board cliches]
aspect: square
---
Creative direction prose follows in the body.
`

func TestParseBrief(t *testing.T) {
	meta, body, err := ParseBrief([]byte(briefDoc))
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != "b_001" || meta.Project != "gradient-descent" || meta.Aspect != "square" {
		t.Fatalf("bad meta: %#v", meta)
	}
	if len(meta.Mood) != 2 || meta.Mood[0] != "technical" {
		t.Fatalf("bad mood: %#v", meta.Mood)
	}
	if !strings.Contains(body, "Creative direction prose") {
		t.Fatalf("bad body: %q", body)
	}
}

func TestParseBriefRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"no front matter": "just prose\n",
		"missing id":      "---\nproject: p\nconcept: c\naspect: square\n---\nbody\n",
		"bad aspect":      "---\nid: b_001\nproject: p\nconcept: c\naspect: wide\n---\nbody\n",
		"unclosed":        "---\nid: b_001\n",
	}
	for name, doc := range cases {
		if _, _, err := ParseBrief([]byte(doc)); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestNormalizeLF(t *testing.T) {
	got := NormalizeLF([]byte("a\r\nb\rc\nd"))
	if string(got) != "a\nb\nc\nd" {
		t.Fatalf("got %q", got)
	}
}

func TestBriefSHA256LineEndingInvariant(t *testing.T) {
	lf := []byte("---\nid: b_001\n---\nbody\n")
	crlf := []byte("---\r\nid: b_001\r\n---\r\nbody\r\n")
	if BriefSHA256(lf) != BriefSHA256(crlf) {
		t.Fatal("CRLF and LF briefs must hash identically")
	}
	if BriefSHA256(lf) == BriefSHA256([]byte("---\nid: b_002\n---\nbody\n")) {
		t.Fatal("different briefs must hash differently")
	}
	if len(BriefSHA256(lf)) != 64 {
		t.Fatal("want 64-char hex digest")
	}
}

func TestParseBriefClosingFenceAtEOF(t *testing.T) {
	// closing fence with no trailing newline is still closed
	doc := "---\nid: b_001\nproject: p\nconcept: c\naspect: square\n---"
	meta, body, err := ParseBrief([]byte(doc))
	if err != nil {
		t.Fatalf("fence at EOF must parse: %v", err)
	}
	if meta.ID != "b_001" || body != "" {
		t.Fatalf("got meta=%#v body=%q", meta, body)
	}
}

func TestParseBriefMissingProjectAndConcept(t *testing.T) {
	cases := map[string]string{
		"missing project": "---\nid: b_001\nconcept: c\naspect: square\n---\nbody\n",
		"missing concept": "---\nid: b_001\nproject: p\naspect: square\n---\nbody\n",
	}
	for name, doc := range cases {
		if _, _, err := ParseBrief([]byte(doc)); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestParseBriefAllValidAspects(t *testing.T) {
	for _, a := range []string{"square", "portrait_4_5", "portrait_2_3", "landscape_4_3"} {
		doc := "---\nid: b_001\nproject: p\nconcept: c\naspect: " + a + "\n---\nbody\n"
		if _, _, err := ParseBrief([]byte(doc)); err != nil {
			t.Fatalf("aspect %s must be valid: %v", a, err)
		}
	}
}
