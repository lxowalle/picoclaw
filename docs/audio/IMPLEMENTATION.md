# PicoClaw 音频功能实现总结

## 实现概览

基于 `docs/audio/picoclaw_audio_design_proposal_v5.md`，已实现以下功能：

## 架构组件

### 1. Provider 层

#### ASR Provider (`pkg/asr/`)
- **接口定义**：`pkg/asr/asr.go`
  - `Provider` 接口：定义了 `Transcribe` 和 `TranscribeStream` 方法
  - `TranscriptionResult`：识别结果结构
  - `ASRCapabilities`：Provider 能力描述
  - 全局注册表：`Register`, `Get`, `List`

- **Doubao 实现**：`pkg/asr/doubao/doubao.go`
  - 支持流式和非流式识别
  - 配置项：AppID、Token、Cluster、APIBase
  - `New()` - 从 Config 结构体创建 Provider
  - `NewFromConfig()` - 从应用程序 config 创建 Provider
  - **注意**：API 调用部分为占位实现，需要接入真实的豆包 API

#### TTS Provider (`pkg/tts/`)
- **接口定义**：`pkg/tts/tts.go`
  - `Provider` 接口：定义了 `Synthesize` 和 `SynthesizeStream` 方法
  - `TTSResult`：合成结果结构
  - `TTSCapabilities`：Provider 能力描述
  - 全局注册表：`Register`, `Get`, `List`

- **Doubao 实现**：`pkg/tts/doubao/doubao.go`
  - 支持流式和非流式合成
  - 配置项：AppID、Token、Voice、APIBase、Format
  - `New()` - 从 Config 结构体创建 Provider
  - `NewFromConfig()` - 从应用程序 config 创建 Provider
  - **注意**：API 调用部分为占位实现，需要接入真实的豆包 API

### 2. Service 层

#### ASR Service (`pkg/asr/service/service.go`)
- 包装 Provider，提供高阶功能
- 自动格式转换（预留接口）
- 简化调用接口

#### TTS Service (`pkg/tts/service/service.go`)
- 包装 Provider，提供高阶功能
- 自动格式转换（预留接口）
- 支持目标格式合成

### 3. Channel 集成

#### Pico Channel (`pkg/channels/pico/`)
- **协议扩展**：`protocol.go`
  - 新增 `audio.start` - 开始音频流
  - 新增 `audio.chunk` - 音频数据块
  - 新增 `audio.stop` - 结束音频流
  - 新增 `audio.data` - 服务端音频数据

- **功能实现**：`pico.go`
  - `handleAudioChunk` - 处理音频输入，调用 ASR 识别
  - `sendAudioResponse` - 使用 TTS 合成并发送音频
  - `initAudioServices` - 根据配置初始化服务

### 4. 配置层

#### 配置结构 (`pkg/config/config.go`)
- `AudioConfig` - 全局音频配置
- `PicoAudioConfig` - Pico Channel 音频配置
- `AudioProviderConfig` - Provider 通用配置
  - `Enabled` - 是否启用
  - `Format` - 音频格式
  - `SupportStreaming` - 是否支持流式
  - `Internal` - Provider 特定配置（map[string]interface{}）
- `PicoConfig` 扩展：
  - `Audio` - 音频功能配置

**设计特点**：
- Providers 是字典类型 `map[string]AudioProviderConfig`，支持多实例
- Provider 特定配置放在 `internal` 字段，由 Provider 自己解析
- 核心配置层无需知道 Provider 的具体字段

## 项目结构

```
pkg/
├── asr/
│   ├── asr.go                    # ASR Provider 接口和注册表
│   ├── registry_doubao.go        # Doubao 注册文件（条件编译）
│   ├── doubao/
│   │   └── doubao.go             # Doubao ASR 实现
│   └── service/
│       └── service.go            # ASR Service 层
├── tts/
│   ├── tts.go                    # TTS Provider 接口和注册表
│   ├── registry_doubao.go        # Doubao 注册文件（条件编译）
│   ├── doubao/
│   │   └── doubao.go             # Doubao TTS 实现
│   └── service/
│       └── service.go            # TTS Service 层
└── channels/pico/
    ├── protocol.go               # 协议定义（新增音频类型）
    └── pico.go                   # Pico Channel 实现（集成音频）
```

## 使用示例

### 配置示例

```json
{
  "channels": {
    "pico": {
      "enabled": true,
      "token": "your-token",
      "audio": {
        "enabled": true,
        "asr_provider": "doubao",
        "tts_provider": "doubao"
      }
    }
  },
  "audio": {
    "enabled": true,
    "providers": {
      "doubao": {
        "enabled": true,
        "format": "opus",
        "support_streaming": true,
        "internal": {
          "appid": "your-app-id",
          "token": "your-token",
          "cluster": "volcengine_streaming_asr",
          "voice": "zh_female_wanwanxiaohe_moon_bigtts"
        }
      }
    }
  }
}
```

### 音频格式处理

- **输入格式**：从客户端消息 payload 的 `format` 字段获取，默认 `opus`
- **输出格式**：由 Provider 配置中的 `format` 字段决定

### 构建命令

```bash
# 启用 Doubao 支持
go build -tags "asr_doubao tts_doubao" ./cmd/picoclaw

# 启用所有音频功能
go build -tags "asr_all tts_all" ./cmd/picoclaw
```

### 客户端音频消息示例

```json
{
  "type": "audio.chunk",
  "id": "msg-001",
  "session_id": "session-001",
  "payload": {
    "data": "base64-encoded-opus-data",
    "format": "opus"
  }
}
```

## 待完成工作

1. **Doubao API 集成**
   - 实现真实的豆包 ASR API 调用
   - 实现真实的豆包 TTS API 调用
   - 处理 API 认证和错误

2. **音频编解码器**
   - 实现格式自动转换（如 Opus → PCM）
   - 支持更多音频格式

3. **流式支持**
   - 实现流式 ASR
   - 实现流式 TTS
   - 实现协调器（Coordinator）层

4. **其他 Provider**
   - Azure ASR/TTS
   - OpenAI Whisper/TTS
   - FunASR
   - FishSpeech

5. **测试和文档**
   - 单元测试
   - 集成测试
   - 使用文档

## 设计符合度

本实现基本遵循了 v5 设计文档的架构：

✅ **三层架构**：Provider → Service → Channel  
✅ **零 CGO**：纯 Go 实现  
✅ **编译裁剪**：使用 build tags 控制 Provider 编译  
✅ **解耦设计**：各层独立，通过接口交互  
⚠️ **流式支持**：骨架已预留，需完整实现 API  
⚠️ **格式转换**：预留接口，需实现 Codec 层  

## 设计决策分析

### Provider 配置设计：通用字段 + internal 字段

#### 优点

1. **扩展性**：添加新 Provider 不需要修改核心配置代码
2. **解耦**：核心层不需要了解每个 Provider 的具体配置字段
3. **多实例支持**：字典形式支持配置多个相同类型但不同参数的 Provider
4. **向后兼容**：Provider 可以自由添加新字段而不影响其他模块

#### 缺点

1. **类型安全丢失**：`internal` 使用 `map[string]interface{}`，失去编译时类型检查
2. **配置验证困难**：错误只能在 Provider 初始化时发现，而不是启动时
3. **文档分散**：每个 Provider 需要单独维护配置文档
4. **IDE 支持减弱**：无法提供 JSON schema 和自动补全
5. **配置错误难以发现**：如字段名拼写错误只能在运行时暴露

#### 对比方案

**方案 A（当前实现）**：通用字段 + internal 字典
```go
type AudioProviderConfig struct {
    Enabled  bool
    Format   string
    Internal map[string]interface{}
}
```

**方案 B（强类型）**：每个 Provider 有自己的配置结构
```go
type AudioProvidersConfig struct {
    Doubao DoubaoConfig
    Azure  AzureConfig
}
```

**方案 C（混合）**：使用 json.RawMessage 延迟解析
```go
type AudioProviderConfig struct {
    Enabled  bool
    Format   string
    Internal json.RawMessage
}
```

当前选择方案 A 是为了最大化扩展性和解耦性，适合 Provider 可能频繁变化的场景。

## 后续建议

1. **优先完成 API 集成**：接入真实的豆包 API，使功能可用
2. **添加格式转换**：支持不同格式间的自动转换
3. **实现流式处理**：降低延迟，提升用户体验
4. **添加更多 Provider**：提供用户更多选择
5. **完善测试**：确保稳定性和可靠性
6. **配置验证**：考虑添加配置验证工具，在启动前检查 Provider 配置
