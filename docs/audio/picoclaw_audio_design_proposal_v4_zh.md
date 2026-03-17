# PicoClaw 音频（TTS/ASR）架构设计

## 设计原则

为 PicoClaw 添加灵活的语音交互能力，支持：

- **纯文本聊天**：现有的文本交互流程保持不变
- **仅 ASR**：语音输入 → 文本输出（适用于不支持语音输出的场景）
- **仅 TTS**：文本输入 → 语音输出（适用于不支持语音输入的场景）
- **全语音**：语音输入 → 语音输出

**核心原则**：Channel 自主决策，Agent 保持透明，不强制语音能力。

---

## 目录结构

```
picoclaw/
├── pkg/
│   ├── asr/
│   │   ├── service.go            # ASR 服务（路由 + 管理）
│   │   └── providers/            # 独立的提供商实现
│   │       ├── openai/
│   │       │   ├── provider.go   # HTTP REST 实现
│   │       │   └── config.go     # OpenAI 专用配置
│   │       ├── doubao/
│   │       │   ├── provider.go   # WebSocket 二进制实现
│   │       │   └── config.go     # 豆包专用配置
│   │       ├── azure/
│   │       │   ├── provider.go   # WebSocket JSON 实现
│   │       │   └── config.go     # Azure 专用配置
│   │       └── funasr/
│   │           ├── provider.go   # WebSocket JSON 实现
│   │           └── config.go     # FunASR 专用配置
│   │
│   ├── tts/
│   │   ├── service.go            # TTS 服务（路由 + 管理）
│   │   └── providers/
│   │       ├── openai/
│   │       │   ├── provider.go   # HTTP REST 实现
│   │       │   └── config.go
│   │       ├── doubao/
│   │       │   ├── provider.go   # WebSocket 二进制实现
│   │       │   └── config.go
│   │       ├── azure/
│   │       │   ├── provider.go   # WebSocket SSML 实现
│   │       │   └── config.go
│   │       └── fishspeech/
│   │           ├── provider.go   # HTTP REST 实现
│   │           └── config.go
│   │
│   ├── audio/
│   │   ├── coordinator.go        # 流式协调器（可选）
│   │   └── session.go            # 会话管理
│   │
│   └── channels/
│       └── websocket/
│           └── audio.go          # Channel 集成
│
└── config/
    └── config.example.json       # 完整配置示例
```

---

## 四种交互模式

### 模式 1：纯文本（现有流程，保持不变）

```
用户          Channel         Agent
 │               │              │
 │──文本────────►│              │
 │               │──llm.req────►│
 │               │              │
 │               │◄─llm.resp────│
 │               │              │
 │◄──文本────────│              │
```

**使用场景**：所有 Channel，默认行为

---

### 模式 2：仅 ASR（语音输入，文本输出）

```
用户          Channel        ASR 服务      Agent
 │               │               │            │
 │──语音────────►│               │            │
 │               │               │            │
 │               │──audio.input─►│            │
 │               │               │            │
 │               │◄─asr.result───│            │
 │               │               │            │
 │               │───────────────┴───────────►│
 │               │                            │
 │               │◄───────────────────────────│
 │               │                            │
 │◄──文本────────│                            │
```

**使用场景**：

- 客服场景（用户说话，客服阅读文本回复）
- 移动端省流量（仅下载文本）

**配置**：`EnableInput: true, EnableOutput: false`

---

### 模式 3：仅 TTS（文本输入，语音输出）

```
用户          Channel        TTS 服务      Agent
 │               │               │            │
 │──文本────────►│               │            │
 │               │               │            │
 │               │───────────────┴───────────►│
 │               │                            │
 │               │◄───────────────────────────│
 │               │                            │
 │               │──tts.request────►│          │
 │               │               │            │
 │               │◄─audio.output───│          │
 │               │               │            │
 │◄──语音────────│               │            │
```

**配置**：`EnableInput: false, EnableOutput: true`

---

### 模式 4：全语音（语音输入，语音输出）

#### 4.1 非流式（简单场景）

```
用户          Channel       ASR 服务     Agent      TTS 服务
 │               │               │            │             │
 │──语音────────►│               │            │             │
 │               │               │            │             │
 │               │──audio.input─►│            │             │
 │               │               │            │             │
 │               │◄─asr.result───│            │             │
 │               │               │            │             │
 │               │───────────────┴───────────►│             │
 │               │                            │             │
 │               │◄───────────────────────────│             │
 │               │                            │             │
 │               │──tts.request───────────────┼────────────►│
 │               │                            │             │
 │               │◄─audio.output──────────────┼─────────────│
 │               │                            │             │
 │◄──语音────────│                            │             │
```

#### 4.2 流式（低延迟场景）

```
用户          Channel       ASR 服务   协调器    Agent    TTS 服务
 │               │               │            │            │          │
 │──语音────────►│               │            │            │          │
 │               │               │            │            │          │
 │               │──audio.input─►│            │            │          │
 │               │               │            │            │          │
 │               │               │─asr.result►│            │          │
 │               │               │            │            │          │
 │               │               │            │──llm.req──►│          │
 │               │               │            │            │          │
 │◄──实时────────│◄──────────────│◄───────────│◄─Token────│          │
 │    文本       │               │            │            │          │
 │               │               │            │            │          │
 │               │               │            │聚合        │          │
 │               │               │            │Token       │          │
 │               │               │            │            │          │
 │               │               │            │─tts.req───-----------►│
 │               │               │            │            │          │
 │               │               │            │◄─audio.chunk──────────│
 │               │               │            │            │          │
 │               │◄──────────────│◄───────────│◄─audio.chunk         │
 │               │               │            │            │          │
 │◄──音频────────│               │            │            │          │
 │    分块       │               │            │            │          │
 │               │◄──────────────│◄───────────│◄─audio.chunk         │
 │               │               │            │            │          │
 │◄──音频────────│               │            │            │          │
 │    分块       │               │            │            │          │
```

**使用场景**：

- 语音助手（类似 Siri/Alexa 的交互）
- 实时翻译
- 智能语音客服

**配置**：`EnableInput: true, EnableOutput: true`

**流式说明**：

- 非流式：Channel 调用 ASR → 等待结果 → 调用 Agent → 调用 TTS
- 流式：需要协调器协调 ASR 流式识别 + LLM Token 流 + TTS 分块合成

## 模块职责

### ASR 服务

```go
type Service struct {
    primary  Provider
    fallback Provider
}

// 非流式识别
func (s *Service) Transcribe(ctx, audio, format) (text, error)

// 流式识别（返回通道用于实时结果）
func (s *Service) TranscribeStream(ctx, config) (<-chan Result, error)
```

**职责**：

- 管理 ASR 提供商（主备切换）
- 处理语音转文本
- 在流式模式下实时返回部分/最终结果

### TTS 服务

```go
type Service struct {
    provider Provider
}

// 非流式合成
func (s *Service) Synthesize(ctx, text, voiceID) (audio, error)

// 流式合成
func (s *Service) SynthesizeStream(ctx, config) (Stream, error)
```

**职责**：

- 管理 TTS 提供商
- 处理文本转语音
- 在流式模式下按句子返回音频分块

### 音频协调器（仅流式）

```go
type Coordinator struct {
    asrService ASRService
    ttsService TTSService
    sessions   map[string]*Session
}

type Session struct {
    ID          string
    SentenceBuf *SentenceBuffer  // 句子缓冲区
    IsStreaming bool
}
```

**职责**：

- **仅在流式模式下使用**
- 聚合 LLM Token 流，按句子触发 TTS
- 管理流式会话状态
- 提供缓冲和流量控制

---

## 为什么流式需要协调器

### 流式的本质

流式的目标是实现**边输入边输出**的低延迟体验，但这引入了数据粒度不匹配的问题：

| 阶段              | 输出粒度                    | 输入要求                  |
| ----------------- | --------------------------- | ------------------------ |
| **ASR 流式**      | 单词/短语（实时）           | 音频分块                  |
| **LLM 流式**      | Token（逐个）               | 完整提示                  |
| **TTS 流式**      | 句子（完整语义单元）        | 句子级文本                |

**核心冲突**：TTS 需要**完整的句子**来合成自然的语音，但 LLM 输出的是**零散的 Token**。

### 没有协调器会怎样？

#### 场景：用户问"北京今天天气怎么样？"

**❌ 没有协调器（直接 Token → TTS）**

```
LLM Token 流：北京→ 今天→ 天气→ 怎么→ 样 ...
           ↓
TTS 调用："北京" → 合成 → 播放 [不自然]
          "今天" → 合成 → 播放 [不自然]
          "天气" → 合成 → 播放 [不自然]
          ...
```

**问题**：

1. **语义断裂**：单个词合成听起来机械
2. **资源浪费**：频繁的 TTS API 调用，成本高
3. **延迟累积**：每次 API 调用都有网络延迟
4. **体验差**：断断续续，像打字机

---

**✅ 有协调器（Token 聚合 → 句子 → TTS）**

```
LLM Token 流：北京 今天 -> 天气 怎么 -> 样 -> ？
           ↓
协调器缓冲区：聚合 token，检测句子结束
           ↓
触发 TTS："北京今天天气怎么样？" → 合成 → 播放 [自然]
```

**优点**：

1. **语义完整**：按完整句子合成，语调自然
2. **减少调用**：一个句子 = 一次 TTS 调用（或一个流式会话）
3. **并行处理**：边合成边播放，隐藏延迟
4. **流畅体验**：像真人说话一样

### 协调器的核心功能

协调器解决三个关键问题：

#### 1. 句子边界检测

```go
// 协调器维护缓冲区
buffer := ""

// 接收 Token
buffer += "今天"  // "今天"
buffer += "天气"  // "今天天气"
buffer += "怎么"  // "今天天气怎么"
buffer += "样"   // "今天天气怎么样"
buffer += "？"   // "今天天气怎么样？" ← 检测到句子结束

// 触发 TTS
ttsStream.Send(buffer)  // 发送完整句子
buffer = ""  // 清空缓冲区
```

**如果没检测到？**

- 发送"今天天气"到 TTS → 语义不完整，语调奇怪
- 等待太久 → 用户感觉卡顿

#### 2. 流量控制（背压）

```
场景：LLM 生成快，TTS 合成慢

没有协调器：
  LLM: Token1 → Token2 → Token3 → ...（积压）
  TTS: 处理中...（不堪重负）
  
有协调器：
  LLM: Token1 → Token2 → Token3 → ...
  协调器：缓冲区满了，暂停接收或丢弃旧 token
  TTS: 按自己的节奏处理
```

**目的**：防止内存溢出，确保系统稳定。

#### 3. 会话状态管理

流式涉及多个阶段，需要协调状态：

```
流式会话状态机：

ASR 识别中 ──► ASR 完成 ──► LLM 生成中 ──► TTS 合成中 ──► 完成
     │           │            │             │
     ▼           ▼            ▼             ▼
  接收音频      触发 LLM     聚合          发送音频分块
                Token
```

**协调器维护**：

- 当前阶段
- 缓冲区内容
- TTS 流式会话对象
- 音频分块发送队列

### 流式 vs 非流式的关键区别

| 维度                    | 非流式             | 流式                                 |
| ----------------------- | ------------------ | ------------------------------------ |
| **数据形式**            | 完整文本           | Token 流                             |
| **处理时机**            | 等待全部完成       | 生成时处理                           |
| **延迟**                | 高（等待全部）     | 低（句子级）                         |
| **复杂度**              | 低                 | 高（需要缓冲、聚合、流量控制）       |

### 类比

**非流式** = 写信：等整封信写完再发送

**没有协调器的流式** = 发电报：逐字发送，接收者逐字阅读（体验差）

**有协调器的流式** = 实时口译：译员听一句翻译一句，听众听到完整句子（体验好）

协调器就是那位**实时译员**，负责将零散的 token"组装"成完整句子，然后交给 TTS"朗读"。

---

## 配置设计

不同厂商的 ASR/TTS 接口差异很大：

- **OpenAI**：HTTP REST + multipart/form-data
- **豆包**：WebSocket + 自定义二进制协议（4字节头部 + gzip）
- **Azure**：WebSocket + SSML 标记语言
- **FunASR**：WebSocket + JSON，支持多种模式（online/offline/2pass）
- **FishSpeech**：HTTP + JSON，支持声音克隆

**因此，策略是**：

1. **每个提供商独立实现**，不强制通用接口抽象
2. **配置与提供商 1:1 映射**，每个提供商有自己的配置结构
3. **服务层只做简单路由**，提供商实现保持独立完整
4. **后期如有需要再提取共性**，不预设抽象

```json
{
  "channels": {
    "websocket": {
      "enabled": true,
      "path": "/ws",
      "audio": {
        "enable_input": true,    
        "enable_output": true,    
        "input_format": "opus",
        "output_format": "opus",  
        "streaming": true,
        "asr_provider": "doubao",
        "tts_provider": "doubao",
      }
    },

  "audio": {
    "enabled": true,
    
    "asr": {
      "openai": {
        "api_key": "sk-xxx",
        "base_url": "https://api.openai.com/v1",
        "model": "whisper-1",
        "language": "zh"
      },
      
      "doubao": {
        "ws_url": "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel",
        "appid": "your_appid",
        "access_token": "your_token",
        "cluster": "volcengine_streaming_asr",
        "resource_id": "volc.bigasr.sauc.duration"
      },
      
      "azure": {
        "region": "eastasia",
        "subscription_key": "your_key",
        "language": "zh-CN"
      },
      
      "funasr": {
        "endpoint": "ws://localhost:10095",
        "mode": "2pass",
        "chunk_size": [5, 10, 5]
      }
    },
    
    "tts": {
      "openai": {
        "api_key": "sk-xxx",
        "model": "tts-1",
        "voice": "alloy",
        "speed": 1.0,
        "response_format": "mp3"
      },
      
      "doubao": {
        "ws_url": "wss://openspeech.bytedance.com/api/v1/tts/ws_binary",
        "appid": "your_appid",
        "access_token": "your_token",
        "cluster": "volcano_tts",
        "voice": "zh_female_wanwanxiaohe_moon_bigtts",
        "speed_ratio": 1.0,
        "volume_ratio": 1.0,
        "pitch_ratio": 1.0
      },
      
      "azure": {
        "region": "eastasia",
        "subscription_key": "your_key",
        "voice_name": "zh-CN-XiaoxiaoNeural",
        "style": "friendly"
      },
      
      "fishspeech": {
        "endpoint": "http://localhost:8000",
        "reference_id": "default",
        "format": "mp3",
        "streaming": true,
        "max_new_tokens": 1024
      }
    }
  },
  
  "channels": {
    "websocket": {
      "enabled": true,
      "audio": {
        "enable_input": true,
        "enable_output": true,
        "input_format": "opus",
        "output_format": "opus",
        "streaming": true
      }
    }
  }
}
```

### 配置说明

**为什么要保留厂商特定字段？**

| 提供商       | 特定字段                              | 原因                              |
|--------------|--------------------------------------|----------------------------------|
| 豆包 ASR     | `resource_id`, `cluster`             | 火山引擎特有的资源标识符          |
| 豆包 TTS     | `speed_ratio`, `volume_ratio`, `pitch_ratio` | 豆包支持精细的声音控制            |
| Azure TTS    | `style`, `rate`, `pitch`             | SSML 支持的声音风格控制           |
| FishSpeech   | `reference_id`, `max_new_tokens`     | 声音克隆和生成参数                |
| FunASR       | `mode`, `chunk_size`                 | 独特的运行模式配置                |

如果强制使用通用字段，这些特性将会丢失或使配置难以理解。
