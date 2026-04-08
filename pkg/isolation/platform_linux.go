//go:build linux

package isolation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

func applyPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled {
		return nil
	}
	// Bubblewrap is the only supported Linux backend right now. Fail closed when
	// it is unavailable instead of silently running the child process unisolated.
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		hint := bwrapInstallHint()
		disableHint := `set "isolation.enabled": false in config.json`
		logger.WarnCF("isolation", "bubblewrap is required for Linux isolation",
			map[string]any{
				"binary":            "bwrap",
				"install":           hint,
				"disable_isolation": disableHint,
				"risk":              "disabling isolation lets child processes run without Linux filesystem isolation",
			})
		return fmt.Errorf("linux isolation requires bwrap and does not fall back automatically: %w; install bubblewrap with one of: %s; or disable isolation by setting %s; disabling isolation means child processes can run without Linux filesystem isolation and may access or modify more host files", err, hint, disableHint)
	}
	if cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return nil
	}

	originalPath := cmd.Path
	originalArgs := append([]string{}, cmd.Args...)
	originalDir := cmd.Dir

	// Start from the configured mount plan, then add the executable, its resolved
	// path, the working directory, and any absolute path arguments the process may
	// need at runtime.
	plan := BuildLinuxMountPlan(root, isolation.ExposePaths)
	plan = ensureLinuxMountRule(plan, originalPath, originalPath, "ro")
	plan = ensureLinuxMountRule(plan, filepath.Dir(originalPath), filepath.Dir(originalPath), "ro")
	if resolved, err := filepath.EvalSymlinks(originalPath); err == nil && resolved != originalPath {
		plan = ensureLinuxMountRule(plan, resolved, resolved, "ro")
		plan = ensureLinuxMountRule(plan, filepath.Dir(resolved), filepath.Dir(resolved), "ro")
	}
	if originalDir != "" {
		plan = ensureLinuxMountRule(plan, originalDir, originalDir, "rw")
		if resolved, err := filepath.EvalSymlinks(originalDir); err == nil && resolved != originalDir {
			plan = ensureLinuxMountRule(plan, resolved, resolved, "rw")
		}
	}
	for _, arg := range originalArgs[1:] {
		if filepath.IsAbs(arg) {
			plan = ensureLinuxPathForArgument(plan, arg)
		}
	}
	logger.DebugCF("isolation", "linux isolation mount plan",
		map[string]any{
			"root":        root,
			"command":     originalPath,
			"working_dir": originalDir,
			"mounts":      formatLinuxMountPlan(plan),
		})
	bwrapArgs, err := buildLinuxBwrapArgs(originalPath, originalArgs, originalDir, plan)
	if err != nil {
		return err
	}

	cmd.Path = bwrapPath
	cmd.Args = bwrapArgs
	cmd.Dir = ""
	return nil
}

func bwrapInstallHint() string {
	return "apt install bubblewrap; dnf install bubblewrap; yum install bubblewrap; pacman -S bubblewrap; apk add bubblewrap"
}

// formatLinuxMountPlan reshapes the internal plan for structured logging.
func formatLinuxMountPlan(plan []MountRule) []map[string]string {
	formatted := make([]map[string]string, 0, len(plan))
	for _, rule := range plan {
		formatted = append(formatted, map[string]string{
			"source": rule.Source,
			"target": rule.Target,
			"mode":   rule.Mode,
		})
	}
	return formatted
}

func postStartPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	return nil
}

// buildLinuxBwrapArgs translates the mount plan into the bubblewrap command
// line that re-executes the original process inside the isolated mount view.
func buildLinuxBwrapArgs(originalPath string, originalArgs []string, originalDir string, plan []MountRule) ([]string, error) {
	bwrapArgs := []string{
		"bwrap",
		"--die-with-parent",
		"--unshare-ipc",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	for _, rule := range plan {
		flag, err := linuxBindFlag(rule)
		if err != nil {
			return nil, err
		}
		bwrapArgs = append(bwrapArgs, flag, rule.Source, rule.Target)
	}
	if originalDir != "" {
		bwrapArgs = append(bwrapArgs, "--chdir", originalDir)
	}
	bwrapArgs = append(bwrapArgs, "--", originalPath)
	if len(originalArgs) > 1 {
		bwrapArgs = append(bwrapArgs, originalArgs[1:]...)
	}
	return bwrapArgs, nil
}

// ensureLinuxMountRule appends a mount rule unless another rule already owns
// the same target path.
func ensureLinuxMountRule(plan []MountRule, source, target, mode string) []MountRule {
	cleanSource := filepath.Clean(source)
	cleanTarget := filepath.Clean(target)
	for _, rule := range plan {
		if filepath.Clean(rule.Target) == cleanTarget {
			return plan
		}
	}
	return append(plan, MountRule{Source: cleanSource, Target: cleanTarget, Mode: mode})
}

// linuxBindFlag selects the correct bubblewrap bind flag based on mount mode.
func linuxBindFlag(rule MountRule) (string, error) {
	info, err := os.Stat(rule.Source)
	if err != nil {
		return "", fmt.Errorf("stat linux mount source %s: %w", rule.Source, err)
	}
	if !info.IsDir() {
		if rule.Mode == "rw" {
			return "--bind", nil
		}
		return "--ro-bind", nil
	}
	if rule.Mode == "rw" {
		return "--bind", nil
	}
	return "--ro-bind", nil
}

// ensureLinuxPathForArgument exposes absolute-path arguments conservatively so
// common CLI flags that point at files or directories keep working in the
// isolated filesystem view.
func ensureLinuxPathForArgument(plan []MountRule, arg string) []MountRule {
	clean := filepath.Clean(arg)
	if info, err := os.Stat(clean); err == nil {
		if info.IsDir() {
			return ensureLinuxMountRule(plan, clean, clean, "rw")
		}
		return ensureLinuxMountRule(plan, filepath.Dir(clean), filepath.Dir(clean), "rw")
	}
	parent := filepath.Dir(clean)
	if parent == "." || parent == "/" {
		return plan
	}
	if _, err := os.Stat(parent); err == nil {
		return ensureLinuxMountRule(plan, parent, parent, "rw")
	}
	return plan
}
