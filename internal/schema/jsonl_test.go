package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeManifestLineDispatchesOnType(t *testing.T) {
	hb, _ := json.Marshal(validRunHeader())
	got, err := DecodeManifestLine(hb)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*RunHeader); !ok {
		t.Fatalf("want *RunHeader, got %T", got)
	}
	cb, _ := json.Marshal(validCallRecord())
	got, err = DecodeManifestLine(cb)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*CallRecord); !ok {
		t.Fatalf("want *CallRecord, got %T", got)
	}
}

func TestDecodeManifestLineRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"malformed json": `{"v":1,"type":"run"`,
		"unknown type":   `{"v":1,"type":"mystery"}`,
		"invalid record": `{"v":1,"type":"run","run":"r_0001"}`,
	}
	for name, line := range cases {
		if _, err := DecodeManifestLine([]byte(line)); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestDecodeVerdictLine(t *testing.T) {
	line := `{"v":1,"type":"verdict","run":"r_0001","ts":"2026-07-24T15:02:11Z",` +
		`"session":"2026-07-24","keep":["c_01_0"],"cull":[],"notes":[]}`
	v, err := DecodeVerdictLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if v.Run != "r_0001" || len(v.Keep) != 1 {
		t.Fatalf("bad decode: %#v", v)
	}
	if _, err := DecodeVerdictLine([]byte(`{"v":1,"type":"verdict"}`)); err == nil ||
		!strings.Contains(err.Error(), "run") {
		t.Fatalf("want run error, got %v", err)
	}
}
