# `pkg/namespace` 设计说明

本文档描述 `picoclaw` 的实例级隔离设计。目标是让整个 `picoclaw` 实例运行在独立且可维护的隔离环境中。

## 目标

- 以整个 `picoclaw` 实例作为隔离单位
- 统一主进程、agent、tools、CLI provider、MCP、hooks、launcher 的运行边界
- 在 Linux、macOS、Windows 上提供默认隔离能力
- 让实例迁移、清理、备份、排障更简单

## 总体结论

- 隔离根直接映射到现有 `config.GetHome()` 语义
- 提供一个实例级总开关用于开启或关闭隔离
- Linux 默认使用基于 `bwrap` 的 namespace 隔离
- macOS 标记为 `TODO`，当前版本暂不实现
- Windows 默认使用受限访问令牌、低完整性级别和 `Job Object` 隔离
- 不向用户暴露平台隔离后端选择配置

## 隔离单位

隔离单位是单个 `picoclaw` 实例。

一个实例只认一个实例根目录，实例内所有配置、状态、缓存、工作目录和子进程运行痕迹都从该目录派生。

## 与现有配置的关系

当前仓库已经有稳定的根目录语义：

- `config.GetHome()`
- `PICOCLAW_HOME`
- `PICOCLAW_CONFIG`
- 默认 `UserHomeDir() + "/.picoclaw"`
- `config.json`
- `launcher-config.json`
- `workspace`

因此本设计直接建立在 `config.GetHome()` 之上，不再引入新的全局根配置。

实例根解析规则：

- 如果设置了 `PICOCLAW_HOME`，使用该值
- 如果未设置 `PICOCLAW_HOME`，使用 `os.UserHomeDir()` 与 `pkg.DefaultPicoClawHome` 拼接后的目录
- 如果用户目录也无法解析，则回退到当前目录 `.`

风险说明：

- 当隔离开启时，不应直接接受 `config.GetHome()` 回退到 `.` 作为最终实例隔离根
- 如果实例根最终解析为当前目录 `.`，实现应直接报错，避免把非预期工作目录当作实例隔离根

配置文件兼容规则：

- 实例隔离根仍然遵循 `config.GetHome()` 语义
- 如果未显式设置 `PICOCLAW_CONFIG`，默认配置文件路径为 `<instance-root>/config.json`
- 如果显式设置了 `PICOCLAW_CONFIG`，则继续遵循该路径，不因隔离开启而强制改回实例根目录
- `.security.yml` 仍按当前项目规则相对于实际 `config.json` 路径解析

## 配置

实例级隔离通过一个总开关控制。

设计约定：

- `DefaultConfig` 中默认将 `isolation.enabled` 设为 `false`
- 用户可以通过配置显式设为 `true` 开启隔离

配置示例：

```json
{
  "isolation": {
    "enabled": true,
    "expose_paths": []
  }
}
```

规则：

- `isolation.enabled = true`：启用实例隔离
- `isolation.enabled = false`：关闭实例隔离
- `DefaultConfig` 默认应为关闭状态
- 开启后按平台自动选择隔离后端
- 不提供平台隔离后端选择配置
- `isolation.expose_paths` 用于显式把宿主机目录或文件暴露到隔离环境内

`expose_paths` 的建议格式：

```json
{
  "isolation": {
    "enabled": true,
    "expose_paths": [
      {
        "source": "/opt/toolchains/go",
        "target": "/opt/toolchains/go",
        "mode": "ro"
      },
      {
        "source": "/data/shared-assets",
        "target": "/opt/picoclaw-instance-a/workspace/assets",
        "mode": "rw"
      }
    ]
  }
}
```

说明：

- `target` 表示隔离环境内的目标绝对路径
- `target` 不等同于实例默认 `workspace`
- Windows 下 `target` 仅用于保持跨平台配置结构一致，不承诺真实文件系统重映射语义
- 在 Linux 上，`target` 进入 mount 视图；在 Windows 上，权限控制仍以 `source` 为准

字段说明：

- `source`：宿主机路径，可以是目录或单个文件
- `target`：映射到隔离环境内的目标路径
- `mode`：`ro` 或 `rw`

约束：

- 默认值为空，表示不额外暴露宿主路径
- 应优先使用 `ro`，只有确实需要写入时才允许 `rw`
- `expose_paths` 是实例级配置，不放到 agent 级别
- 路径必须显式列出，不支持通配模式
- `source` 和 `target` 都必须是绝对路径
- 如果未显式指定 `target`，则默认等于 `source`
- 同一个 `target` 只能保留一条最终规则，后加载配置覆盖先加载配置

覆盖规则：

- 内部默认暴露项先加载
- 外部配置文件中的 `isolation.expose_paths` 后加载
- 当 `target` 相同时，外部配置覆盖内部默认项
- 当 `target` 不同时，规则并存
- 默认映射规则全部允许被外部配置覆盖
- 覆盖既可以替换整条 `source -> target` 规则，也可以只调整同一 `target` 的 `mode`
- 覆盖后的最终规则仍必须满足平台启动所需的最小运行时依赖；否则初始化应直接失败

最小运行依赖校验示例：

- Linux 上如果最终映射结果缺少运行命令所需的 `/usr`、`/bin`、`/lib`、`/lib64` 中的必要路径，应直接失败
- Linux 上如果把关键系统路径错误地重定向到不存在位置，应直接失败
- Linux 上如果移除关键文件如 `/etc/resolv.conf` 或把其指向非法目标，应直接失败
- Windows 上如果最终访问规则未包含实例根的必要访问权限，应直接失败

这样可以让程序内置必要映射，同时允许部署者在外部配置中替换或重定向这些映射。

默认映射目录：

- `config.GetHome()` 解析出的实例根目录，默认通常为 `$HOME/.picoclaw`
- Linux 下还会默认映射运行所需的最小系统路径，例如 `/usr`、`/bin`、`/lib`、`/lib64`、`/etc/resolv.conf`
- Windows 下实例根目录默认授予必要访问权限；额外宿主目录只有在 `expose_paths` 中显式声明后才会放开
- `expose_paths` 只用于补充默认映射之外的宿主路径，或覆盖内部默认映射规则

默认 workspace 规则：

- 默认情况下，workspace 仍按当前 `DefaultConfig()` 规则从实例根派生，通常为 `<instance-root>/workspace`
- 如果用户在现有配置中显式设置了 workspace，则以用户配置为准
- 隔离设计不应强制把已显式配置的 workspace 重写回 `<instance-root>/workspace`

## 实例目录布局

整个实例目录布局统一为：

```text
<instance-root>/
  config.json
  .security.yml
  launcher-config.json
  workspace/
  skills/
  logs/
  cache/
  state/
  runtime-user-env/
```

说明：

- `workspace/` 表示默认工作目录布局；若用户显式配置 workspace，则以实际配置为准
- `skills/`、`logs/`、`cache/`、`state/` 统一放在实例根下
- `runtime-user-env/` 是实例子进程使用的私有运行环境目录
- 平台隔离后端必须围绕这套实例目录布局工作，不能绕过实例根直接落回宿主机用户目录
- `config.json` 表示默认配置文件位置；若显式设置 `PICOCLAW_CONFIG`，则以实际配置路径为准

## 配置示例

```json
{
  "isolation": {
    "enabled": true,
    "expose_paths": [
      {
        "source": "/opt/toolchains/go",
        "target": "/opt/toolchains/go",
        "mode": "ro"
      }
    ]
  },
  "agents": {
    "defaults": {
      "workspace": "~/.picoclaw/workspace",
      "restrict_to_workspace": true
    }
  }
}
```

配合环境变量：

```bash
export PICOCLAW_HOME=/opt/picoclaw-instance-a
```

此时推荐运行语义为：

- `config.json` 位于 `/opt/picoclaw-instance-a/config.json`
- 默认 workspace 位于 `/opt/picoclaw-instance-a/workspace`
- 所有实例状态都位于 `/opt/picoclaw-instance-a/` 下

如果未设置 `PICOCLAW_HOME`，则实例根默认按 `config.GetHome()` 解析，通常为用户目录下的 `.picoclaw`。

## 平台策略

### Linux

Linux 默认使用基于 `bwrap` 的 namespace 隔离。

默认要求：

- 使用 `mount namespace`
- 使用 `ipc namespace`
- 使用 `bwrap` 构造独立文件系统视图
- 只暴露实例运行所需的最小文件系统视图
- 把 `config.GetHome()` 解析出的实例根直接映射到隔离环境中
- 把 `isolation.expose_paths` 中声明的 `source -> target` 路径按只读或读写方式显式映射到隔离环境中

实现说明：

- 当前 Linux 后端依赖系统已安装 `bwrap` (`bubblewrap`)
- 如果 `bwrap` 不可用，Linux 隔离初始化应直接报错，不允许静默回退到未隔离启动
- 可参考的安装命令：`apt install bubblewrap`、`dnf install bubblewrap`、`yum install bubblewrap`、`pacman -S bubblewrap`、`apk add bubblewrap`
- 如果临时无法安装 `bwrap`，也可以在配置中将 `isolation.enabled` 显式设为 `false` 关闭隔离
- 关闭隔离后，子进程将不再使用 Linux namespace 文件系统隔离，访问和修改宿主机文件的风险会明显上升

### macOS

macOS 当前标记为 `TODO`，暂不实现。

对外说明：

- 当前版本不承诺 macOS 隔离能力
- 当前版本不默认启用 macOS sandbox
- 当前版本不提供 macOS 平台上的强隔离保证

后续实现目标：

- `~/.ssh`
- `~/.gitconfig`
- `~/Documents`
- `~/Desktop`
- `~/Downloads`

在后续版本实现 macOS sandbox 后，再定义具体启动链路和失败策略。

### Windows

Windows 默认使用受限访问令牌、低完整性级别和 `Job Object` 隔离。

默认要求：

- 使用受限访问令牌启动实例创建的子进程
- 将子进程令牌降到低完整性级别
- 使用 `Job Object` 约束进程树生命周期
- 对实例根目录授予必要读写权限
- 对实例根目录之外的宿主目录默认不授予额外写权限
- 对敏感宿主目录通过低权限令牌和低完整性级别进一步压缩写入能力
- 对 `isolation.expose_paths` 中声明的 `source -> target` 路径按显式白名单放开访问权限

说明：

- Windows 不对外实现真正的 `source -> target` 文件系统重映射
- Windows 上的核心能力是“按 `source` 控制访问权限”，而不是像 Linux mount namespace 那样把任意宿主路径稳定地挂载到另一个隔离目标路径
- 因此在 Windows 上，`target` 主要用于保持跨平台配置结构一致，以及供隔离环境内路径解析使用；真正的权限控制仍然基于 `source`
- 如果未来 Windows 侧具备稳定、低复杂度的目标路径重映射实现，再单独扩展该能力

Windows 上如果受限令牌、低完整性级别或 `Job Object` 初始化失败，应直接报错，不允许回退到普通用户权限启动。

## 生效范围

当 `isolation.enabled=true` 时，实例隔离覆盖整个实例的所有运行路径：

- 主进程默认目录解析
- agent workspace 解析
- `exec` 工具
- CLI provider，例如 `claude-cli`、`codex-cli`
- 进程型 hooks
- 启动型 MCP server
- launcher 相关运行目录
- skills、state、sessions、logs、cache 的默认目录
- 其他后续新增的 `exec.Command` / `exec.CommandContext` 路径

其中：

- Linux 上这些路径都必须进入 `namespace` 启动链路
- macOS 当前暂无隔离启动链路，后续补齐
- Windows 上这些路径都必须进入受限令牌 + 低完整性级别 + `Job Object` 启动链路

初始化顺序：

1. 先按当前项目规则解析 `config.GetHome()` 和 `PICOCLAW_CONFIG`
2. 再读取配置文件并得到最终配置
3. 根据最终配置判断 `isolation.enabled`
4. 只有在启用隔离后，才初始化平台隔离后端
5. 后续由 `picoclaw` 启动的子进程统一进入对应平台的隔离启动链路

这样可以避免“先隔离才能读配置”与“先读配置才能决定是否隔离”的循环依赖。

## 与 `restrict_to_workspace` 的关系

- 实例隔离负责“整个 picoclaw 把哪里当作自己的运行世界”
- `restrict_to_workspace` 负责“agent 在这个世界里默认能访问哪些路径”
- `isolation.expose_paths` 负责“哪些宿主路径以什么目标路径被显式带入隔离环境”

两者互补，不互相替代。

## 失败策略

当 `isolation.enabled=true` 时，以下情况都应直接报错：

- 实例根目录准备失败
- 默认目录收敛失败
- 平台隔离后端初始化失败
- 子进程隔离启动失败

不允许静默回退到宿主机默认用户目录，也不允许回退到未隔离启动。

当 `isolation.enabled=false` 时：

- 不启用 Linux `namespace`
- 不启用 Windows 受限访问令牌、低完整性级别和 `Job Object` 隔离
- 不处理 `isolation.expose_paths`
- 进程按当前普通运行方式启动

## 安全边界

当 `isolation.enabled=true` 时，本设计提供的是 `picoclaw` 的隔离运行模型：

- Linux 使用基于 `bwrap` 的 namespace 隔离
- macOS 当前为 `TODO`
- Windows 使用受限访问令牌、低完整性级别和 `Job Object` 隔离

它能解决的问题：

- 避免 `picoclaw` 的配置、日志、状态、skills、缓存散落在宿主机默认用户目录
- 避免多个 `picoclaw` 实例共享同一套运行状态
- 为实例内子进程提供统一的默认隔离启动链路
- 让实例迁移、备份、删除、排障更简单

关于“误删重要资料、读取重要资料”的结论：

- Linux 和 Windows 上，隔离的目标是显著降低误删宿主重要资料、误读宿主敏感文件的风险
- 当前设计不承诺绝对杜绝所有敏感文件访问或宿主文件破坏
- macOS 当前未实现隔离，不应依赖其提供这类保护
- 一旦配置了 `isolation.expose_paths`，这些路径就会成为隔离环境内可见路径，因此需要由用户自行控制暴露范围

它不承诺：

- 三个平台提供完全相同的底层隔离原语
- 容器或 VM 级完全等价边界
- 绝对杜绝所有私密文件访问
- 绝对杜绝所有宿主文件破坏

以下情况仍然可能带来风险：

- Linux 上 namespace/root 视图暴露范围过大，把宿主敏感路径一并映射进隔离环境
- 用户在 `isolation.expose_paths` 中加入了不必要的敏感目录或使用了过宽的 `rw` 暴露
- Linux 上为了兼容工具链而额外放宽挂载、网络或系统路径访问范围
- Windows 上受限令牌或低完整性级别限制不足，仍保留了过宽的宿主访问能力
- Windows 上为了兼容某些 CLI、编译器或运行时而额外放宽文件访问权限
- 子进程本身需要访问的“必要系统路径”定义过宽，间接扩大了可读取或可写入范围
- `restrict_to_workspace=false` 或额外白名单路径配置过宽，使 agent 能接触更多宿主路径
- 被执行的命令、脚本、构建工具或第三方 CLI 本身具有高风险行为，即使在隔离下仍可能破坏已暴露路径内的数据
- 平台隔离后端实现存在漏洞、遗漏调用路径，或某些子进程没有走统一隔离启动链路
- macOS 当前未实现隔离，运行时仍可能直接接触宿主机用户目录

真正高风险的不可信代码执行场景，仍应放到容器或 VM 中处理。

## 实现伪代码

下面的伪代码只展示整体实现思路，便于评审，不代表最终代码结构必须完全一致。

### 1. 解析实例根和隔离配置

```go
type IsolationConfig struct {
	Enabled     bool         `json:"enabled,omitempty"`
	ExposePaths []ExposePath `json:"expose_paths,omitempty"`
}

type ExposePath struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
	Mode   string `json:"mode"` // ro | rw
}

func resolveInstanceRoot() string {
	// 直接复用项目现有的实例根解析逻辑。
	return config.GetHome()
}
```

### 2. 启动时准备实例目录

```go
func prepareInstanceRoot(root string) error {
	dirs := []string{
		root,
		filepath.Join(root, "workspace"),
		filepath.Join(root, "skills"),
		filepath.Join(root, "logs"),
		filepath.Join(root, "cache"),
		filepath.Join(root, "state"),
		filepath.Join(root, "runtime-user-env"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("prepare instance dir %s: %w", dir, err)
		}
	}
	return nil
}
```

### 3. 校验 `expose_paths`

```go
func validateExposePaths(items []ExposePath) error {
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.Source == "" {
			return fmt.Errorf("source is required")
		}
		if item.Mode != "ro" && item.Mode != "rw" {
			return fmt.Errorf("invalid expose_paths mode: %s", item.Mode)
		}

		source := filepath.Clean(expandHome(item.Source))
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
```

### 4. Linux: 构建 `bwrap` 文件系统视图

```go
type LinuxMount struct {
	Source string
	Target string
	Mode   string // ro | rw
}

func buildLinuxMountPlan(root string, expose []ExposePath) []LinuxMount {
	plan := []LinuxMount{
		// 直接把 config.GetHome() 解析出的实例根映射为隔离根。
		{Source: root, Target: root, Mode: "rw"},
		{Source: "/usr", Target: "/usr", Mode: "ro"},
		{Source: "/bin", Target: "/bin", Mode: "ro"},
		{Source: "/lib", Target: "/lib", Mode: "ro"},
		{Source: "/lib64", Target: "/lib64", Mode: "ro"},
		{Source: "/etc/resolv.conf", Target: "/etc/resolv.conf", Mode: "ro"},
	}

	for _, item := range expose {
		source := filepath.Clean(expandHome(item.Source))
		target := item.Target
		if target == "" {
			target = source
		}
		target = filepath.Clean(target)
		plan = append(plan, LinuxMount{Source: source, Target: target, Mode: item.Mode})
	}
	return plan
}

func startLinuxIsolated(cmd *exec.Cmd, root string, expose []ExposePath) error {
	plan := buildLinuxMountPlan(root, expose)

	// 伪代码：
	// 1. 生成 bwrap 参数
	// 2. 打开 ipc namespace
	// 3. 把 config.GetHome() 解析出的实例根 bind mount 进隔离视图
	// 4. 再按 expose_paths 和必要系统路径补充 bind mount
	// 5. ro 条目用 ro-bind
	// 6. 切换 cwd 到 filepath.Join(root, "workspace")
	// 7. 通过 bwrap exec 启动目标进程

	_ = plan
	return runInLinuxNamespaces(cmd)
}
```

### 5. Windows: 用受限令牌、低完整性级别和 `Job Object` 启动

```go
type WindowsAccessRule struct {
	Path string
	Mode string // ro | rw | deny
}

func buildWindowsAccessRules(root string, expose []ExposePath) []WindowsAccessRule {
	rules := []WindowsAccessRule{
		{Path: root, Mode: "rw"},
		{Path: filepath.Join(os.Getenv("USERPROFILE"), ".ssh"), Mode: "deny"},
		{Path: filepath.Join(os.Getenv("USERPROFILE"), "Documents"), Mode: "deny"},
		{Path: filepath.Join(os.Getenv("USERPROFILE"), "Desktop"), Mode: "deny"},
	}

	for _, item := range expose {
		source := filepath.Clean(expandHome(item.Source))
		_ = item.Target // Windows 权限以 source 为准，target 用于隔离环境内路径解析。
		rules = append(rules, WindowsAccessRule{Path: source, Mode: item.Mode})
	}
	return rules
}

func startWindowsIsolated(cmd *exec.Cmd, root string, expose []ExposePath) error {
	rules := buildWindowsAccessRules(root, expose)

	// 伪代码：
	// 1. 创建受限访问令牌
	// 2. 将令牌降到低完整性级别
	// 3. 创建 Job Object
	// 4. 以受限令牌启动子进程并加入 Job Object

	_ = rules
	return runWithRestrictedTokenAndJobObject(cmd)
}
```

### 6. macOS: 预留统一入口

```go
func startDarwinIsolated(cmd *exec.Cmd, root string, expose []ExposePath) error {
	return fmt.Errorf("macOS isolation is TODO")
}
```

### 7. 统一子进程启动入口

```go
func Configure(cfg IsolationConfig)

func startIsolatedProcess(cmd *exec.Cmd, root string) error {
	cfg := CurrentConfig()
	if err := prepareInstanceRoot(root); err != nil {
		return err
	}
	if err := validateExposePaths(cfg.ExposePaths); err != nil {
		return err
	}

	switch runtime.GOOS {
	case "linux":
		return startLinuxIsolated(cmd, root, cfg.ExposePaths)
	case "windows":
		return startWindowsIsolated(cmd, root, cfg.ExposePaths)
	case "darwin":
		return startDarwinIsolated(cmd, root, cfg.ExposePaths)
	default:
		return fmt.Errorf("isolation unsupported on %s", runtime.GOOS)
	}
}
```

### 8. 统一进程启动封装

```go
func newExecTool(...) *ExecTool {
	// exec 工具内部所有命令最终走 startIsolatedProcess
}

func newCLIProvider(...) Provider {
	// claude-cli / codex-cli 等在启动命令时走 startIsolatedProcess
}

func startMCPServer(...) error {
	// MCP server 启动时走 startIsolatedProcess
}

func runProcessHook(...) error {
	// 进程型 hooks 走 startIsolatedProcess
}
```

为什么需要这一步：

- 当前代码里存在多处独立的 `exec.Command` / `exec.CommandContext` 调用，进程创建没有统一收口
- `exec` 工具、CLI provider、MCP stdio server、process hook 都会各自启动子进程
- 如果不统一收口，后续很容易出现某些路径没有进入隔离启动链路的问题
- Linux 上即使主进程已经先进入 `namespace`，统一封装仍然有价值，因为它可以确保所有 spawn 路径都使用同一套约束和默认行为
- Windows 上这一步是必需的，因为受限访问令牌、低完整性级别和 `Job Object` 需要在创建子进程时施加，不能只依赖主进程“已经被隔离”
- 统一封装的目标不是让每个模块各自实现隔离逻辑，而是让所有子进程创建都复用同一个入口

### 9. 关键实现原则

- 隔离总开关只在实例级生效，不下沉到 agent 级别
- `expose_paths` 只做显式白名单，不做通配符和隐式继承
- Linux 以最小挂载视图为核心，禁止“先暴露整个宿主根目录再黑名单过滤”
- Windows 以“实例根允许、敏感目录拒绝、显式路径白名单放开”为核心
- 任何需要启动子进程的路径都必须复用同一个隔离启动入口，不能各自实现

## 最终方案

- 用 `config.GetHome()` 解析出的实例根作为整个 `picoclaw` 的隔离根
- 通过 `isolation.enabled` 作为实例级总开关
- 通过 `isolation.expose_paths` 显式声明需要带入隔离环境的宿主路径
- Linux 默认使用基于 `bwrap` 的 namespace 隔离
- macOS 标记为 `TODO`，当前版本暂不实现
- Windows 默认使用受限访问令牌、低完整性级别和 `Job Object` 隔离
- 所有默认目录都优先从实例根统一派生；若存在现有显式 override，则继续遵循该 override
- 不新增平台隔离后端选择配置项

这套方案适合作为对外公布版本：边界清晰、配置最少、平台行为明确，也更利于整个项目长期维护。
