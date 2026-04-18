package cli

import (
	"reflect"
	"testing"
)

func TestReorderArgs(t *testing.T) {
	bools := map[string]bool{"enter": true}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "flag_then_positional",
			in:   []string{"--pane", "%42", "hello"},
			want: []string{"--pane", "%42", "--", "hello"},
		},
		{
			name: "positional_between_flags",
			in:   []string{"--pane", "%42", "hello", "--enter"},
			want: []string{"--pane", "%42", "--enter", "--", "hello"},
		},
		{
			name: "equals_form",
			in:   []string{"--pane=%42", "hello", "--enter"},
			want: []string{"--pane=%42", "--enter", "--", "hello"},
		},
		{
			name: "explicit_double_dash",
			in:   []string{"--pane", "%42", "--", "--not-a-flag"},
			want: []string{"--pane", "%42", "--", "--not-a-flag"},
		},
		{
			name: "empty_payload",
			in:   []string{"--pane", "%42", ""},
			want: []string{"--pane", "%42", "--", ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reorderArgs(tc.in, bools)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
