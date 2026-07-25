package run

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactRawReplacesLargePayloads(t *testing.T) {
	big := make([]byte, 5000)
	for i := range big {
		big[i] = byte(i % 251)
	}
	sum := sha256.Sum256(big)
	in := map[string]any{
		"note": "small string stays",
		"nested": map[string]any{
			"data": base64.StdEncoding.EncodeToString(big),
		},
		"list": []any{base64.StdEncoding.EncodeToString(big)},
	}
	raw, _ := json.Marshal(in)
	out := RedactRaw(raw)

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("redacted output must stay valid JSON: %v", err)
	}
	if parsed["note"] != "small string stays" {
		t.Fatal("small strings must survive")
	}
	blob := parsed["nested"].(map[string]any)["data"].(map[string]any)
	if blob["$blob"] != hex.EncodeToString(sum[:]) || blob["bytes"].(float64) != 5000 {
		t.Fatalf("bad blob replacement: %#v", blob)
	}
	if _, ok := parsed["list"].([]any)[0].(map[string]any); !ok {
		t.Fatal("payloads inside arrays must be replaced too")
	}
	if strings.Contains(string(out), base64.StdEncoding.EncodeToString(big[:100])) {
		t.Fatal("payload bytes leaked into redacted output")
	}
}

func TestRedactRawLeavesSmallAndNonBase64(t *testing.T) {
	small := base64.StdEncoding.EncodeToString(make([]byte, 100)) // valid but tiny
	raw, _ := json.Marshal(map[string]any{"a": small, "b": "definitely not base64!!"})
	var parsed map[string]any
	if err := json.Unmarshal(RedactRaw(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["a"] != small || parsed["b"] != "definitely not base64!!" {
		t.Fatalf("small/non-base64 strings must be untouched: %#v", parsed)
	}
}

func TestRedactRawNonJSONVerbatim(t *testing.T) {
	in := []byte("plain text, not json")
	if string(RedactRaw(in)) != string(in) {
		t.Fatal("non-JSON must pass through verbatim")
	}
}
