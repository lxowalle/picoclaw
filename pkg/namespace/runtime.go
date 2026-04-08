package namespace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/config"
)

type MountRule struct {
	Source string
	Target string
	Mode   string
}

type AccessRule struct {
	Path string
	Mode string
}

type UserEnv struct {
	Home         string
	Tmp          string
	Config       string
	Cache        string
	State        string
	AppData      string
	LocalAppData string
}

var (
	isolationMu      sync.RWMutex
	currentIsolation = config.DefaultConfig().Isolation
	currentWorkspace = config.DefaultConfig().WorkspacePath()
)

func Configure(cfg *config.Config) {
	isolationMu.Lock()
	defer isolationMu.Unlock()
	if cfg == nil {
		defaults := config.DefaultConfig()
		currentIsolation = defaults.Isolation
		currentWorkspace = defaults.WorkspacePath()
		return
	}
	currentIsolation = cfg.Isolation
	currentWorkspace = filepath.Clean(cfg.WorkspacePath())
}

func CurrentConfig() config.IsolationConfig {
	isolationMu.RLock()
	defer isolationMu.RUnlock()
	return currentIsolation
}

func CurrentWorkspace() string {
	isolationMu.RLock()
	defer isolationMu.RUnlock()
	return currentWorkspace
}

func ResolveInstanceRoot() (string, error) {
	root := filepath.Clean(config.GetHome())
	if root == "." {
		return "", fmt.Errorf("instance root resolved to current directory")
	}
	return root, nil
}

func PrepareInstanceRoot(root string) error {
	for _, dir := range InstanceDirs(root) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("prepare instance dir %s: %w", dir, err)
		}
	}
	return nil
}

func InstanceDirs(root string) []string {
	dirs := []string{
		root,
		filepath.Join(root, "skills"),
		filepath.Join(root, "logs"),
		filepath.Join(root, "cache"),
		filepath.Join(root, "state"),
		filepath.Join(root, "runtime-user-env"),
		filepath.Join(root, "runtime-user-env", "home"),
		filepath.Join(root, "runtime-user-env", "tmp"),
		filepath.Join(root, "runtime-user-env", "config"),
		filepath.Join(root, "runtime-user-env", "cache"),
		filepath.Join(root, "runtime-user-env", "state"),
	}
	workspace := CurrentWorkspace()
	if workspace == "" {
		workspace = filepath.Join(root, pkg.WorkspaceName)
	}
	dirs = append(dirs, workspace)
	if runtime.GOOS == "windows" {
		dirs = append(dirs,
			filepath.Join(root, "runtime-user-env", "AppData", "Roaming"),
			filepath.Join(root, "runtime-user-env", "AppData", "Local"),
		)
	}
	return dirs
}

func ResolveUserEnv(root string) UserEnv {
	base := filepath.Join(root, "runtime-user-env")
	return UserEnv{
		Home:         filepath.Join(base, "home"),
		Tmp:          filepath.Join(base, "tmp"),
		Config:       filepath.Join(base, "config"),
		Cache:        filepath.Join(base, "cache"),
		State:        filepath.Join(base, "state"),
		AppData:      filepath.Join(base, "AppData", "Roaming"),
		LocalAppData: filepath.Join(base, "AppData", "Local"),
	}
}

func ApplyUserEnv(cmd *exec.Cmd, root string) {
	userEnv := ResolveUserEnv(root)
	envMap := make(map[string]string)
	for _, item := range cmd.Environ() {
		if idx := strings.IndexRune(item, '='); idx > 0 {
			envMap[item[:idx]] = item[idx+1:]
		}
	}

	if runtime.GOOS == "windows" {
		envMap["USERPROFILE"] = userEnv.Home
		envMap["HOME"] = userEnv.Home
		envMap["TEMP"] = userEnv.Tmp
		envMap["TMP"] = userEnv.Tmp
		envMap["APPDATA"] = userEnv.AppData
		envMap["LOCALAPPDATA"] = userEnv.LocalAppData
	} else {
		envMap["HOME"] = userEnv.Home
		envMap["TMPDIR"] = userEnv.Tmp
		envMap["XDG_CONFIG_HOME"] = userEnv.Config
		envMap["XDG_CACHE_HOME"] = userEnv.Cache
		envMap["XDG_STATE_HOME"] = userEnv.State
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env
}

func ValidateExposePaths(items []config.ExposePath) error {
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.Source == "" {
			return fmt.Errorf("source is required")
		}
		if item.Mode != "ro" && item.Mode != "rw" {
			return fmt.Errorf("invalid expose_paths mode: %s", item.Mode)
		}

		source := filepath.Clean(item.Source)
		target := item.Target
		if target == "" {
			target = source
		}
		target = filepath.Clean(target)

		if !filepath.IsAbs(source) || !filepath.IsAbs(target) {
			return fmt.Errorf("source and target must be absolute paths")
		}
		if _, ok := seen[target]; ok {
			return fmt.Errorf("duplicate expose_path target: %s", target)
		}
		seen[target] = struct{}{}
	}
	return nil
}

func NormalizeExposePath(item config.ExposePath) config.ExposePath {
	source := filepath.Clean(item.Source)
	target := item.Target
	if target == "" {
		target = source
	}
	return config.ExposePath{
		Source: source,
		Target: filepath.Clean(target),
		Mode:   item.Mode,
	}
}

func DefaultExposePaths(root string) []config.ExposePath {
	items := []config.ExposePath{{
		Source: root,
		Target: root,
		Mode:   "rw",
	}}
	if runtime.GOOS == "linux" {
		items = append(items,
			config.ExposePath{Source: "/usr", Target: "/usr", Mode: "ro"},
			config.ExposePath{Source: "/bin", Target: "/bin", Mode: "ro"},
			config.ExposePath{Source: "/lib", Target: "/lib", Mode: "ro"},
			config.ExposePath{Source: "/lib64", Target: "/lib64", Mode: "ro"},
			config.ExposePath{Source: "/etc/resolv.conf", Target: "/etc/resolv.conf", Mode: "ro"},
		)
	}
	return items
}

func MergeExposePaths(defaults []config.ExposePath, overrides []config.ExposePath) []config.ExposePath {
	merged := make([]config.ExposePath, 0, len(defaults)+len(overrides))
	indexByTarget := make(map[string]int, len(defaults)+len(overrides))
	appendOrReplace := func(item config.ExposePath) {
		normalized := NormalizeExposePath(item)
		if idx, ok := indexByTarget[normalized.Target]; ok {
			merged[idx] = normalized
			return
		}
		indexByTarget[normalized.Target] = len(merged)
		merged = append(merged, normalized)
	}
	for _, item := range defaults {
		appendOrReplace(item)
	}
	for _, item := range overrides {
		appendOrReplace(item)
	}
	return merged
}

func BuildLinuxMountPlan(root string, overrides []config.ExposePath) []MountRule {
	merged := MergeExposePaths(DefaultExposePaths(root), overrides)
	plan := make([]MountRule, 0, len(merged))
	for _, item := range merged {
		plan = append(plan, MountRule{Source: item.Source, Target: item.Target, Mode: item.Mode})
	}
	return plan
}

func BuildWindowsAccessRules(root string, overrides []config.ExposePath) []AccessRule {
	rules := []AccessRule{{Path: root, Mode: "rw"}}
	if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
		rules = append(rules,
			AccessRule{Path: filepath.Join(userProfile, ".ssh"), Mode: "deny"},
			AccessRule{Path: filepath.Join(userProfile, ".gitconfig"), Mode: "deny"},
			AccessRule{Path: filepath.Join(userProfile, "Documents"), Mode: "deny"},
			AccessRule{Path: filepath.Join(userProfile, "Desktop"), Mode: "deny"},
			AccessRule{Path: filepath.Join(userProfile, "Downloads"), Mode: "deny"},
		)
	}
	for _, item := range MergeExposePaths(nil, overrides) {
		rules = append(rules, AccessRule{Path: item.Source, Mode: item.Mode})
	}
	return rules
}

func IsSupported() bool {
	switch runtime.GOOS {
	case "linux", "windows":
		return true
	default:
		return false
	}
}

func Preflight() error {
	isolation := CurrentConfig()
	if !isolation.Enabled {
		return nil
	}
	root, err := ResolveInstanceRoot()
	if err != nil {
		return err
	}
	if err := PrepareInstanceRoot(root); err != nil {
		return err
	}
	if err := ValidateExposePaths(isolation.ExposePaths); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		for _, rule := range BuildLinuxMountPlan(root, isolation.ExposePaths) {
			if rule.Source == "" || rule.Target == "" {
				return fmt.Errorf("invalid linux mount rule")
			}
		}
	}
	if runtime.GOOS == "windows" {
		for _, rule := range BuildWindowsAccessRules(root, isolation.ExposePaths) {
			if rule.Path == "" {
				return fmt.Errorf("invalid windows access rule")
			}
		}
	}
	return nil
}

func Start(cmd *exec.Cmd) error {
	if err := PrepareCommand(cmd); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	isolation := CurrentConfig()
	root := ""
	if isolation.Enabled {
		var err error
		root, err = ResolveInstanceRoot()
		if err != nil {
			_ = cmd.Process.Kill()
			return err
		}
	}
	if err := postStartPlatformIsolation(cmd, isolation, root); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return nil
}

func Run(cmd *exec.Cmd) error {
	if err := PrepareCommand(cmd); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	isolation := CurrentConfig()
	root := ""
	if isolation.Enabled {
		var err error
		root, err = ResolveInstanceRoot()
		if err != nil {
			_ = cmd.Process.Kill()
			return err
		}
	}
	if err := postStartPlatformIsolation(cmd, isolation, root); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	return cmd.Wait()
}

func PrepareCommand(cmd *exec.Cmd) error {
	isolation := CurrentConfig()
	if err := Preflight(); err != nil {
		return err
	}
	if isolation.Enabled {
		root, err := ResolveInstanceRoot()
		if err != nil {
			return err
		}
		ApplyUserEnv(cmd, root)
		if err := applyPlatformIsolation(cmd, isolation, root); err != nil {
			return err
		}
	}
	return nil
}
