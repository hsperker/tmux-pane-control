package ipc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DaemonSocketPath derives the controller socket path from the tmux
// server's resolved socket path per spec §11.4:
//
//	$XDG_RUNTIME_DIR/tpctl/<hash-of-resolved-tmux-socket-path>.sock
//
// If XDG_RUNTIME_DIR is unset, os.TempDir() is used (still per-user
// on typical Unix systems). The directory is created if missing.
func DaemonSocketPath(tmuxSocket string) (string, error) {
	dir, err := daemonDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	h := sha256.Sum256([]byte(tmuxSocket))
	return filepath.Join(dir, hex.EncodeToString(h[:8])+".sock"), nil
}

func daemonDir() (string, error) {
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "tpctl"), nil
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("tpctl-%d", os.Geteuid())), nil
}

// ResolveTmuxSocket returns the absolute path tmux would use to key
// its server, per spec §11.4 precedence:
//
//  1. explicit --tmux-socket PATH (socketPath)
//  2. --tmux-socket-name NAME resolved to /tmp/tmux-UID/NAME (socketName)
//  3. $TMUX's leading component
//  4. tmux default: /tmp/tmux-UID/default
//
// Passing both socketPath and socketName is caller-rejected per §11.4.
func ResolveTmuxSocket(socketPath, socketName string) (string, error) {
	if socketPath != "" && socketName != "" {
		return "", errors.New("--tmux-socket and --tmux-socket-name are mutually exclusive")
	}
	if socketPath != "" {
		abs, err := filepath.Abs(socketPath)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	uidDir := fmt.Sprintf("/tmp/tmux-%d", os.Geteuid())
	if socketName != "" {
		return filepath.Join(uidDir, socketName), nil
	}
	if tmux := os.Getenv("TMUX"); tmux != "" {
		if comma := strings.IndexByte(tmux, ','); comma >= 0 {
			return tmux[:comma], nil
		}
		return tmux, nil
	}
	return filepath.Join(uidDir, "default"), nil
}
