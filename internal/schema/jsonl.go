package schema

import (
	"encoding/json"
	"fmt"
)

// DecodeManifestLine parses one manifest.jsonl line into *RunHeader or
// *CallRecord based on the "type" field, validating before returning.
func DecodeManifestLine(line []byte) (any, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	switch probe.Type {
	case "run":
		var h RunHeader
		if err := json.Unmarshal(line, &h); err != nil {
			return nil, err
		}
		if err := h.Validate(); err != nil {
			return nil, err
		}
		return &h, nil
	case "call":
		var c CallRecord
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		return &c, nil
	default:
		return nil, fmt.Errorf("unknown record type %q", probe.Type)
	}
}

// DecodeVerdictLine parses and validates one verdicts.jsonl line.
func DecodeVerdictLine(line []byte) (*Verdict, error) {
	var v Verdict
	if err := json.Unmarshal(line, &v); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return &v, nil
}
