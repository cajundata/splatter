package sheet

import (
	"bytes"
	"strings"
	"testing"
)

func renderFixture(t *testing.T) string {
	t.Helper()
	root := fixtureRun(t)
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, d); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestRenderGoldenSubstrings(t *testing.T) {
	html := renderFixture(t)
	for _, want := range []string{
		// header: project, run, iteration, brief id + concept
		"p1 / r_0002",
		"iteration 2",
		"b_001",
		"contour-line mountain range",
		// provider grouping
		"<h2>gemini</h2>",
		"<h2>openai</h2>",
		// thumbnail: full-res img hyperlinked to itself, relative path
		`<a href="images/c_01_0.png"><img src="images/c_01_0.png"`,
		// model badge and params
		"g-ret",
		"square → 1:1",
		"seed —",
		// cost with source tag
		"table:2026-07-25.0",
		// per-image keep/cull status from verdicts (later record wins)
		`<span class="status-keep">keep</span>`,
		`<span class="status-cull">cull</span>`,
		// critique verdict + scores
		"critique: keep",
		"concept_fit 4",
		"strong lines",
		// failed call renders as an error card inside its provider group
		"FAILED at request (http 429): rate limited",
		// footer totals and parent link
		"2 image(s)",
		"$0.0780",
		"1 call(s) cost unavailable",
		`<a href="../r_0001/sheet.html">`,
		// copyable verdict command block
		"<pre>splatter verdict --run r_0002 --keep c_01_0,c_01_1</pre>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered sheet missing %q", want)
		}
	}
}

func TestRenderIsSelfContained(t *testing.T) {
	html := renderFixture(t)
	for _, banned := range []string{"<script", "http://", "https://", "src=\"/", "href=\"/"} {
		if strings.Contains(html, banned) {
			t.Errorf("sheet must be self-contained; found %q", banned)
		}
	}
}

func TestRenderDeterministic(t *testing.T) {
	if renderFixture(t) != renderFixture(t) {
		t.Fatal("rendering the same run twice must produce identical bytes")
	}
}

func TestRenderMissingBriefNote(t *testing.T) {
	root := fixtureRun(t)
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	d.BriefID, d.Concept = "", ""
	d.BriefNote = "brief briefs/b_001.md not available locally (sha256 deadbeef)"
	var buf bytes.Buffer
	if err := Render(&buf, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "sha256 deadbeef") {
		t.Fatal("missing brief must render the hash note")
	}
}
