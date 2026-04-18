//go:build unix

package ipc

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// EnsureDaemon connects to the daemon for the given tmux server,
// spawning one if none is running (spec §11.2). It returns a client
// ready to Call, or an error. Spawn uses the currently running
// binary (os.Executable) to exec `tpctl daemon`.
//
// The startup race coordination (§11.5) uses flock on a per-server
// lock file in the same directory as the socket.
func EnsureDaemon(tmuxSocket string) (*Client, error) {
	sockPath, err := DaemonSocketPath(tmuxSocket)
	if err != nil {
		return nil, err
	}
	client := NewClient(sockPath)

	// Fast path: already running.
	if err := client.Ping(); err == nil {
		return client, nil
	}

	// Lock; re-check after acquiring in case another process raced
	// us to the spawn.
	lockPath := sockPath + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	if err := client.Ping(); err == nil {
		return client, nil
	}

	// Spawn a detached `tpctl daemon` with explicit sockets so it
	// doesn't need to re-derive them.
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("os.Executable: %w", err)
	}
	cmd := exec.Command(exe, "daemon",
		"--tmux-socket", tmuxSocket,
		"--daemon-socket", sockPath,
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn daemon: %w", err)
	}
	// Detach: don't wait on the child.
	go func() { _ = cmd.Wait() }()

	// Poll readiness with exponential backoff up to ~2s total.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Ping(); err == nil {
			return client, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon failed to become ready at %s", sockPath)
}
