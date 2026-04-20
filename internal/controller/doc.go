// Package controller owns the store and the tmux subscription.
// It runs the single-writer event loop that drains pane output into
// the store, and hosts the command-level request handlers (list,
// snapshot, read, text, key, wait). See spec §10 "Architectural
// patterns" and §13 "Internal state machines".
package controller
