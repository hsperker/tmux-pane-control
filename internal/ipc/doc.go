// Package ipc carries requests between the short-lived CLI frontend
// and the long-lived daemon. It owns the Unix-socket transport, the
// auto-spawn path, and the §11.5 startup-race coordination. The
// socket path is derived from the tmux server identity per §11.4.
package ipc
