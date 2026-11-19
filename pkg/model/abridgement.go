package model

import "encoding/json"

// Abridgement reports only what this recording states; an absent flag is unknown.
func (r Recording) Abridgement() string {
	if r.Abridged {
		return "abridged"
	}
	if r.AbridgedKnown {
		return "unabridged"
	}
	return ""
}

// UnmarshalJSON retains the difference between absent and explicitly false.
func (r *Recording) UnmarshalJSON(b []byte) error {
	type plain Recording
	var value struct {
		plain
		Abridged *bool `json:"abridged"`
	}
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	*r = Recording(value.plain)
	if value.Abridged != nil {
		r.Abridged, r.AbridgedKnown = *value.Abridged, true
	}
	return nil
}

// MarshalJSON preserves unknown flags through typed read-modify-write paths.
func (r Recording) MarshalJSON() ([]byte, error) {
	type plain Recording
	var abridged *bool
	if r.Abridged || r.AbridgedKnown {
		abridged = &r.Abridged
	}
	return json.Marshal(struct {
		plain
		Abridged *bool `json:"abridged,omitempty"`
	}{plain(r), abridged})
}
