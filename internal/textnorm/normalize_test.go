package textnorm

import "testing"

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
