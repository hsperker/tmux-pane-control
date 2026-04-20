// Package waiter holds the three pure match evaluators used by
// `tpctl wait`: sentinel (spec §9.6 sentinel grammar), regex (RE2),
// and quiescence (time-based idle detection). Each is a pure
// function on a byte slice; side effects, scheduling, and timeout
// handling live in the controller.
package waiter
