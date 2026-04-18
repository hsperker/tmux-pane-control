// Package waiter implements the wait match evaluators for spec §9.6:
// sentinel (structured exit-code marker), regex (RE2), and quiescence
// (no-output-for-N-ms). The package is intentionally stateless — it
// matches against a byte slice and reports where the match ended so
// callers can advance their stream cursors.
package waiter

import (
	"bytes"
	"strconv"
)

// SentinelMatch is the result of a successful sentinel search.
//
//	Matched — the whole "__DONE__:<token>:<digits>" literal
//	ExitCode — the parsed <digits> as a Go int
//	End — byte offset in the input just past the last digit; the
//	      caller uses this to position the new "next" token.
type SentinelMatch struct {
	Matched  string
	ExitCode int
	End      int
}

// MatchSentinel searches buf for the first occurrence of
// `__DONE__:<token>:<digits>` where <digits> is `[0-9]+`, and returns
// the match. buf must already be ANSI-stripped (spec §9.6). Returns
// ok=false if no such occurrence exists.
//
// Constraints on token (caller's responsibility to enforce, spec §9.6):
//   - must not contain ':'
//   - must not contain newline
//
// Signed forms (e.g. "__DONE__:abc:-1") are intentionally not matches
// because the exit-code grammar is unsigned.
func MatchSentinel(buf []byte, token string) (SentinelMatch, bool) {
	prefix := []byte("__DONE__:" + token + ":")
	offset := 0
	for {
		idx := bytes.Index(buf[offset:], prefix)
		if idx < 0 {
			return SentinelMatch{}, false
		}
		idx += offset
		start := idx + len(prefix)
		end := start
		for end < len(buf) && buf[end] >= '0' && buf[end] <= '9' {
			end++
		}
		if end > start {
			digits := string(buf[start:end])
			code, err := strconv.Atoi(digits)
			if err == nil {
				return SentinelMatch{
					Matched:  string(buf[idx:end]),
					ExitCode: code,
					End:      end,
				}, true
			}
			// Overflow on absurd digit counts; treat as no match
			// at this position and keep searching.
		}
		offset = idx + 1
	}
}
