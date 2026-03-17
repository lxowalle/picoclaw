# 方案验证：asr.NewService() 设计

## 你的设计思路

```
Channel (不修改接口)
    ↓ 调用
asr.NewService(providerName, cfg) → 返回 *service.Service
    ↓ 内部处理
    1. 创建 Provider (根据 providerName)
    2. 包装为 Service
    3. 返回 Service
```

## 实现方式

### 方案 1：在 asr 包中封装（推荐）

**asr/asr.go**:
```go
// NewService 根据 provider 名称创建 ASR Service
// Channel 只需调用此函数，无需了解 Provider 细节
func NewService(providerName string, cfg *config.Config) (*service.Service, error) {
    provider, err := NewProviderFromConfig(providerName, cfg)
    if err != nil {
        return nil, err
    }
    return service.NewService(provider), nil
}
```

**Channel 使用**:
```go
func (c *PicoChannel) initAudioServices(cfg *config.Config) error {
    if cfg.Audio.ASRProvider != "" {
        svc, err := asr.NewService(cfg.Audio.ASRProvider, cfg)
        if err == nil {
            c.asrService = svc
        }
    }
    return nil
}
```

**优点**:
- ✅ Channel 代码极简（1 行调用）
- ✅ 添加 Provider 不修改 Channel
- ✅ 符合"不要修改 channel 的接口"要求

### 方案 2：Channel 保持现状

当前 Channel 已经使用了工厂模式：
```go
provider, err := asr.NewProviderFromConfig(providerName, cfg)
if err == nil {
    c.asrService = asrService.NewService(provider)
}
```

**问题**:
- Channel 需要 import service 包
- 多一行代码

## 结论

**方案 1 更好**：
1. 添加 `asr.NewService()` 和 `tts.NewService()` 函数
2. Channel 代码简化到极致
3. 完全符合你的要求

是否要按方案 1 实现？