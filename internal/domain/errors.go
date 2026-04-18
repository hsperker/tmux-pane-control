package domain

// ErrorCode is the canonical set of command-level error codes defined by
// spec §7.5. Implementations may emit additional codes, but the canonical
// cases must use these constants.
type ErrorCode string

const (
	ErrMissingAfter ErrorCode = "MISSING_AFTER"
	ErrInvalidAfter ErrorCode = "INVALID_AFTER"
	ErrPaneNotFound ErrorCode = "PANE_NOT_FOUND"
	ErrPaneClosed   ErrorCode = "PANE_CLOSED"
	ErrTimeout      ErrorCode = "TIMEOUT"

	// Implementation-defined (spec §7.5 allows additional codes).
	ErrInvalidKey   ErrorCode = "INVALID_KEY"
	ErrInvalidArgs  ErrorCode = "INVALID_ARGS"
	ErrInvalidRegex ErrorCode = "INVALID_REGEX"
)

// ErrorResponse is the JSON-serializable shape for command-level failures
// (spec §7.2, §7.6). PaneID is included only when the command targeted a
// specific pane and that pane was identified.
type ErrorResponse struct {
	PaneID  string    `json:"pane_id,omitempty"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// Error makes ErrorResponse usable as a Go error for internal plumbing.
// JSON output must still go through a marshaller, not this string.
func (e *ErrorResponse) Error() string {
	if e.PaneID != "" {
		return string(e.Code) + " [" + e.PaneID + "]: " + e.Message
	}
	return string(e.Code) + ": " + e.Message
}
