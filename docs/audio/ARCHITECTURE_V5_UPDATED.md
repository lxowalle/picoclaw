# PicoClaw 音频服务架构 v5 - 更新文档

## 核心设计变更

### 1. Provider 选择器模式（Selector Pattern）

**问题**：Service 构造时不应绑定特定 Provider，需要支持主备切换

**解决方案**：
```
config.json → Selector → Providers (primary, fallback, ...)
                      ↓
                Provider Selection
                      ↓
               Service with Failover
```

### 2. 架构层次

```
cmd/picoclaw/internal/gateway/helpers.go
    ↓
channels.NewManager(cfg, msgBus, mediaStore)
    ↓ (内部调用)
initAudioServices() → asrService.NewServiceFromConfig(cfg)
                      ttsService.NewServiceFromConfig(cfg)
    ↓ (创建 Selector)
asr/selector.TTSSelector (管理多个 Provider)
tts/selector.ASRSelector (管理多个 Provider)
    ↓ (Provider 自注册)
asr/doubao.init() → asr.RegisterFactory("doubao", ...)
tts/doubao.init() → tts.RegisterFactory("doubao", ...)
    ↓ (Manager 注入到 Channel)
Manager.initChannel() → ch.SetASRService(asrSvc)
                        ch.SetTTSService(ttsSvc)
    ↓ (Channel 使用)
PicoChannel.handleAudioChunk() → asrService.Recognize()
PicoChannel.sendAudioResponse() → ttsService.Synthesize()
```

### 3. 关键组件

#### 3.1 ASR/TTS Selector (`pkg/asr/selector/`, `pkg/tts/selector/`)

- **职责**：管理多个 Provider，实现优先级选择和故障转移
- **优先级**：0 = 最高优先级（主 Provider），1+ = 备用
- **状态管理**：Healthy → Degraded → Unavailable
- **自动恢复**：健康检查定时重置状态

```go
type ASRSelector struct {
    providers []*ProviderEntry  // 按优先级排序
}

func (s *ASRSelector) GetProvider() (Provider, string, error) {
    // 返回最高可用的 Provider
}

func (s *ASRSelector) MarkFailed(name string, err error) {
    // 标记 Provider 为降级/不可用
}
```

#### 3.2 Service 层 (`pkg/asr/service/`, `pkg/tts/service/`)

- **职责**：封装 Provider 选择逻辑，提供高级 API
- **故障转移**：自动尝试下一个 Provider
- **健康检查**：后台定时检查 Provider 状态

```go
type Service struct {
    selector    *selector.ASRSelector
    healthCheck *time.Ticker
}

func (s *Service) Recognize(ctx, audio, format) (string, error) {
    // 尝试所有 Provider，自动故障转移
    for _, provider := range s.selector.GetProviders() {
        text, err := provider.Transcribe(ctx, audio, format)
        if err == nil {
            return text, nil  // 成功
        }
        s.selector.MarkFailed(provider.Name, err)  // 标记失败
    }
    return "", fmt.Errorf("all providers failed")
}
```

#### 3.3 Provider 工厂注册 (`pkg/asr/asr.go`, `pkg/tts/tts.go`)

- **职责**：Provider 包自注册，无需修改核心代码
- **扩展性**：添加新 Provider 只需创建包并调用 RegisterFactory

```go
// pkg/asr/doubao/doubao.go
func init() {
    asr.RegisterFactory("doubao", func(cfg *config.Config) Provider {
        return NewFromConfig(cfg)
    })
}
```

#### 3.4 Manager 注入 (`pkg/channels/manager.go`)

- **职责**：统一创建 Service，注入到所有支持的 Channel
- **解耦**：Channel 无需知道 Provider 细节，只接收 Service

```go
func (m *Manager) initAudioServices() (*asrService.Service, *ttsService.Service, error) {
    asrSvc, _ := asrService.NewServiceFromConfig(m.config)
    ttsSvc, _ := ttsService.NewServiceFromConfig(m.config)
    return asrSvc, ttsSvc, nil
}

func (m *Manager) initChannel(name, displayName string, asrSvc, ttsSvc) {
    ch := createChannel(name)
    
    // 注入服务
    if setter, ok := ch.(ASRAware); ok {
        setter.SetASRService(asrSvc)
    }
    if setter, ok := ch.(TTSAware); ok {
        setter.SetTTSService(ttsSvc)
    }
}
```

### 4. 配置示例

```json
{
  "audio": {
    "enabled": true,
    "asr": {
      "enabled": true,
      "provider": "doubao"
    },
    "tts": {
      "enabled": true,
      "provider": "doubao"
    },
    "providers": {
      "doubao": {
        "enabled": true,
        "format": "opus",
        "internal": {
          "appid": "xxx",
          "token": "xxx",
          "cluster": "volcengine_streaming_asr"
        }
      },
      "azure": {
        "enabled": true,
        "format": "pcm",
        "internal": {
          "region": "westus",
          "key": "xxx"
        }
      }
    }
  }
}
```

**故障转移**：当 `doubao` 失败时，自动切换到 `azure`

### 5. 添加新 Provider 的步骤

#### 步骤 1：创建 Provider 实现

```go
// pkg/asr/azure/azure.go
package azure

type Provider struct {
    region, key string
}

func NewFromConfig(cfg *config.Config) *Provider {
    internal := cfg.Audio.Providers["azure"].Internal
    return &Provider{
        region: internal["region"].(string),
        key:    internal["key"].(string),
    }
}

func (p *Provider) Transcribe(ctx context.Context, audio []byte, format asr.Format) (string, error) {
    // Azure ASR 实现
}

func (p *Provider) Name() string { return "azure" }

func init() {
    asr.RegisterFactory("azure", NewFromConfig)
}
```

#### 步骤 2：配置启用

```json
{
  "audio": {
    "providers": {
      "azure": {
        "enabled": true,
        "internal": { ... }
      }
    }
  }
}
```

✅ **无需修改 Channel、Manager 或任何核心代码！**

### 6. 接口定义

```go
// pkg/channels/interfaces.go

// ASRAware - Channel 实现此接口可接收 ASR 服务
type ASRAware interface {
    SetASRService(svc *asrService.Service)
}

// TTSAware - Channel 实现此接口可接收 TTS 服务
type TTSAware interface {
    SetTTSService(svc *ttsService.Service)
}
```

### 7. 优势总结

1. **零修改扩展**：添加 Provider 无需修改任何现有代码
2. **自动故障转移**：Service 自动尝试备用 Provider
3. **统一初始化**：Manager 统一创建和注入 Service
4. **解耦彻底**：Channel 不感知 Provider，只使用 Service 接口
5. **配置驱动**：通过 JSON 配置控制 Provider 优先级和启用状态
6. **向后兼容**：保留 NewService(provider) 用于单 Provider 场景

### 8. 与 v5 设计文档的对应

| v5 设计 | 实现 |
|---------|------|
| Service 层 Provider 选择 | `asr/selector/`, `tts/selector/` |
| 故障转移 (failover) | `Service.Recognize()` 循环尝试 Provider |
| Provider 注册 | `asr.RegisterFactory()`, `tts.RegisterFactory()` |
| Channel 注入 | `Manager.initChannel()` 调用 `SetASRService()` |
| 配置驱动 | `config.Audio.Providers` |

