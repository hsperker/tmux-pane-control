package waiter

import "testing"

func TestMatchSentinel_Basic(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		token      string
		wantOK     bool
		wantMatch  string
		wantCode   int
		wantEndInc int // end offset to verify
	}{
		{"exact", "__DONE__:abc:0", "abc", true, "__DONE__:abc:0", 0, 14},
		{"with_prefix", "hello\n__DONE__:abc:7\n", "abc", true, "__DONE__:abc:7", 7, 20},
		{"leading_zeros", "__DONE__:abc:007", "abc", true, "__DONE__:abc:007", 7, 16},
		{"multi_digit", "__DONE__:tag:255", "tag", true, "__DONE__:tag:255", 255, 16},
		{"large_unbounded", "__DONE__:x:1000", "x", true, "__DONE__:x:1000", 1000, 15},
		{"no_token", "__DONE__:abc:0", "xyz", false, "", 0, 0},
		{"no_prefix", "abc:0", "abc", false, "", 0, 0},
		{"empty_input", "", "abc", false, "", 0, 0},
		{"signed_not_matched", "__DONE__:abc:-1", "abc", false, "", 0, 0},
		{"no_digits_after_colon", "__DONE__:abc:x", "abc", false, "", 0, 0},
		{"trailing_junk_after_digits", "__DONE__:abc:42xyz", "abc", true, "__DONE__:abc:42", 42, 15},
		{"digits_then_newline", "__DONE__:abc:12\n", "abc", true, "__DONE__:abc:12", 12, 15},
		{"second_occurrence_wins_when_first_has_no_digits", "__DONE__:abc:NaN __DONE__:abc:9", "abc", true, "__DONE__:abc:9", 9, 31},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MatchSentinel([]byte(tc.input), tc.token)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (got=%+v)", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.Matched != tc.wantMatch {
				t.Fatalf("matched=%q want %q", got.Matched, tc.wantMatch)
			}
			if got.ExitCode != tc.wantCode {
				t.Fatalf("exit_code=%d want %d", got.ExitCode, tc.wantCode)
			}
			if got.End != tc.wantEndInc {
				t.Fatalf("end=%d want %d", got.End, tc.wantEndInc)
			}
		})
	}
}

func TestMatchSentinel_TokenWithSpecialChars(t *testing.T) {
	// Spec §9.6: token must not contain ':' or newline. The matcher
	// does not enforce this (caller does) but must still function
	// correctly for tokens with other characters.
	got, ok := MatchSentinel([]byte("__DONE__:abc_123:0"), "abc_123")
	if !ok || got.ExitCode != 0 {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
}

func TestMatchSentinel_PartialPrefixNoMatch(t *testing.T) {
	cases := []string{
		"__DONE_:abc:0",
		"_DONE__:abc:0",
		"__DONE__abc:0",
	}
	for _, in := range cases {
		if _, ok := MatchSentinel([]byte(in), "abc"); ok {
			t.Fatalf("unexpected match for %q", in)
		}
	}
}
