# PicoClaw 音频（TTS/ASR）架构设计 v5

---

## 设计目标

- **零 CGO**：纯 Go 实现，支持全平台交叉编译
- **编译裁剪**：按需编译，最小化二进制体积
- **格式支持**：PCM（原生）、WAV（容器）、Opus（解码）、MP3（可选）
- **解耦架构**：Provider、Service、Channel 三层解耦
- **流式优先**：支持实时语音交互的低延迟场景

---

## 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                         Channel 层                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐ │
│  │  WebSocket  │  │   HTTP API  │  │      CLI/Other          │ │
│  └──────┬──────┘  └──────┬──────┘  └───────────┬─────────────┘ │
└─────────┼────────────────┼─────────────────────┼───────────────┘
          │                │                     │
          └────────────────┴─────────────────────┘
                           │
                           │ 原始音频数据（不解码）
                           │
┌──────────────────────────┴─────────────────────────────────────┐
│                         Service 层                              │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │                  Audio Middleware                          │ │
│  │  ┌─────────────┐  ┌─────────────┐  ┌───────────────────┐ │ │
│  │  │   Decoder   │  │   Encoder   │  │  Format Detector  │ │ │
│  │  │ (输入解码)  │  │  (输出编码)  │  │  (格式自动检测)   │ │ │
│  │  └─────────────┘  └─────────────┘  └───────────────────┘ │ │
│  └──────────────────────────┬────────────────────────────────┘ │
│                             │                                   │
│  ┌──────────────────────────┴───────────────────────────────┐  │
│  │                      Coordinator                          │  │
│  │         (流式模式：聚合 Token、缓冲、流量控制)              │  │
│  └──────────────┬───────────────────────────────┬────────────┘  │
│                 │                               │               │
│  ┌──────────────┴─────────────┐ ┌───────────────┴────────────┐ │
│  │       ASR Service          │ │        TTS Service         │ │
│  │  ┌──────────────────────┐  │ │  ┌──────────────────────┐  │ │
│  │  │   ASRProvider        │  │ │  │    TTSProvider       │  │ │
│  │  │  - Transcribe        │  │ │  │  - Synthesize        │  │ │
│  │  │  - TranscribeStream  │  │ │  │  - SynthesizeStream  │  │ │
│  │  └──────────────────────┘  │ │  └──────────────────────┘  │ │
│  └────────────────────────────┘ └────────────────────────────┘ │
└────────────────────────────────────────────────────────────────┘
                           │
                           │ Provider 接口
                           ▼
┌────────────────────────────────────────────────────────────────┐
│                      Provider 实现层                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │  Doubao  │  │  OpenAI  │  │  Azure   │  │ FunASR   │       │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘       │
└────────────────────────────────────────────────────────────────┘
```

---

## 音频编解码层

### 支持格式

| 格式 | 编码 | 解码 | 说明 |
|------|------|------|------|
| **PCM** | ✅ | ✅ | 原生支持，零依赖 |
| **WAV** | ✅ | ✅ | 容器格式，零依赖 |
| **Opus** | ❌ | ✅ | 仅解码，用于 WebRTC 输入 |
| **MP3** | ✅ | ✅ | 可选，需 build tag `codec_mp3` |

### 接口设计

```go
// Format 音频格式
type Format string
const (
    FormatPCM  Format = "pcm"
    FormatWAV  Format = "wav"
    FormatOpus Format = "opus"
    FormatMP3  Format = "mp3"
)

// Codec 编解码器接口
type Codec interface {
    Decode(ctx context.Context, data []byte) ([]byte, error)  // 任意格式 → PCM
    Encode(ctx context.Context, pcm []byte) ([]byte, error)   // PCM → 目标格式
    CanDecode(format Format) bool
    CanEncode(format Format) bool
}

// Manager 编解码管理器
type Manager struct {
    codecs map[Format]Codec
}
```

---

## ASR 服务层

### Provider 接口

```go
// ASRProvider ASR 提供商接口
type ASRProvider interface {
    // Transcribe 非流式语音识别
    Transcribe(ctx context.Context, audio []byte, format Format) (string, error)
    
    // TranscribeStream 流式语音识别（可选）
    TranscribeStream(ctx context.Context, audioStream <-chan []byte, format Format) (
        <-chan TranscriptionResult, error)
    
    Name() string
    Capabilities() ASRCapabilities
}

type ASRCapabilities struct {
    SupportsStreaming bool     // 是否支持流式识别
    SupportsFormat    []Format // 支持的输入格式（如 [PCM, WAV]）
    MaxAudioDuration  int      // 最大音频时长（秒）
}
```

### 注册机制

```go
// pkg/asr/registry.go
var providers = make(map[string]ASRProvider)

func Register(name string, provider ASRProvider) {
    providers[name] = provider
}

func Get(name string) (ASRProvider, bool) {
    p, ok := providers[name]
    return p, ok
}
```

**条件编译注册**：

```go
// pkg/asr/registry_doubao.go
//go:build asr_doubao || asr_all
package asr

import "github.com/sipeed/picoclaw/pkg/asr/doubao"

func init() {
    Register("doubao", doubao.New())
}
```

### Service 实现

```go
// pkg/asr/service.go

type Service struct {
    provider ASRProvider
    codecMgr *codec.Manager
}

// Recognize 识别音频（自动处理格式转换）
func (s *Service) Recognize(ctx context.Context, audio []byte, format Format) (string, error) {
    // 检查 Provider 是否支持该格式
    if !s.providerSupportsFormat(format) {
        // 不支持，需要解码为 PCM
        pcm, err := s.codecMgr.Decode(ctx, format, audio)
        if err != nil {
            return "", fmt.Errorf("decode audio: %w", err)
        }
        audio = pcm
        format = codec.FormatPCM
    }
    
    return s.provider.Transcribe(ctx, audio, format)
}

// RecognizeStream 流式识别
func (s *Service) RecognizeStream(ctx context.Context, 
    audioStream <-chan []byte, format Format) (
    <-chan TranscriptionResult, error) {
    
    // 如果不支持该格式，先解码
    if !s.providerSupportsFormat(format) {
        pcmStream := s.codecMgr.DecodeStream(ctx, format, audioStream)
        return s.provider.TranscribeStream(ctx, pcmStream, codec.FormatPCM)
    }
    
    return s.provider.TranscribeStream(ctx, audioStream, format)
}

func (s *Service) providerSupportsFormat(format Format) bool {
    for _, f := range s.provider.Capabilities().SupportsFormat {
        if f == format {
            return true
        }
    }
    return false
}
```

---

## TTS 服务层

### Provider 接口

```go
// TTSProvider TTS 提供商接口
type TTSProvider interface {
    // Synthesize 非流式语音合成
    Synthesize(ctx context.Context, text string, voiceID string) (
        audio []byte, format Format, err error)
    
    // SynthesizeStream 流式语音合成（可选）
    SynthesizeStream(ctx context.Context, text string, voiceID string) (
        <-chan TTSResult, error)
    
    Name() string
    OutputFormat() Format
    Capabilities() TTSCapabilities
}

type TTSResult struct {
    Audio     []byte
    Format    Format
    IsFinal   bool
    Timestamp time.Time
}

type TTSCapabilities struct {
    SupportsStreaming bool
    MaxTextLength     int
}
```

### Service 实现

```go
// pkg/tts/service.go

type Service struct {
    provider TTSProvider
    codecMgr *codec.Manager
}

// Synthesize 合成语音（自动转码为目标格式）
func (s *Service) Synthesize(ctx context.Context, text string, voiceID string, 
    targetFormat Format) ([]byte, error) {
    
    audio, sourceFormat, err := s.provider.Synthesize(ctx, text, voiceID)
    if err != nil {
        return nil, err
    }
    
    // 格式匹配，直接返回
    if sourceFormat == targetFormat {
        return audio, nil
    }
    
    // 需要转码
    return s.codecMgr.Transcode(ctx, audio, sourceFormat, targetFormat)
}

// SynthesizeStream 流式合成（自动转码）
func (s *Service) SynthesizeStream(ctx context.Context, text string, voiceID string,
    targetFormat Format) (<-chan []byte, error) {
    
    resultStream, err := s.provider.SynthesizeStream(ctx, text, voiceID)
    if err != nil {
        return nil, err
    }
    
    sourceFormat := s.provider.OutputFormat()
    
    // 格式匹配，直接透传
    if sourceFormat == targetFormat {
        return s.extractAudioStream(resultStream), nil
    }
    
    // 需要转码，启动转码 goroutine
    return s.transcodeStream(ctx, resultStream, sourceFormat, targetFormat)
}
```

---

## 协调器层

### 协调器定位

**协调器是流式模式的核心组件，负责：**

1. **Token 聚合**：将 LLM 输出的零散 Token 聚合成完整句子
2. **流水线协调**：管理 ASR → LLM → TTS 的数据流
3. **流量控制**：防止 TTS 处理速度跟不上 LLM 生成速度

### 使用场景

| 模式 | 是否使用协调器 | 说明 |
|------|---------------|------|
| **流式 ASR + TTS** | ✅ | 需要聚合 ASR 结果，协调 LLM Token，触发 TTS |
| **流式仅 TTS** | ✅ | TTS Service 内部使用简单流式处理，无需完整协调器 |
| **非流式任何模式** | ❌ | 串行调用 ASR/TTS Service 即可 |

**注：仅 TTS 流式模式时，TTS Service 直接处理流式输出，不需要独立协调器组件。**

**特殊情况：混合流式/非流式**

当 LLM 和 TTS 的流式支持不一致时，协调器提供适配策略：
- **LLM 流式 + TTS 非流式**：Token 聚合为完整句子 → 批量 TTS → 分块流式发送
- **LLM 非流式 + TTS 流式**：等待完整响应 → 分句 → 逐句流式 TTS
- **两者都非流式**：完全非流式处理，对外保持流式接口（单帧）

### 接口设计

```go
// Coordinator 流式协调器
type Coordinator struct {
    asrService   *asr.Service
    ttsService   *tts.Service
    llmClient    llm.Client
    inputFormat  Format  // Channel 约定的输入格式
    outputFormat Format  // Channel 要求的输出格式
}

// Session 语音会话
type Session struct {
    ID           string
    SentenceBuf  *SentenceBuffer
    InputFormat  Format  // 从 Coordinator 继承
    OutputFormat Format  // 从 Coordinator 继承
    ctx          context.Context
    cancel       context.CancelFunc
}

// SentenceBuffer 句子缓冲区
type SentenceBuffer struct {
    maxTokens int
    timeout   time.Duration
}

// StartSession 启动流式会话
// 格式信息从 Coordinator 初始化时传入
func (c *Coordinator) StartSession(ctx context.Context) (*Session, error)

// ProcessAudioChunk 处理音频分块（流式输入）
func (c *Coordinator) ProcessAudioChunk(session *Session, audioChunk []byte) error

// StopSession 停止会话
func (c *Coordinator) StopSession(session *Session) error
```

### 工作流程

```go
// 伪代码：协调器流式处理
func (c *Coordinator) run(session *Session, audioInput <-chan []byte) {
    // 1. ASR 流式识别 → 文本
    textStream := c.asrService.RecognizeStream(session.ctx, audioInput, session.InputFormat)
    
    // 2. 聚合为完整句子
    sentenceStream := aggregateToSentences(textStream, session.SentenceBuf)
    
    // 3. 句子 → LLM → TTS
    for sentence := range sentenceStream {
        // 发送到 LLM，获取 Token 流
        llmTokenStream := c.llmClient.CompleteStream(sentence)
        
        // 4. Token 再次聚合为句子
        ttsInputStream := aggregateTokens(llmTokenStream, session.SentenceBuf)
        
        // 5. 发送到 TTS（流式）
        for ttsInput := range ttsInputStream {
            audioStream := c.ttsService.SynthesizeStream(session.ctx, ttsInput, voiceID, session.OutputFormat)
            
            // 6. 输出音频
            for audioChunk := range audioStream {
                session.Output <- audioChunk
            }
        }
    }
}
```

### 混合流式/非流式处理

实际场景中，LLM 和 TTS 的流式支持可能不一致，协调器需要提供适配策略：

#### 场景矩阵

| LLM | TTS | 处理策略 | 延迟 |
|-----|-----|----------|------|
| 流式 | 流式 | 标准流式流水线 | 低 |
| 流式 | 非流式 | **流式→聚合→批量** | 中 |
| 非流式 | 流式 | **完整文本→分句→流式** | 中 |
| 非流式 | 非流式 | 完全非流式 | 高 |

#### 1. LLM 流式 + TTS 非流式

**策略**：Token 流聚合为完整句子 → 批量调用 TTS → 流式发送结果

```go
// 伪代码：LLM 流式 + TTS 非流式
func (c *Coordinator) handleStreamingLLM_NonStreamingTTS(session *Session, sentence string) {
    // 1. LLM 流式生成 Token
    tokenStream := c.llmClient.CompleteStream(sentence)
    
    // 2. 聚合 Token 为完整句子
    fullText := aggregateTokensToText(tokenStream)
    
    // 3. 等待完整文本生成完成
    <-fullText.Done()
    text := fullText.String()
    
    // 4. 批量调用 TTS（非流式）
    audio, _, err := c.ttsService.Synthesize(session.ctx, text, voiceID, session.OutputFormat)
    if err != nil {
        return
    }
    
    // 5. 模拟流式输出（分块发送）
    chunks := splitIntoChunks(audio, 1024) // 每块 1KB
    for _, chunk := range chunks {
        session.Output <- chunk
        time.Sleep(20 * time.Millisecond) // 模拟流式间隔
    }
}
```

**优化策略**：
- **提前触发**：当句子长度超过阈值时，即使 LLM 还在生成，也提前发送给 TTS
- **预加载**：TTS 处理时，继续接收 LLM Token，准备下一句

#### 2. LLM 非流式 + TTS 流式

**策略**：等待完整 LLM 响应 → 分句 → 逐句流式 TTS

```go
// 伪代码：LLM 非流式 + TTS 流式
func (c *Coordinator) handleNonStreamingLLM_StreamingTTS(session *Session, sentence string) {
    // 1. LLM 非流式生成完整文本
    fullText := c.llmClient.Complete(sentence)
    
    // 2. 分句处理
    sentences := splitIntoSentences(fullText)
    
    // 3. 逐句流式 TTS
    for _, s := range sentences {
        audioStream, err := c.ttsService.SynthesizeStream(session.ctx, s, voiceID, session.OutputFormat)
        if err != nil {
            continue
        }
        
        // 4. 流式输出
        for audioChunk := range audioStream {
            session.Output <- audioChunk
        }
    }
}
```

#### 3. 两者都非流式（降级模式）

**策略**：完全非流式处理，但对外保持流式接口

```go
// 伪代码：完全非流式
func (c *Coordinator) handleNonStreaming(session *Session, sentence string) {
    // 1. LLM 非流式
    fullText := c.llmClient.Complete(sentence)
    
    // 2. TTS 非流式
    audio, _, err := c.ttsService.Synthesize(session.ctx, fullText, voiceID, session.OutputFormat)
    if err != nil {
        return
    }
    
    // 3. 一次性发送（模拟单帧流式）
    session.Output <- audio
}
```

#### 4. 适配器模式

将非流式组件包装为流式接口，简化协调器逻辑：

```go
// StreamingAdapter 流式适配器
type StreamingAdapter struct {
    llmClient  llm.Client
    ttsService *tts.Service
}

// AdaptLLM 将 LLM 包装为流式接口
func (a *StreamingAdapter) AdaptLLM(ctx context.Context, text string) <-chan string {
    tokenStream := make(chan string)
    
    go func() {
        defer close(tokenStream)
        
        if a.llmClient.SupportsStreaming() {
            // 原生支持流式
            for token := range a.llmClient.CompleteStream(text) {
                tokenStream <- token
            }
        } else {
            // 非流式，模拟流式
            fullText := a.llmClient.Complete(text)
            tokens := simulateTokenStream(fullText) // 按字/词分割
            for _, token := range tokens {
                tokenStream <- token
                time.Sleep(10 * time.Millisecond) // 模拟生成延迟
            }
        }
    }()
    
    return tokenStream
}

// AdaptTTS 将 TTS 包装为流式接口
func (a *StreamingAdapter) AdaptTTS(ctx context.Context, text string) <-chan []byte {
    audioStream := make(chan []byte)
    
    go func() {
        defer close(audioStream)
        
        if a.ttsService.SupportsStreaming() {
            // 原生支持流式
            stream, _ := a.ttsService.SynthesizeStream(ctx, text, voiceID, format)
            for chunk := range stream {
                audioStream <- chunk
            }
        } else {
            // 非流式，分块发送
            audio, _, _ := a.ttsService.Synthesize(ctx, text, voiceID, format)
            chunks := splitIntoChunks(audio, 1024)
            for _, chunk := range chunks {
                audioStream <- chunk
                time.Sleep(20 * time.Millisecond)
            }
        }
    }()
    
    return audioStream
}
```

#### 5. 协调器统一处理逻辑

使用适配器后，协调器无需关心底层是否支持流式：

```go
func (c *Coordinator) runUnified(session *Session, audioInput <-chan []byte) {
    adapter := &StreamingAdapter{
        llmClient:  c.llmClient,
        ttsService: c.ttsService,
    }
    
    // 1. ASR 识别
    sentenceStream := c.asrService.RecognizeStream(session.ctx, audioInput, session.InputFormat)
    
    for sentence := range sentenceStream {
        // 2. LLM（自动适配流式/非流式）
        tokenStream := adapter.AdaptLLM(session.ctx, sentence)
        
        // 3. 聚合为句子
        ttsInputStream := aggregateTokens(tokenStream, session.SentenceBuf)
        
        for ttsInput := range ttsInputStream {
            // 4. TTS（自动适配流式/非流式）
            audioStream := adapter.AdaptTTS(session.ctx, ttsInput)
            
            // 5. 输出
            for audioChunk := range audioStream {
                session.Output <- audioChunk
            }
        }
    }
}
```

### 性能对比

| 场景 | 首字延迟 | 总延迟 | CPU 占用 | 适用场景 |
|------|----------|--------|----------|----------|
| 全流式 | 低 (~500ms) | 低 | 中 | 实时对话 |
| LLM流+TTS非 | 高 (~2s) | 中 | 低 | TTS 质量优先 |
| LLM非+TTS流 | 高 (~3s) | 中 | 中 | LLM 质量优先 |
| 全非流式 | 高 (~5s) | 高 | 低 | 后台处理 |

### 选择建议

- **实时语音助手**：优先选择全流式 Provider（豆包、Azure）
- **高质量合成**：接受 LLM 流式 + TTS 非流式，牺牲部分延迟换取音质
- **成本敏感**：选择全非流式，使用更便宜的非流式 API

### 与 Channel 的交互

```go
// Channel 中启动流式会话
func (c *Channel) StartStreamingSession() error {
    if c.config.Streaming {
        // 流式模式：使用协调器
        // 格式信息已在 Coordinator 初始化时传入
        session, err := c.coordinator.StartSession(ctx)
        c.currentSession = session
    }
}

// 处理音频输入
func (c *Channel) HandleAudioInput(audio []byte) {
    if c.config.Streaming && c.coordinator != nil {
        // 流式模式：分块发送给协调器
        c.coordinator.ProcessAudioChunk(c.currentSession, audio)
    } else {
        // 非流式模式：直接调用 ASR Service
        text, _ := c.asrService.Recognize(ctx, audio, c.config.InputFormat)
        c.handleTextInput(text)
    }
}

// 处理文本输出（需要 TTS）
func (c *Channel) HandleTextOutput(text string) {
    if c.config.Streaming && c.coordinator != nil {
        // 流式模式下由协调器处理，这里不直接调用 TTS
        return
    }
    
    // 非流式模式：直接调用 TTS Service
    audio, _ := c.ttsService.Synthesize(ctx, text, voiceID, c.config.OutputFormat)
    c.sendAudio(audio)
}
```

---

## 编译时裁剪

### 构建标签

```
# 音频编解码器
codec_pcm      - PCM 支持（默认启用）
codec_wav      - WAV 支持
codec_opus     - Opus 解码支持
codec_mp3      - MP3 支持（可选）
codec_all      - 所有编解码器
noaudio        - 完全禁用音频

# ASR 提供商
asr_doubao     - 豆包 ASR
asr_openai     - OpenAI Whisper
asr_azure      - Azure Speech
asr_funasr     - FunASR
asr_all        - 所有 ASR
noasr          - 禁用 ASR

# TTS 提供商
tts_doubao     - 豆包 TTS
tts_openai     - OpenAI TTS
tts_azure      - Azure TTS
tts_fishspeech - FishSpeech
tts_all        - 所有 TTS
notts          - 禁用 TTS
```

### 编译示例

```bash
# 场景1: 最小体积（仅文本聊天）
go build -tags "noaudio,noasr,notts" ./cmd/picoclaw

# 场景2: WebRTC 语音助手（Opus 输入输出）
go build -tags "codec_opus,asr_doubao,tts_doubao" ./cmd/picoclaw

# 场景3: 通用语音助手（支持 WAV 文件上传）
go build -tags "codec_wav,codec_opus,asr_all,tts_all" ./cmd/picoclaw

# 场景4: 完整功能
go build -tags "codec_all,asr_all,tts_all" ./cmd/picoclaw
```

---

## 配置设计

```json
{
  "audio": {
    "enabled": true,
    "asr": {
      "default_provider": "doubao",
      "providers": {
        "doubao": {
            "enabled": true,
            "support_streaming": true,
            "format": "opus",
            "appid": "xxx",
            "token": "xxx",
            "cluster": "volcengine_streaming_asr"
        }
      },
    },
    "tts": {
      "default_provider": "doubao",
      "providers": {
        "doubao": {
            "enabled": true,
            "support_streaming": true,
            "format": "opus",
            "appid": "xxx",
            "token": "xxx",
            "voice": "zh_female_wanwanxiaohe_moon_bigtts"
        }
      }
    },
    "coordinator": {
      "enabled": true,
    }
  },
  "channels": {
    "websocket": {
      "audio": {
        "enable_asr": true,
        "enable_tts": true,
        "asr_provider": "doubao",
        "tts_provider": "doubao",
        "streaming": true,
      }
    }
  }
}
```

---

## 使用流程

### 1. Channel 初始化

```go
func NewChannel(config ChannelConfig) (*Channel, error) {
    // 1. 检查编解码器是否已编译
    if !codec.IsAvailable(config.InputFormat) {
        return nil, fmt.Errorf("codec %s not compiled in", config.InputFormat)
    }
    if !codec.IsAvailable(config.OutputFormat) {
        return nil, fmt.Errorf("codec %s not compiled in", config.OutputFormat)
    }
    
    // 2. 初始化 ASR/TTS Service（内部包含 Audio Middleware）
    // 从全局 Provider 注册表获取指定 Provider
    asrProvider, ok := asr.Get(config.ASRProvider)
    if !ok {
        return nil, fmt.Errorf("ASR provider %s not found", config.ASRProvider)
    }
    ttsProvider, ok := tts.Get(config.TTSProvider)
    if !ok {
        return nil, fmt.Errorf("TTS provider %s not found", config.TTSProvider)
    }
    
    asrService, err := asr.NewService(asrProvider)
    if err != nil {
        return nil, fmt.Errorf("init ASR service: %w", err)
    }
    ttsService, err := tts.NewService(ttsProvider)
    if err != nil {
        return nil, fmt.Errorf("init TTS service: %w", err)
    }
    
    // 3. 初始化协调器（仅流式模式）
    var coordinator *Coordinator
    if config.Streaming {
        coordinator = NewCoordinator(asrService, ttsService, llmClient, 
            config.InputFormat, config.OutputFormat)
    }
    
    return &Channel{
        asrService:    asrService,
        ttsService:    ttsService,
        coordinator:   coordinator,
        config:        config,
    }, nil
}
```

### 2. 音频编解码使用时机

| 时机 | 层级 | 操作 | 说明 |
|------|------|------|------|
| **ASR 输入前** | ASR Service | Decode | 如果 Provider 不支持输入格式，解码为 PCM |
| **TTS 输出后** | TTS Service | Encode | 将 Provider 输出转码为 Channel 要求格式 |

**注：Channel 只处理原始字节流，不解码/编码。格式转换在 Service 层完成。**

### 3. 流式 vs 非流式

```go
// 流式模式（使用协调器）
func (c *Channel) handleStreamingAudio(audioChunk []byte) {
    c.coordinator.ProcessAudioChunk(c.session, audioChunk)
    // 音频输出通过协调器回调
}

// 非流式模式（直接调用 Service）
func (c *Channel) handleNonStreamingAudio(audio []byte) {
    // 1. ASR（Service 自动处理格式转换）
    text, _ := c.asrService.Recognize(ctx, audio, c.config.InputFormat)
    
    // 2. LLM
    response, _ := c.llmClient.Complete(text)
    
    // 3. TTS（Service 自动将 Provider 输出转码为 Channel 要求的格式）
    audioOut, _ := c.ttsService.Synthesize(ctx, response, voiceID, c.config.OutputFormat)
    
    c.sendAudio(audioOut)
}
```

### 4. 错误处理

```go
// 编解码器不支持时
if !codecMgr.IsFormatSupported(format) {
    return fmt.Errorf("format %s not supported, rebuild with -tags 'codec_%s'", 
        format, format)
}

// Provider 不支持格式时（Service 层自动处理）
audio, err := asrService.Recognize(ctx, audio, format)
// Service 内部会自动解码为 Provider 支持的格式
```

---

## 设计原则总结

1. **Service 层负责编解码**：ASR/TTS Service 内部根据 Provider 能力决定是否转码
2. **协调器仅用于流式模式**：非流式模式直接调用 Service，流式模式使用协调器管理流水线
3. **仅 TTS 流式**：TTS Service 内部处理流式逻辑，无需独立协调器组件
4. **Channel 保持简单**：只负责原始数据传输，不感知音频格式
5. **编译时确定**：Provider 和 Codec 在编译期确定，运行时通过条件编译文件注册
6. **JSON 配置**：使用 JSON 作为配置文件格式
