package waiter

import (
	"regexp"
	"testing"
)

func mustCompile(t *testing.T, pat string) *regexp.Regexp {
	t.Helper()
	r, err := regexp.Compile(pat)
	if err != nil {
		t.Fatalf("compile %q: %v", pat, err)
	}
	return r
}

func TestMatchRegex_Basic(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		pattern   string
		wantOK    bool
		wantMatch string
	}{
		{"literal", "server is READY\n", "READY", true, "READY"},
		{"no_match", "hello world", "READY", false, ""},
		{"first_of_many", "READY\nREADY\n", "READY", true, "READY"},
		{"multi_char_class", "abc123def", "[0-9]+", true, "123"},
		{"no_dotall_default", "a\nb", "a.b", false, ""},
		{"dotall_flag", "a\nb", "(?s)a.b", true, "a\nb"},
		{"anchored_not_implicit", "junk READY", "^READY", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re := mustCompile(t, tc.pattern)
			got, ok := MatchRegex([]byte(tc.input), re)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (got=%+v)", ok, tc.wantOK, got)
			}
			if ok && got.Matched != tc.wantMatch {
				t.Fatalf("matched=%q want %q", got.Matched, tc.wantMatch)
			}
		})
	}
}
