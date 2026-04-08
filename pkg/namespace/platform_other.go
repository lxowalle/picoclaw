//go:build !linux && !windows

package namespace

import (
	"os/exec"

	"github.com/sipeed/picoclaw/pkg/config"
)

func applyPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	return nil
}

func postStartPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	return nil
}
