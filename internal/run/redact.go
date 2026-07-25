// Package run orchestrates provider calls and owns all evidence writing:
// run IDs, manifest records, image files, redacted raw sidecars, and cost
// resolution. Adapters never touch the filesystem.
package run

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

// blobThreshold: base64 payloads decoding beyond this are replaced in raw
// sidecars, keeping evidence auditable without megabytes of image data.
const blobThreshold = 4096

// RedactRaw replaces large base64 payloads with {"$blob":sha256,"bytes":N}.
// Non-JSON input is returned verbatim (fallback preserves evidence).
func RedactRaw(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return raw
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = redactValue(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = redactValue(e)
		}
		return t
	case string:
		// quick reject: decoded size can't exceed threshold
		if len(t) < blobThreshold*4/3 {
			return t
		}
		decoded, err := base64.StdEncoding.DecodeString(t)
		if err != nil || len(decoded) <= blobThreshold {
			return t
		}
		sum := sha256.Sum256(decoded)
		return map[string]any{"$blob": hex.EncodeToString(sum[:]), "bytes": len(decoded)}
	default:
		return v
	}
}
