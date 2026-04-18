// Package ipc defines the wire protocol between the short-lived CLI
// client and the long-lived daemon (spec §11). Encoding is
// line-delimited JSON: one Request per line from the client, one
// Response per line back from the server. Keep-alive is unnecessary
// because each request/response pair is self-contained.
package ipc

import (
	"encoding/json"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// Op identifies the operation carried in a Request.
type Op string

const (
	OpList     Op = "list"
	OpSnapshot Op = "snapshot"
	OpRead     Op = "read"
	OpText     Op = "text"
	OpKey      Op = "key"
	OpWait     Op = "wait"
	// OpShutdown asks the daemon to exit cleanly. Not part of the
	// public CLI; used by tests and by controlled teardown.
	OpShutdown Op = "shutdown"
)

// Request is the union of every arg every handler needs. Fields are
// tagged omitempty so unused ones don't appear on the wire.
type Request struct {
	Op Op `json:"op"`

	Pane  domain.PaneID `json:"pane,omitempty"`
	After domain.Token  `json:"after,omitempty"`

	// snapshot
	HistoryLines int  `json:"history_lines,omitempty"`
	HasHistory   bool `json:"has_history,omitempty"`

	// text
	Text  string `json:"text,omitempty"`
	Enter bool   `json:"enter,omitempty"`

	// key
	Keys []string `json:"keys,omitempty"`

	// wait
	Mode          string `json:"mode,omitempty"`
	SentinelToken string `json:"sentinel_token,omitempty"`
	Pattern       string `json:"pattern,omitempty"`
	QuietMs       int    `json:"quiet_ms,omitempty"`
	TimeoutMs     int    `json:"timeout_ms,omitempty"`
}

// Response envelope. On success either Body carries the command's
// JSON payload (list/snapshot/read/wait) or Body is empty for
// text/key. On command-level failure Error is set. On runtime failure
// Runtime carries a diagnostic that the client surfaces on stderr.
type Response struct {
	OK      bool                  `json:"ok"`
	Body    json.RawMessage       `json:"body,omitempty"`
	Error   *domain.ErrorResponse `json:"error,omitempty"`
	Runtime string                `json:"runtime,omitempty"`
}
