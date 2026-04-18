package textnorm

import (
	"regexp"
	"testing"
)

func regexpMustCompile(t *testing.T, p string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(p)
	if err != nil {
		t.Fatalf("compile %q: %v", p, err)
	}
	return re
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain", "hello", "hello"},
		{"preserve_internal_spaces", "a  b", "a  b"},
		{"preserve_leading_indent", "    hello", "    hello"},
		{"trim_trailing_ws", "line1  \nline2\t ", "line1\nline2"},
		{"trim_trailing_blanks", "a\n\n\n", "a"},
		{"all_blank_becomes_empty", "\n\n\n", ""},
		{"crlf", "foo\r\nbar", "foo\nbar"},
		{"stray_cr", "foo\rbar", "foobar"},
		{"double_crlf", "a\r\n\r\nb", "a\n\nb"},
		{"csi_color", "\x1b[31mred\x1b[0m", "red"},
		{"csi_with_params", "\x1b[1;31;48;5;202mX\x1b[0m", "X"},
		{"csi_cursor_move", "\x1b[2J\x1b[Hhello", "hello"},
		{"osc_title_bel", "\x1b]0;my title\x07text", "text"},
		{"osc_title_st", "\x1b]0;my title\x1b\\text", "text"},
		{"other_esc_two_char", "\x1b(Bhello", "hello"},
		{"combined", "\x1b[1;31mERROR\x1b[0m: failed  \n\n", "ERROR: failed"},
		{"no_escape_fastpath", "abc def\nghi", "abc def\nghi"},
		{"incomplete_esc_drops", "hello\x1b", "hello"},
		{"incomplete_csi_drops", "a\x1b[31", "a"},
		{"newline_between_ansi", "\x1b[31ma\x1b[0m\n\x1b[31mb\x1b[0m", "a\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.in)
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripANSIAndCR(t *testing.T) {
	// Spec §9.6 match input: ANSI + CR removed, newlines and
	// interior whitespace preserved (unlike §8.3 Normalize, which
	// also trims).
	cases := []struct {
		name, in, want string
	}{
		{"plain", "hello", "hello"},
		{"crlf_collapses", "ready\r\n", "ready\n"},
		{"stray_cr", "ab\rcd", "abcd"},
		{"crlf_twice", "a\r\nb\r\n", "a\nb\n"},
		{"ansi_plus_crlf", "\x1b[32mready\x1b[0m\r\n", "ready\n"},
		{"trailing_ws_preserved", "ready   \n", "ready   \n"},
		{"trailing_blank_preserved", "ready\n\n", "ready\n\n"},
		{"indent_preserved", "    hi\n", "    hi\n"},
		{"no_cr_fastpath", "plain\nlines", "plain\nlines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripANSIAndCR(tc.in)
			if got != tc.want {
				t.Fatalf("StripANSIAndCR(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripANSIAndCR_RE2MultilineAnchor pins the real motivation for
// §9.6's CR rule: (?m)^ready$ must match against the CR-terminated
// form that TTY output produces. With CR preserved, the anchor sits
// between \r and \n and fails.
func TestStripANSIAndCR_RE2MultilineAnchor(t *testing.T) {
	input := "prefix\r\nready\r\nsuffix\r\n"
	normalized := StripANSIAndCR(input)

	// Sanity check the normalization shape.
	if normalized != "prefix\nready\nsuffix\n" {
		t.Fatalf("unexpected normalization: %q", normalized)
	}

	// (?m)^ready$ must match against the normalized buffer.
	pattern := `(?m)^ready$`
	re := regexpMustCompile(t, pattern)
	if !re.MatchString(normalized) {
		t.Fatalf("%q did not match %q (CR-strip is not in effect)", pattern, normalized)
	}

	// And the regression: the same pattern would NOT match the raw
	// input, which is the behavior the spec rule fixes.
	if re.MatchString(input) {
		t.Fatalf("%q matched raw CRLF input unexpectedly — test is not covering the regression case", pattern)
	}
}

func TestNormalize_Idempotent(t *testing.T) {
	inputs := []string{
		"hello",
		"a\nb\n",
		"\x1b[31mred\x1b[0m\n",
		"  indented\nline\n",
	}
	for _, s := range inputs {
		once := Normalize(s)
		twice := Normalize(once)
		if once != twice {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, once, twice)
		}
	}
}
