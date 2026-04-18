package waiter

import "regexp"

// RegexMatch is the result of a successful regex search.
//
//	Matched — the whole match (group 0 equivalent); spec §9.6
//	          intentionally does not expose submatches.
//	End — byte offset in the input just past the match.
type RegexMatch struct {
	Matched string
	End     int
}

// MatchRegex searches buf for the first occurrence of re and returns
// it. buf must already be ANSI-stripped (spec §9.6). Returns ok=false
// if the pattern does not match.
//
// The pattern should be compiled by the caller; regex compilation is
// a command-level concern (invalid regex → INVALID_REGEX in the CLI).
func MatchRegex(buf []byte, re *regexp.Regexp) (RegexMatch, bool) {
	loc := re.FindIndex(buf)
	if loc == nil {
		return RegexMatch{}, false
	}
	return RegexMatch{
		Matched: string(buf[loc[0]:loc[1]]),
		End:     loc[1],
	}, true
}
