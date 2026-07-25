package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"gopkg.in/yaml.v3"
)

type BriefMeta struct {
	ID         string   `yaml:"id"`
	Project    string   `yaml:"project"`
	Concept    string   `yaml:"concept"`
	Mood       []string `yaml:"mood"`
	Motifs     []string `yaml:"motifs"`
	Exclusions []string `yaml:"exclusions"`
	Aspect     string   `yaml:"aspect"`
}

// harness aspect vocabulary, spec §6.1
var validAspects = map[string]bool{
	"square": true, "portrait_4_5": true, "portrait_2_3": true, "landscape_4_3": true,
}

// ParseBrief splits YAML front matter from body prose and validates
// required fields. Input is LF-normalized first so CRLF briefs parse.
func ParseBrief(data []byte) (*BriefMeta, string, error) {
	norm := NormalizeLF(data)
	if !bytes.HasPrefix(norm, []byte("---\n")) {
		return nil, "", fmt.Errorf("brief missing front matter opening ---")
	}
	rest := norm[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return nil, "", fmt.Errorf("brief front matter not closed")
	}
	var meta BriefMeta
	if err := yaml.Unmarshal(rest[:end], &meta); err != nil {
		return nil, "", fmt.Errorf("brief front matter: %w", err)
	}
	switch {
	case meta.ID == "":
		return nil, "", fmt.Errorf("brief missing required field: id")
	case meta.Project == "":
		return nil, "", fmt.Errorf("brief missing required field: project")
	case meta.Concept == "":
		return nil, "", fmt.Errorf("brief missing required field: concept")
	case !validAspects[meta.Aspect]:
		return nil, "", fmt.Errorf("brief invalid aspect: %q", meta.Aspect)
	}
	body := string(rest[end+len("\n---\n"):])
	return &meta, body, nil
}

// NormalizeLF converts CRLF and lone CR line endings to LF.
func NormalizeLF(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
}

// BriefSHA256 hashes the LF-normalized brief bytes; this is the hash
// frozen into run headers as brief_sha256.
func BriefSHA256(fileBytes []byte) string {
	sum := sha256.Sum256(NormalizeLF(fileBytes))
	return hex.EncodeToString(sum[:])
}
