// Package textnorm implements the text normalization rules defined by
// spec §8.3. It is a pure function on strings with no external
// dependencies; all text-bearing JSON fields pass through Normalize
// before serialization.
package textnorm

import "strings"

// Normalize applies the §8.3 rules in order:
//
//  1. strip ANSI control sequences (CSI, OSC, DCS, single-char ESC)
//  2. drop carriage returns entirely; \r\n and stray \r both collapse
//     to \n or nothing respectively
//  3. trim trailing whitespace (spaces/tabs) from each line
//  4. remove trailing blank lines
//
// Leading whitespace and interior runs of whitespace are preserved so
// indentation survives round-tripping.
func Normalize(s string) string {
	s = stripANSI(s)
	if strings.IndexByte(s, '\r') >= 0 {
		s = strings.ReplaceAll(s, "\r", "")
	}

	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// stripANSI removes ANSI escape sequences. It's a small hand-rolled
// state machine to keep the package dependency-free. The grammar
// covered:
//
//   - CSI: ESC [ 0x30-0x3F* 0x20-0x2F* 0x40-0x7E
//   - OSC: ESC ]  ...  BEL | ESC \
//   - DCS/SOS/PM/APC: ESC (P|X|^|_) ... ESC \
//   - any other two-byte ESC sequence (charset designation, single
//     shifts, etc.) is dropped
//
// Incomplete trailing sequences at the end of input are dropped.
func stripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3F {
				j++
			}
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2F {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7E {
				j++
			}
			i = j
		case ']':
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		case 'P', 'X', '^', '_':
			j := i + 2
			for j < len(s) {
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default:
			// Generic ECMA-48 escape sequence: ESC I* F
			// with I in 0x20-0x2F and F in 0x30-0x7E. Covers
			// charset designators (ESC ( B), single-char ops
			// (ESC 7, ESC =), and private-use forms.
			j := i + 1
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2F {
				j++
			}
			if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7E {
				j++
			}
			i = j
		}
	}
	return b.String()
}
