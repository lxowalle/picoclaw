# PicoClaw 音频架构 v5 - 组件注册机制详解

本文档详细解释 v5 架构中各组件的注册机制，确保：
1. **高度解耦**：各层通过接口交互，不直接依赖具体实现
2. **单一修改原则**：添加新 Provider 无需修改 Channel 代码
3. **编译时确定**：通过 build tags 控制哪些组件被编译进二进制

---

## 架构分层与注册关系

```
┌─────────────────────────────────────────────────────────────┐
│  Layer 4: Channel (只修改一次)                                │
│  - 通过接口调用 Service                                       │
│  - 不感知 Provider 的存在                                     │
└───────────────────────┬─────────────────────────────────────┘
                        │ Service 接口
                        ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 3: Service (包级注册)                                  │
│  - ASR Service: 包装 ASRProvider                              │
│  - TTS Service: 包装 TTSProvider                              │
│  - Coordinator: 按需创建，包装 Service                        │
│  - Codec Manager: 管理 Encoder/Decoder                        │
└───────────────────────┬─────────────────────────────────────┘
                        │ Provider / Codec 接口
                        ▼
┌─────────────────────────────────────────────────────────────┐
│  Layer 2: Codec (条件编译注册)                                 │
│  - Encoder: PCM → Target Format                              │
│  - Decoder: Source Format → PCM                              │
│  - Format Detector: 自动检测格式                              │
├─────────────────────────────────────────────────────────────┤
│  Layer 1: Provider (条件编译注册)                              │
│  - ASR Provider: Doubao, Azure, OpenAI...                    │
│  - TTS Provider: Doubao, Azure, OpenAI...                    │
└─────────────────────────────────────────────────────────────┘
```

---

## 1. Provider 注册到 Service

### 1.1 问题：如何解耦 Provider 和 Service？

**传统做法（紧耦合）**：
```go
// BAD: Service 直接 import 所有 Provider
import "pkg/asr/doubao"
import "pkg/asr/azure"

func NewService(config Config) (*Service, error) {
    switch config.Provider {
    case "doubao":
        return doubao.New(config), nil
    case "azure":
        return azure.New(config), nil
    }
}
```

**问题**：
- 添加新 Provider 必须修改 Service 代码
- 无法按需编译（所有 Provider 都被链接）

### 1.2 解决方案：全局注册表 + 条件编译

**Step 1: Provider 接口定义** (`pkg/asr/asr.go`)
```go
package asr

// Provider 接口 - 所有 ASR 实现必须实现此接口
type Provider interface {
    Transcribe(ctx context.Context, audio []byte, format Format) (string, error)
    TranscribeStream(ctx context.Context, audioStream <-chan []byte, format Format) (<-chan TranscriptionResult, error)
    Name() string
    Capabilities() ASRCapabilities
}

// 全局注册表
var providers = make(map[string]Provider)

// Register 注册 Provider
func Register(name string, provider Provider) {
    providers[name] = provider
}

// Get 获取 Provider
func Get(name string) (Provider, bool) {
    p, ok := providers[name]
    return p, ok
}
```

**Step 2: Provider 实现** (`pkg/asr/doubao/doubao.go`)
```go
package doubao

import "github.com/sipeed/picoclaw/pkg/asr"

type Provider struct {
    // 实现细节...
}

func (p *Provider) Transcribe(...) (string, error) { ... }
func (p *Provider) Name() string { return "doubao" }
func (p *Provider) Capabilities() asr.ASRCapabilities { ... }

// 提供创建函数，但不直接注册
func New(config Config) *Provider {
    return &Provider{...}
}

// 提供从全局配置创建的方式
func NewFromConfig(cfg *config.Config) *Provider {
    // 从 cfg.Audio.Providers["doubao"].Internal 解析配置
}
```

**Step 3: 条件编译注册文件** (`pkg/asr/registry_doubao.go`)
```go
//go:build asr_doubao || asr_all

package asr

import "github.com/sipeed/picoclaw/pkg/asr/doubao"

func init() {
    // 在 init() 中自动注册到全局注册表
    Register("doubao", doubao.New(doubao.DefaultConfig()))
}
```

**关键点**：
- `//go:build asr_doubao || asr_all` 表示只有启用此 build tag 时才编译此文件
- `init()` 函数在包加载时自动执行注册
- 不修改 Service 代码，Provider 自注册

### 1.3 Service 使用注册表

**Step 4: Service 实现** (`pkg/asr/service/service.go`)
```go
package service

import "github.com/sipeed/picoclaw/pkg/asr"

type Service struct {
    provider asr.Provider  // 依赖接口，不依赖具体实现
}

// NewService 通过 Provider 名称创建 Service
func NewService(providerName string, cfg *config.Config) (*Service, error) {
    // 1. 尝试从注册表获取
    provider, ok := asr.Get(providerName)
    if !ok {
        // 2. 如果注册表中没有，尝试从 config 动态创建
        provider = createProviderFromConfig(providerName, cfg)
        if provider == nil {
            return nil, fmt.Errorf("provider %s not found", providerName)
        }
    }
    
    return &Service{provider: provider}, nil
}

func createProviderFromConfig(name string, cfg *config.Config) asr.Provider {
    // 使用 switch 创建，但只在 Service 包内，不影响其他层
    switch name {
    case "doubao":
        return doubao.NewFromConfig(cfg)
    // 添加新 Provider 只需在这里加一行 case
    default:
        return nil
    }
}
```

**优势**：
1. **解耦**：Service 只依赖接口，不依赖具体 Provider
2. **灵活**：支持注册表和动态创建两种方式
3. **单一职责**：Service 只负责包装，不负责创建逻辑

---

## 2. Coordinator 注册到 Service

### 2.1 Coordinator 的定位

Coordinator 是可选的流式处理组件，只在需要时创建：

```
Channel (流式模式)
    ↓ 创建
Coordinator
    ├── ASR Service (流式识别)
    ├── LLM Client (流式生成)
    └── TTS Service (流式合成)
```

### 2.2 Coordinator 不直接注册到 Service

**错误设计**：
```go
// BAD: Coordinator 不应该注册到 Service
func init() {
    asr.RegisterCoordinator("default", coordinator.New(...))
}
```

**正确设计**：
```go
// Coordinator 是按需创建的独立组件
type Coordinator struct {
    asrService *asr.Service
    ttsService *tts.Service
    llmClient  llm.Client
}

// NewCoordinator 创建协调器
func NewCoordinator(asr *asr.Service, tts *tts.Service, llm llm.Client) *Coordinator {
    return &Coordinator{
        asrService: asr,
        ttsService: tts,
        llmClient:  llm,
    }
}
```

### 2.3 Coordinator 在 Channel 层创建

```go
// Channel 初始化时按需创建 Coordinator
func (c *Channel) initAudioServices(cfg *config.Config) error {
    // 1. 创建 ASR Service
    if cfg.Audio.ASRProvider != "" {
        asrService, _ = asr.NewService(cfg.Audio.ASRProvider, cfg)
        c.asrService = asrService
    }
    
    // 2. 创建 TTS Service
    if cfg.Audio.TTSProvider != "" {
        ttsService, _ = tts.NewService(cfg.Audio.TTSProvider, cfg)
        c.ttsService = ttsService
    }
    
    // 3. 如果是流式模式，创建 Coordinator
    if cfg.Audio.Streaming {
        c.coordinator = NewCoordinator(asrService, ttsService, c.llmClient)
    }
    
    return nil
}
```

**关键点**：
- Coordinator 不注册到任何全局注册表
- 它在 Channel 层被创建，持有 Service 引用
- 添加新 Provider 不影响 Coordinator

---

## 3. Encoder/Decoder 注册到 Codec Manager

### 3.1 Codec 接口

```go
package codec

// Codec 编解码器接口
type Codec interface {
    Decode(ctx context.Context, data []byte) ([]byte, error)
    Encode(ctx context.Context, pcm []byte) ([]byte, error)
    CanDecode(format Format) bool
    CanEncode(format Format) bool
    Name() string
}

// 全局 Codec 注册表
var codecs = make(map[string]Codec)

func Register(name string, codec Codec) {
    codecs[name] = codec
}

func Get(name string) (Codec, bool) {
    c, ok := codecs[name]
    return c, ok
}

func GetForFormat(format Format) Codec {
    for _, codec := range codecs {
        if codec.CanDecode(format) || codec.CanEncode(format) {
            return codec
        }
    }
    return nil
}
```

### 3.2 Codec 实现与注册

**PCM Codec** (`pkg/audio/codec/pcm/pcm.go`):
```go
package pcm

type Codec struct{}

func (c *Codec) Decode(data []byte) ([]byte, error) { return data, nil }
func (c *Codec) Encode(pcm []byte) ([]byte, error) { return pcm, nil }
func (c *Codec) CanDecode(format Format) bool { return format == FormatPCM }
func (c *Codec) CanEncode(format Format) bool { return format == FormatPCM }
```

**注册文件** (`pkg/audio/codec/registry_pcm.go`):
```go
//go:build codec_pcm || codec_all

package codec

import "github.com/sipeed/picoclaw/pkg/audio/codec/pcm"

func init() {
    Register("pcm", &pcm.Codec{})
}
```

**Opus Codec** (`pkg/audio/codec/opus/opus.go`):
```go
//go:build codec_opus || codec_all

package opus

import "github.com/sipeed/picoclaw/pkg/audio/codec"

type Codec struct{}

func (c *Codec) Decode(data []byte) ([]byte, error) {
    // Opus 解码实现
}

func (c *Codec) Encode(pcm []byte) ([]byte, error) {
    // Opus 编码实现
}

func (c *Codec) CanDecode(format Format) bool { return format == FormatOpus }
func (c *Codec) CanEncode(format Format) bool { return false } // 暂不支持编码
```

**注册文件** (`pkg/audio/codec/registry_opus.go`):
```go
//go:build codec_opus || codec_all

package codec

import "github.com/sipeed/picoclaw/pkg/audio/codec/opus"

func init() {
    Register("opus", &opus.Codec{})
}
```

### 3.3 Service 层使用 Codec

```go
package service

type Service struct {
    provider asr.Provider
    codecMgr *codec.Manager  // 通过 Manager 使用 Codec，不直接依赖
}

func NewService(provider asr.Provider) *Service {
    return &Service{
        provider: provider,
        codecMgr: codec.DefaultManager(),  // 获取全局 Manager
    }
}

func (s *Service) Recognize(ctx context.Context, audio []byte, format Format) (string, error) {
    // 检查 Provider 是否支持该格式
    if !s.providerSupportsFormat(format) {
        // 需要解码为 PCM
        codec := s.codecMgr.GetForFormat(format)
        if codec == nil {
            return "", fmt.Errorf("no codec available for format %s", format)
        }
        pcm, err := codec.Decode(ctx, audio)
        if err != nil {
            return "", err
        }
        audio = pcm
        format = FormatPCM
    }
    
    return s.provider.Transcribe(ctx, audio, format)
}
```

---

## 4. Service 注册到 Channel（关键：只修改一次）

### 4.1 问题：如何避免每次添加 Provider 都修改 Channel？

**错误做法**：
```go
// BAD: 每次添加 Provider 都要修改这里
func (c *Channel) initAudio() {
    switch cfg.Provider {
    case "doubao":
        c.asr = doubao.New(cfg)
    case "azure":
        c.asr = azure.New(cfg)  // 添加 Azure 必须修改 Channel
    case "openai":
        c.asr = openai.New(cfg) // 添加 OpenAI 必须修改 Channel
    }
}
```

### 4.2 解决方案：配置驱动 + 工厂模式

**核心思想**：Channel 只认识配置，不认识具体 Provider

```go
// Channel 只配置使用哪个 Provider，不创建具体实例
type PicoAudioConfig struct {
    Enabled      bool   `json:"enabled,omitempty"`
    Streaming    bool   `json:"streaming,omitempty"`
    ASRProvider  string `json:"asr_provider,omitempty"`  // 例如: "doubao"
    TTSProvider  string `json:"tts_provider,omitempty"`  // 例如: "doubao"
}
```

**Channel 初始化**（只修改一次）：
```go
// pkg/channels/pico/pico.go

// 只修改一次：添加音频服务初始化逻辑
func (c *PicoChannel) initAudioServices(cfg *config.Config) error {
    // 1. 初始化 ASR Service
    if cfg.Channels.Pico.Audio.ASRProvider != "" {
        provider, err := c.createASRProvider(cfg)
        if err != nil {
            return err
        }
        c.asrService = asrService.NewService(provider)
    }
    
    // 2. 初始化 TTS Service
    if cfg.Channels.Pico.Audio.TTSProvider != "" {
        provider, err := c.createTTSProvider(cfg)
        if err != nil {
            return err
        }
        c.ttsService = ttsService.NewService(provider)
    }
    
    // 3. 流式模式下创建 Coordinator
    if cfg.Channels.Pico.Audio.Streaming {
        c.coordinator = NewCoordinator(c.asrService, c.ttsService, c.llmClient)
    }
    
    return nil
}

// createASRProvider 创建 ASR Provider（添加新 Provider 只需修改这里）
func (c *PicoChannel) createASRProvider(cfg *config.Config) (asr.Provider, error) {
    providerName := cfg.Channels.Pico.Audio.ASRProvider
    
    // 方法1：尝试从全局注册表获取（通过 build tags 编译进来的 Provider）
    if provider, ok := asr.Get(providerName); ok {
        return provider, nil
    }
    
    // 方法2：从配置动态创建（无需注册表）
    switch providerName {
    case "doubao":
        return doubao.NewFromConfig(cfg), nil
    // 添加新 Provider 只需在这里添加 case，无需修改其他代码
    default:
        return nil, fmt.Errorf("unknown ASR provider: %s", providerName)
    }
}

// createTTSProvider 创建 TTS Provider
func (c *PicoChannel) createTTSProvider(cfg *config.Config) (tts.Provider, error) {
    providerName := cfg.Channels.Pico.Audio.TTSProvider
    
    if provider, ok := tts.Get(providerName); ok {
        return provider, nil
    }
    
    switch providerName {
    case "doubao":
        return ttsdoubao.NewFromConfig(cfg), nil
    default:
        return nil, fmt.Errorf("unknown TTS provider: %s", providerName)
    }
}
```

### 4.3 添加新 Provider 的步骤（无需修改 Channel）

**步骤1**：创建 Provider 实现
```go
// pkg/asr/azure/azure.go
package azure

type Provider struct{}
func New(config Config) *Provider { return &Provider{} }
func (p *Provider) Transcribe(...) (string, error) { ... }
```

**步骤2**：创建条件编译注册文件（可选）
```go
// pkg/asr/registry_azure.go
//go:build asr_azure || asr_all
package asr
import "github.com/sipeed/picoclaw/pkg/asr/azure"
func init() { Register("azure", azure.New(azure.DefaultConfig())) }
```

**步骤3**：在 Channel 的 switch case 中添加（只需修改一处）
```go
// pkg/channels/pico/pico.go
func (c *PicoChannel) createASRProvider(cfg *config.Config) (asr.Provider, error) {
    // ...
    switch providerName {
    case "doubao":
        return doubao.NewFromConfig(cfg), nil
    case "azure":  // 添加这一行
        return azure.NewFromConfig(cfg), nil  // 添加这一行
    }
}
```

**注意**：
- 如果不使用注册表，则必须修改 `createASRProvider` 中的 switch
- 如果使用注册表（方法1），则**完全不需要修改 Channel**，只需创建 Provider 和注册文件

---

## 5. 完整注册流程图

### 5.1 编译期注册流程

```
编译期（通过 build tags）
│
├─ codec_pcm build tag
│   └─ pkg/audio/codec/registry_pcm.go 编译
│       └─ init() → codec.Register("pcm", ...)
│
├─ codec_opus build tag
│   └─ pkg/audio/codec/registry_opus.go 编译
│       └─ init() → codec.Register("opus", ...)
│
├─ asr_doubao build tag
│   └─ pkg/asr/registry_doubao.go 编译
│       └─ init() → asr.Register("doubao", ...)
│
└─ tts_doubao build tag
    └─ pkg/tts/registry_doubao.go 编译
        └─ init() → tts.Register("doubao", ...)
```

### 5.2 运行时初始化流程

```
运行时
│
├─ 1. 导入包，执行 init()
│   ├─ codec 注册表填充
│   ├─ asr 注册表填充
│   └─ tts 注册表填充
│
├─ 2. 创建 Channel
│   └─ NewPicoChannel(cfg, bus)
│       ├─ 创建 BaseChannel
│       └─ 调用 initAudioServices(cfg)
│           │
│           ├─ 3. 创建 ASR Service
│           │   ├─ createASRProvider(cfg)
│           │   │   ├─ 尝试从注册表获取
│           │   │   └─ 或从配置动态创建
│           │   └─ asrService.NewService(provider)
│           │
│           ├─ 4. 创建 TTS Service
│           │   └─ (同上)
│           │
│           └─ 5. 流式模式创建 Coordinator
│               └─ NewCoordinator(asrSvc, ttsSvc, llm)
│
└─ 6. Channel 开始运行
    ├─ 收到 audio.chunk → 调用 asrService.Recognize()
    ├─ 需要回复 → 调用 ttsService.Synthesize()
    └─ 流式模式 → 调用 coordinator.Process()
```

---

## 6. 解耦验证清单

| 检查项 | 是否解耦 | 说明 |
|--------|----------|------|
| Channel ↔ Provider | ✅ | Channel 通过 Service 接口间接使用 Provider |
| Channel ↔ Codec | ✅ | Channel 不直接使用 Codec，由 Service 处理 |
| Service ↔ Provider | ✅ | Service 依赖 Provider 接口，不依赖具体实现 |
| Service ↔ Codec | ✅ | Service 通过 Codec Manager 使用，不直接依赖 |
| Provider ↔ Config | ✅ | Provider 自己解析配置，Config 层不知道 Provider 字段 |
| 添加新 Provider | ✅ | 无需修改 Channel（使用注册表时）或只修改一处 switch |
| 编译裁剪 | ✅ | 通过 build tags 控制编译哪些 Provider |

---

## 7. 最佳实践总结

### 7.1 添加新 Provider 的标准流程

1. **创建 Provider 实现**（独立包）
   ```go
   // pkg/asr/newprovider/newprovider.go
   package newprovider
   type Provider struct{}
   func New(config Config) *Provider { ... }
   ```

2. **创建注册文件**（可选，用于编译裁剪）
   ```go
   // pkg/asr/registry_newprovider.go
   //go:build asr_newprovider || asr_all
   package asr
   func init() { Register("newprovider", newprovider.New(...)) }
   ```

3. **更新 Channel 工厂**（如果不使用注册表）
   ```go
   // pkg/channels/pico/pico.go
   case "newprovider":
       return newprovider.NewFromConfig(cfg), nil
   ```

4. **配置文件中启用**
   ```json
   {"audio": {"providers": {"newprovider": {"enabled": true}}}}
   ```

### 7.2 设计原则

1. **面向接口编程**：所有层都依赖接口，不依赖具体实现
2. **注册表模式**：全局注册表 + init() 自动注册
3. **条件编译**：通过 build tags 实现编译裁剪
4. **配置驱动**：Channel 行为由配置决定，不硬编码
5. **单一修改**：添加功能只在特定文件修改一次

---

## 8. 示例：完整的添加 Provider 流程

假设要添加 **Azure ASR Provider**：

**文件1**: `pkg/asr/azure/azure.go`（新文件）
```go
package azure
import "github.com/sipeed/picoclaw/pkg/asr"

type Provider struct{ appID, token string }

func New(config Config) *Provider {
    return &Provider{appID: config.AppID, token: config.Token}
}

func NewFromConfig(cfg *config.Config) *Provider {
    internal := cfg.Audio.Providers["azure"].Internal
    return New(Config{
        AppID: internal["appid"].(string),
        Token: internal["token"].(string),
    })
}

func (p *Provider) Transcribe(ctx context.Context, audio []byte, format asr.Format) (string, error) {
    // Azure ASR 实现
    return "", nil
}

func (p *Provider) Name() string { return "azure" }
```

**文件2**: `pkg/asr/registry_azure.go`（新文件）
```go
//go:build asr_azure || asr_all
package asr
import "github.com/sipeed/picoclaw/pkg/asr/azure"
func init() { Register("azure", azure.New(azure.DefaultConfig())) }
```

**修改1**: `pkg/channels/pico/pico.go`（修改一处）
```go
func (c *PicoChannel) createASRProvider(cfg *config.Config) (asr.Provider, error) {
    switch providerName {
    case "doubao":
        return doubao.NewFromConfig(cfg), nil
    case "azure":  // 添加这两行
        return azure.NewFromConfig(cfg), nil
    default:
        return nil, fmt.Errorf("unknown provider: %s", providerName)
    }
}
```

**配置**:
```json
{
  "audio": {
    "asr": {"provider": "azure"},
    "providers": {
      "azure": {
        "enabled": true,
        "internal": {"appid": "xxx", "token": "yyy"}
      }
    }
  }
}
```

**构建**:
```bash
go build -tags "asr_azure" ./cmd/picoclaw
```

**验证**：✅ 只有 1 个文件被修改（`pico.go`），其他都是新增
