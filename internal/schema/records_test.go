package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validRunHeader() RunHeader {
	return RunHeader{
		V: 1, Type: "run", Run: "r_0001", Project: "gradient-descent",
		Brief: "briefs/b_001.md", BriefSHA256: "9f2c", Iteration: 1,
		Created: time.Date(2026, 7, 24, 14, 31, 8, 0, time.UTC),
		Harness: "splatter v0.1.0",
	}
}

func validCallRecord() CallRecord {
	usd := 0.27
	return CallRecord{
		V: 1, Type: "call", Run: "r_0001", Call: "c_01",
		TS:       time.Date(2026, 7, 24, 14, 31, 26, 0, time.UTC),
		Provider: "gemini", ModelRequested: "m", ModelReturned: "m",
		Profile: "gemini-baseline", Operation: "generate",
		Request:  CallRequest{Prompt: "p", N: 4, Aspect: "square"},
		Response: CallResponse{LatencyMS: 18240, HTTPStatus: 200},
		Cost:     Cost{USD: &usd, Source: "reported"},
		Images: []ImageRef{{ID: "c_01_0", File: "images/c_01_0.png",
			SHA256: "ab", W: 1024, H: 1024, AspectActual: "square"}},
		Raw: "raw/c_01.json",
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []any{validRunHeader(), validCallRecord(),
		Verdict{V: 1, Type: "verdict", Run: "r_0001",
			TS: time.Date(2026, 7, 24, 15, 2, 11, 0, time.UTC), Session: "2026-07-24",
			Keep: []string{"c_01_0"}, Cull: []string{"c_01_1"},
			Notes: []VerdictNote{{Image: nil, Text: "run-level"}}},
		Critique{V: 1, Run: "r_0001", Rubric: "rubric_v1",
			Items: []CritiqueItem{{Image: "c_01_0", Verdict: "keep",
				Scores: map[string]int{"concept_fit": 4}, Reason: "r"}}},
	}
	for _, in := range cases {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("%T marshal: %v", in, err)
		}
		out := reflect.New(reflect.TypeOf(in))
		if err := json.Unmarshal(b, out.Interface()); err != nil {
			t.Fatalf("%T unmarshal: %v", in, err)
		}
		if !reflect.DeepEqual(in, out.Elem().Interface()) {
			t.Fatalf("%T round-trip mismatch:\n in: %#v\nout: %#v", in, in, out.Elem().Interface())
		}
	}
}

func TestUnknownFieldsTolerated(t *testing.T) {
	b, _ := json.Marshal(validRunHeader())
	withExtra := strings.Replace(string(b), `{`, `{"future_field":42,`, 1)
	var h RunHeader
	if err := json.Unmarshal([]byte(withExtra), &h); err != nil {
		t.Fatalf("unknown field must be tolerated: %v", err)
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("record with unknown field must validate: %v", err)
	}
}

func TestValidateCatchesMissingRequired(t *testing.T) {
	h := validRunHeader()
	h.BriefSHA256 = ""
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "brief_sha256") {
		t.Fatalf("want brief_sha256 error, got %v", err)
	}
	h = validRunHeader()
	h.V = 0
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "invalid v: want 1") {
		t.Fatalf("want v error, got %v", err)
	}
	c := validCallRecord()
	c.Cost.Source = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "cost.source") {
		t.Fatalf("want cost.source error, got %v", err)
	}
	c = validCallRecord()
	c.Operation = "remix"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("want operation error, got %v", err)
	}
	v := Verdict{V: 1, Type: "verdict", Run: "r_0001", TS: time.Now().UTC(), Session: ""}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("want session error, got %v", err)
	}
	q := Critique{V: 1, Run: "", Rubric: "rubric_v1"}
	if err := q.Validate(); err == nil || !strings.Contains(err.Error(), "run") {
		t.Fatalf("want run error, got %v", err)
	}
}

func TestRunHeaderLineage(t *testing.T) {
	parent := "r_0000"
	rel := "textual-refinement-of"
	h := validRunHeader()
	h.ParentRun, h.Relationship = &parent, &rel
	if err := h.Validate(); err != nil {
		t.Fatalf("valid lineage must pass: %v", err)
	}
	h = validRunHeader()
	h.Relationship = &rel // relationship without parent_run
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "parent_run") {
		t.Fatalf("want parent_run pairing error, got %v", err)
	}
	h = validRunHeader()
	h.ParentRun = &parent // parent_run without relationship
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "relationship") {
		t.Fatalf("want relationship pairing error, got %v", err)
	}
	h = validRunHeader()
	bad := "remix-of"
	h.ParentRun, h.Relationship = &parent, &bad
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "remix-of") {
		t.Fatalf("want invalid relationship error, got %v", err)
	}
}

func TestValidateMiscFieldErrors(t *testing.T) {
	h := validRunHeader()
	h.Iteration = 0
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "invalid iteration: 0") {
		t.Fatalf("want iteration error, got %v", err)
	}

	v := Verdict{V: 1, Type: "verdict", Run: "r_0001", TS: time.Now().UTC(), Session: "2026-07-24",
		Notes: []VerdictNote{{Text: ""}}}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "invalid notes[0]: text required") {
		t.Fatalf("want notes[0] error, got %v", err)
	}

	q := Critique{V: 1, Run: "r_0001", Rubric: "rubric_v1",
		Items: []CritiqueItem{{Image: "c_01_0", Verdict: "maybe"}}}
	if err := q.Validate(); err == nil || !strings.Contains(err.Error(), `invalid items[0].verdict: "maybe"`) {
		t.Fatalf("want items[0].verdict error, got %v", err)
	}
}

func TestFailedCallValidates(t *testing.T) {
	c := validCallRecord()
	c.Images = nil
	c.Error = &CallError{Stage: "request", HTTPStatus: 429, Message: "rate limited"}
	c.Cost = Cost{USD: nil, Source: "unavailable"}
	c.Raw = "" // a call that never reached the wire has no raw sidecar
	if err := c.Validate(); err != nil {
		t.Fatalf("failed-call record must validate: %v", err)
	}
	ok := validCallRecord()
	ok.Raw = ""
	if err := ok.Validate(); err == nil {
		t.Fatal("successful call without raw must fail validation")
	}
}
