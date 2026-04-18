package domain

// PaneID is a tmux pane identifier like "%42". It is the canonical pane
// identity in the public API (spec §5).
type PaneID string

// Token is an opaque checkpoint token. Agents must not parse it; the
// controller owns its format. Scoped to a single pane within a single
// controller lifetime (spec §4.3).
type Token string

// ListResponse is the shape for `tpctl list` (spec §9.1).
// Panes is always present, even when empty (spec §8.1).
type ListResponse struct {
	Panes []PaneID `json:"panes"`
}

// SnapshotResponse is the shape for `tpctl snapshot` (spec §9.2).
// ScrollbackText is present iff the caller supplied --history-lines,
// represented here by a pointer; a non-nil empty string encodes
// `scrollback_text: ""`.
type SnapshotResponse struct {
	PaneID         PaneID  `json:"pane_id"`
	Next           Token   `json:"next"`
	ScrollbackText *string `json:"scrollback_text,omitempty"`
	Text           string  `json:"text"`
}

// ReadResponse is the shape for `tpctl read` (spec §9.3).
// Text is always present; an empty string is a success.
type ReadResponse struct {
	PaneID PaneID `json:"pane_id"`
	Next   Token  `json:"next"`
	Text   string `json:"text"`
}

// WaitResult is the discriminator for wait responses.
type WaitResult string

const (
	WaitSentinel   WaitResult = "sentinel"
	WaitRegex      WaitResult = "regex"
	WaitQuiescence WaitResult = "quiescence"
)

// WaitResponse is the shape for `tpctl wait` (spec §9.6).
// Matched is present for sentinel and regex. ExitCode is present only
// for sentinel; pointer distinguishes "absent" from "zero".
type WaitResponse struct {
	PaneID   PaneID     `json:"pane_id"`
	Next     Token      `json:"next"`
	Result   WaitResult `json:"result"`
	Matched  *string    `json:"matched,omitempty"`
	ExitCode *int       `json:"exit_code,omitempty"`
}

// NewString returns a pointer to the given string. Useful for building
// responses with optional string fields.
func NewString(s string) *string { return &s }

// NewInt returns a pointer to the given int. Useful for building
// responses with optional integer fields (e.g. ExitCode).
func NewInt(i int) *int { return &i }
