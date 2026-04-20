// Package tmuxctl is the tmux facade. Every call into tmux goes
// through the Port interface defined here; the real Adapter is the
// only place tpctl execs tmux. Tests use Fake for hermetic
// coverage. See spec §14 "Internal tmux integration guidance".
package tmuxctl
