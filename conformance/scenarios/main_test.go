// Suite entry + suite-wide sanity checks.

package scenarios

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestMain records the number of tpctl daemon processes at suite
// start, runs the scenarios, and verifies the count has not grown
// on exit. A growth would indicate one or more scenarios leaked
// a daemon despite per-scenario teardown, which would be a §11.9
// or §11.2 bug somewhere.
//
// Linux-only probe via /proc; other platforms skip the check.
func TestMain(m *testing.M) {
	before := countDaemonProcesses()
	code := m.Run()
	after := countDaemonProcesses()

	if before >= 0 && after > before {
		// Log and bump exit code. We don't want to mask a
		// scenario-level failure either — preserve non-zero.
		os.Stderr.WriteString("conformance TestMain: daemon count grew from " +
			strconv.Itoa(before) + " to " + strconv.Itoa(after) + "\n")
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// countDaemonProcesses returns the number of processes whose argv
// includes "tpctl daemon", or -1 on platforms where /proc is not
// available (in which case the suite skips the zombie sanity check).
func countDaemonProcesses() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1
	}
	n := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(ent.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + ent.Name() + "/cmdline")
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(parts) >= 2 && strings.Contains(parts[0], "tpctl") && parts[1] == "daemon" {
			n++
		}
	}
	return n
}

