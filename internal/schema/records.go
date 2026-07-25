package schema

import (
	"fmt"
	"time"
)

type RunHeader struct {
	V            int       `json:"v"`
	Type         string    `json:"type"` // "run"
	Run          string    `json:"run"`
	Project      string    `json:"project"`
	Brief        string    `json:"brief"`
	BriefSHA256  string    `json:"brief_sha256"`
	Iteration    int       `json:"iteration"`
	ParentRun    *string   `json:"parent_run"`
	Relationship *string   `json:"relationship"` // "textual-refinement-of" | "regeneration-of"
	Created      time.Time `json:"created"`
	Harness      string    `json:"harness"`
}

type CallRequest struct {
	Prompt string         `json:"prompt"`
	N      int            `json:"n"`
	Aspect string         `json:"aspect"`
	Seed   *int64         `json:"seed"`
	Native map[string]any `json:"native,omitempty"`
}

type CallResponse struct {
	LatencyMS         int64  `json:"latency_ms"`
	HTTPStatus        int    `json:"http_status"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
}

type Cost struct {
	USD    *float64 `json:"usd"`    // null when source == "unavailable"
	Source string   `json:"source"` // "reported" | "table:<version>" | "unavailable"
}

type ImageRef struct {
	ID           string `json:"id"`
	File         string `json:"file"` // relative, slash-normalized
	SHA256       string `json:"sha256"`
	W            int    `json:"w"`
	H            int    `json:"h"`
	AspectActual string `json:"aspect_actual"`
}

type CallError struct {
	Stage      string `json:"stage"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Message    string `json:"message"`
}

type CallRecord struct {
	V              int          `json:"v"`
	Type           string       `json:"type"` // "call"
	Run            string       `json:"run"`
	Call           string       `json:"call"`
	TS             time.Time    `json:"ts"`
	Provider       string       `json:"provider"`
	ModelRequested string       `json:"model_requested"`
	ModelReturned  string       `json:"model_returned"`
	Profile        string       `json:"profile"`
	Operation      string       `json:"operation"` // "generate" | "edit"
	Request        CallRequest  `json:"request"`
	Response       CallResponse `json:"response"`
	Cost           Cost         `json:"cost"`
	Images         []ImageRef   `json:"images"`
	Raw            string       `json:"raw"`
	Error          *CallError   `json:"error"`
}

type VerdictNote struct {
	Image *string `json:"image"` // nil = run-level note
	Text  string  `json:"text"`
}

type Verdict struct {
	V       int           `json:"v"`
	Type    string        `json:"type"` // "verdict"
	Run     string        `json:"run"`
	TS      time.Time     `json:"ts"`
	Session string        `json:"session"`
	Keep    []string      `json:"keep"`
	Cull    []string      `json:"cull"`
	Notes   []VerdictNote `json:"notes"`
}

type CritiqueItem struct {
	Image   string         `json:"image"`
	Verdict string         `json:"verdict"` // "keep" | "cull"
	Scores  map[string]int `json:"scores"`
	Reason  string         `json:"reason"`
}

type Critique struct {
	V      int            `json:"v"`
	Run    string         `json:"run"`
	Rubric string         `json:"rubric"`
	Items  []CritiqueItem `json:"items"`
}

func missing(name string) error { return fmt.Errorf("missing required field: %s", name) }

func (h RunHeader) Validate() error {
	switch {
	case h.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", h.V)
	case h.Type != "run":
		return fmt.Errorf("invalid type: want \"run\", got %q", h.Type)
	case h.Run == "":
		return missing("run")
	case h.Project == "":
		return missing("project")
	case h.Brief == "":
		return missing("brief")
	case h.BriefSHA256 == "":
		return missing("brief_sha256")
	case h.Iteration < 1:
		return fmt.Errorf("invalid iteration: %d", h.Iteration)
	case h.Created.IsZero():
		return missing("created")
	case h.Harness == "":
		return missing("harness")
	}
	if h.Relationship != nil {
		if h.ParentRun == nil {
			return fmt.Errorf("invalid relationship: set without parent_run")
		}
		if r := *h.Relationship; r != "textual-refinement-of" && r != "regeneration-of" {
			return fmt.Errorf("invalid relationship: %q", r)
		}
	}
	if h.ParentRun != nil && h.Relationship == nil {
		return fmt.Errorf("invalid parent_run: set without relationship")
	}
	return nil
}

func (c CallRecord) Validate() error {
	switch {
	case c.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", c.V)
	case c.Type != "call":
		return fmt.Errorf("invalid type: want \"call\", got %q", c.Type)
	case c.Run == "":
		return missing("run")
	case c.Call == "":
		return missing("call")
	case c.TS.IsZero():
		return missing("ts")
	case c.Provider == "":
		return missing("provider")
	case c.Operation != "generate" && c.Operation != "edit":
		return fmt.Errorf("invalid operation: %q", c.Operation)
	case c.Cost.Source == "":
		return missing("cost.source")
	}
	if len(c.Images) == 0 && c.Error == nil {
		return fmt.Errorf("invalid record: no images and no error")
	}
	// A failed call may never have produced a wire body; raw is required
	// only for successful calls.
	if c.Error == nil && c.Raw == "" {
		return missing("raw")
	}
	for i, img := range c.Images {
		if img.ID == "" || img.File == "" || img.SHA256 == "" {
			return fmt.Errorf("invalid images[%d]: id, file, sha256 all required", i)
		}
		if img.W < 1 || img.H < 1 {
			return fmt.Errorf("invalid images[%d]: w and h must be >= 1", i)
		}
		if img.AspectActual == "" {
			return fmt.Errorf("invalid images[%d]: aspect_actual required", i)
		}
	}
	return nil
}

func (v Verdict) Validate() error {
	switch {
	case v.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", v.V)
	case v.Type != "verdict":
		return fmt.Errorf("invalid type: want \"verdict\", got %q", v.Type)
	case v.Run == "":
		return missing("run")
	case v.TS.IsZero():
		return missing("ts")
	case v.Session == "":
		return missing("session")
	}
	for i, n := range v.Notes {
		if n.Text == "" {
			return fmt.Errorf("invalid notes[%d]: text required", i)
		}
	}
	return nil
}

func (q Critique) Validate() error {
	switch {
	case q.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", q.V)
	case q.Run == "":
		return missing("run")
	case q.Rubric == "":
		return missing("rubric")
	}
	for i, it := range q.Items {
		if it.Image == "" {
			return fmt.Errorf("invalid items[%d]: image required", i)
		}
		if it.Verdict != "keep" && it.Verdict != "cull" {
			return fmt.Errorf("invalid items[%d].verdict: %q", i, it.Verdict)
		}
	}
	return nil
}
