# PicoClaw Audio (TTS/ASR) Architecture Design

## Design Principles

Add flexible voice interaction capabilities to PicoClaw, supporting:

- **Text-only chat**: Existing text interaction workflow remains unchanged
- **ASR only**: Voice input → Text output (for scenarios without voice output support)
- **TTS only**: Text input → Voice output (for scenarios without voice input support)
- **Full voice**: Voice input → Voice output

**Core principle**: Channel makes autonomous decisions, Agent stays transparent, no forced voice capabilities.

---

## Directory Structure

```
picoclaw/
├── pkg/
│   ├── asr/
│   │   ├── service.go            # ASR Service (routing + management)
│   │   └── providers/            # Independent provider implementations
│   │       ├── openai/
│   │       │   ├── provider.go   # HTTP REST implementation
│   │       │   └── config.go     # OpenAI-specific config
│   │       ├── doubao/
│   │       │   ├── provider.go   # WebSocket binary implementation
│   │       │   └── config.go     # Doubao-specific config
│   │       ├── azure/
│   │       │   ├── provider.go   # WebSocket JSON implementation
│   │       │   └── config.go     # Azure-specific config
│   │       └── funasr/
│   │           ├── provider.go   # WebSocket JSON implementation
│   │           └── config.go     # FunASR-specific config
│   │
│   ├── tts/
│   │   ├── service.go            # TTS Service (routing + management)
│   │   └── providers/
│   │       ├── openai/
│   │       │   ├── provider.go   # HTTP REST implementation
│   │       │   └── config.go
│   │       ├── doubao/
│   │       │   ├── provider.go   # WebSocket binary implementation
│   │       │   └── config.go
│   │       ├── azure/
│   │       │   ├── provider.go   # WebSocket SSML implementation
│   │       │   └── config.go
│   │       └── fishspeech/
│   │           ├── provider.go   # HTTP REST implementation
│   │           └── config.go
│   │
│   ├── audio/
│   │   ├── coordinator.go        # Streaming coordinator (optional)
│   │   └── session.go            # Session management
│   │
│   └── channels/
│       └── websocket/
│           └── audio.go          # Channel integration
│
└── config/
    └── config.example.json       # Complete configuration example
```

---

## Four Interaction Modes

### Mode 1: Text-only (Existing workflow, unchanged)

```
User          Channel         Agent
 │               │              │
 │──text────────►│              │
 │               │──llm.req────►│
 │               │              │
 │               │◄─llm.resp────│
 │               │              │
 │◄──text────────│              │
```

**Use case**: All channels, default behavior

---

### Mode 2: ASR only (Voice input, text output)

```
User          Channel        ASR Service      Agent
 │               │               │              │
 │──voice────────►│               │              │
 │               │               │              │
 │               │──audio.input─►│              │
 │               │               │              │
 │               │◄─asr.result───│              │
 │               │               │              │
 │               │───────────────┴─────────────►│
 │               │                              │
 │               │◄─────────────────────────────│
 │               │                              │
 │◄──text────────│                              │
```

**Use cases**:

- Customer service (user speaks, agent reads text to reply)
- Mobile data saving (download text only)

**Configuration**: `EnableInput: true, EnableOutput: false`

---

### Mode 3: TTS only (Text input, voice output)

```
User          Channel        TTS Service      Agent
 │               │               │              │
 │──text────────►│               │              │
 │               │               │              │
 │               │───────────────┴─────────────►│
 │               │                              │
 │               │◄─────────────────────────────│
 │               │                              │
 │               │──tts.request────►│          │
 │               │               │            │
 │               │◄─audio.output───│          │
 │               │               │            │
 │◄──voice────────│               │            │
```

**Configuration**: `EnableInput: false, EnableOutput: true`

---

### Mode 4: Full voice (Voice input, voice output)

#### 4.1 Non-streaming (Simple scenarios)

```
User          Channel       ASR Service     Agent      TTS Service
 │               │               │            │             │
 │──voice────────►│               │            │             │
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
 │◄──voice────────│                            │             │
```

#### 4.2 Streaming (Low-latency scenarios)

```
User          Channel       ASR Service   Coordinator    Agent    TTS Service
 │               │               │            │            │          │
 │──voice────────►│               │            │            │          │
 │               │               │            │            │          │
 │               │──audio.input─►│            │            │          │
 │               │               │            │            │          │
 │               │               │─asr.result►│            │          │
 │               │               │            │            │          │
 │               │               │            │──llm.req──►│          │
 │               │               │            │            │          │
 │◄──real-time────│◄──────────────│◄───────────│◄─Token────│          │
 │    text       │               │            │            │          │
 │               │               │            │            │          │
 │               │               │            │aggregate   │          │
 │               │               │            │Token       │          │
 │               │               │            │            │          │
 │               │               │            │─tts.req───-----------►│
 │               │               │            │            │          │
 │               │               │            │◄─audio.chunk──────────│
 │               │               │            │            │          │
 │               │◄──────────────│◄───────────│◄─audio.chunk         │
 │               │               │            │            │          │
 │◄──audio───────│               │            │            │          │
 │    chunk      │               │            │            │          │
 │               │◄──────────────│◄───────────│◄─audio.chunk         │
 │               │               │            │            │          │
 │◄──audio───────│               │            │            │          │
 │    chunk      │               │            │            │          │
```

**Use cases**:

- Voice assistants (Siri/Alexa-like interaction)
- Real-time translation
- Intelligent customer service calls

**Configuration**: `EnableInput: true, EnableOutput: true`

**Streaming notes**:

- Non-streaming: Channel calls ASR → waits for result → calls Agent → calls TTS
- Streaming: Requires Coordinator to coordinate ASR streaming recognition + LLM Token stream + TTS chunked synthesis

## Module Responsibilities

### ASR Service

```go
type Service struct {
    primary  Provider
    fallback Provider
}

// Non-streaming recognition
func (s *Service) Transcribe(ctx, audio, format) (text, error)

// Streaming recognition (returns channel for real-time results)
func (s *Service) TranscribeStream(ctx, config) (<-chan Result, error)
```

**Responsibilities**:

- Manage ASR providers (primary/backup switching)
- Handle speech-to-text conversion
- Return partial/final results in real-time for streaming mode

### TTS Service

```go
type Service struct {
    provider Provider
}

// Non-streaming synthesis
func (s *Service) Synthesize(ctx, text, voiceID) (audio, error)

// Streaming synthesis
func (s *Service) SynthesizeStream(ctx, config) (Stream, error)
```

**Responsibilities**:

- Manage TTS providers
- Handle text-to-speech conversion
- Return audio chunks by sentence in streaming mode

### Audio Coordinator (Streaming only)

```go
type Coordinator struct {
    asrService ASRService
    ttsService TTSService
    sessions   map[string]*Session
}

type Session struct {
    ID          string
    SentenceBuf *SentenceBuffer  // Sentence buffer
    IsStreaming bool
}
```

**Responsibilities**:

- **Used only in streaming mode**
- Aggregate LLM Token stream, trigger TTS by sentence
- Manage streaming session state
- Provide buffering and flow control

---

## Why Streaming Needs a Coordinator

### The Essence of Streaming

The goal of streaming is to achieve **input-and-output-simultaneously** low-latency experience, but this introduces data granularity mismatch:

| Stage         | Output Granularity      | Input Requirements       |
| ------------- | ----------------------- | ------------------------ |
| **ASR Streaming** | Word/phrase (real-time) | Audio chunks             |
| **LLM Streaming** | Tokens (one-by-one)     | Complete prompt          |
| **TTS Streaming** | Sentences (complete semantic units) | Sentence-level text |

**Key conflict**: TTS needs **complete sentences** to synthesize natural speech, but LLM outputs **scattered Tokens**.

### What Happens Without a Coordinator?

#### Scenario: User asks "What's the weather like in Beijing today?"

**❌ Without Coordinator (direct Token → TTS)**

```
LLM Token stream: What's→  the weather→  in → Beijing→  today ...
          ↓
TTS Call: "What's" → synthesize → play [unnatural]
          "the weather" → synthesize → play [unnatural]
          "'in" → synthesize → play [unnatural]
          ...
```

**Problems**:

1. **Semantic breaks**: Single-word synthesis sounds robotic
2. **Resource waste**: Frequent TTS API calls, high cost
3. **Latency accumulation**: Network latency on each API call
4. **Poor UX**: Choppy, like a typewriter

---

**✅ With Coordinator (Token aggregation → sentence → TTS)**

```
LLM Token stream: What's the -> weather in -> Beijing -> today ?
          ↓
Coordinator buffer: Aggregate tokens, detect sentence end
          ↓
Trigger TTS: "What's the weather in Beijing today?" → synthesize → play [natural]
```

**Advantages**:

1. **Semantic completeness**: Synthesize by complete sentences, natural intonation
2. **Reduced calls**: One sentence = one TTS call (or one streaming session)
3. **Parallel processing**: Synthesize while playing, hiding latency
4. **Smooth UX**: Like a real person speaking

### Core Functions of the Coordinator

The coordinator solves three key problems:

#### 1. Sentence Boundary Detection

```go
// Coordinator maintains buffer
buffer := ""

// Receive Token
buffer += "What"  // "What"
buffer += "is"  // "What is"
buffer += "'this"  // "What is this"
buffer += "?"   // "What is this?" ← detected sentence end

// Trigger TTS
ttsStream.Send(buffer)  // Send complete sentence
buffer = ""  // Clear buffer
```

**What if not detected?**

- Send "What'" to TTS → Incomplete semantics, strange intonation
- Wait too long → User feels laggy

#### 2. Flow Control (Backpressure)

```
Scenario: LLM generates fast, TTS synthesizes slow

Without Coordinator:
  LLM: Token1 → Token2 → Token3 → ... (backlog)
  TTS: Processing... (overwhelmed)
  
With Coordinator:
  LLM: Token1 → Token2 → Token3 → ...
  Coordinator: Buffer full, pause receiving or drop old tokens
  TTS: Process at its own pace
```

**Purpose**: Prevent memory overflow, ensure system stability.

#### 3. Session State Management

Streaming involves multiple stages, need to coordinate states:

```
Streaming session state machine:

ASR Recognizing ──► ASR Complete ──► LLM Generating ──► TTS Synthesizing ──► Complete
     │           │            │             │
     ▼           ▼            ▼             ▼
  Receive Audio   Trigger LLM   Aggregate     Send audio chunks
                  Tokens
```

**Coordinator maintains**:

- Current stage
- Buffer content
- TTS streaming session object
- Audio chunk send queue

### Streaming vs Non-streaming Key Differences

| Dimension               | Non-streaming      | Streaming                          |
| ----------------------- | ------------------ | ---------------------------------- |
| **Data form**           | Complete text      | Token stream                       |
| **Processing timing**   | Wait for all done  | Process while generating           |
| **Latency**             | High (wait for all) | Low (sentence-level)               |
| **Complexity**          | Low                | High (needs buffering, aggregation, flow control) |

### Analogy

**Non-streaming** = Writing a letter: Wait for the whole letter to finish before sending

**Streaming without Coordinator** = Sending telegram: Send character by character, receiver reads word by word (poor experience)

**Streaming with Coordinator** = Real-time interpretation: Interpreter listens to a sentence, translates a sentence, listener hears complete sentences (good experience)

The Coordinator is that **real-time interpreter**, responsible for "assembling" scattered tokens into complete sentences, then handing to TTS to "read aloud".

---

## Configuration Design

ASR/TTS interfaces vary greatly between vendors:

- **OpenAI**: HTTP REST + multipart/form-data
- **Doubao**: WebSocket + custom binary protocol (4-byte header + gzip)
- **Azure**: WebSocket + SSML markup language
- **FunASR**: WebSocket + JSON, supports multiple modes (online/offline/2pass)
- **FishSpeech**: HTTP + JSON, supports voice cloning

**Therefore, the strategy is**:

1. **Each Provider implements independently**, no forced generic interface abstraction
2. **Configuration maps 1:1 to Provider**, each Provider has its own config structure
3. **Service layer only does simple routing**, Provider implementations remain independent and complete
4. **Extract commonality later if needed**, don't preset abstractions

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

### Configuration Notes

**Why keep vendor-specific fields?**

| Provider | Specific Fields | Reason |
|----------|----------------|--------|
| Doubao ASR | `resource_id`, `cluster` | Volcano Engine-specific resource identifiers |
| Doubao TTS | `speed_ratio`, `volume_ratio`, `pitch_ratio` | Doubao supports fine-grained voice control |
| Azure TTS | `style`, `rate`, `pitch` | SSML-supported voice style control |
| FishSpeech | `reference_id`, `max_new_tokens` | Voice cloning and generation parameters |
| FunASR | `mode`, `chunk_size` | Unique operating mode configuration |

If forced into generic fields, these features would be lost or make config hard to understand.

---
