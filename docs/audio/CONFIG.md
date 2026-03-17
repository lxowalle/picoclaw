# Pico Channel 音频功能配置

本文档介绍如何配置 Pico Channel 的 TTS（文本转语音）和 ASR（语音识别）功能。

## 功能概述

Pico Channel 现在支持语音交互功能：
- **ASR (Automatic Speech Recognition)**：将客户端发送的音频转换为文本
- **TTS (Text-to-Speech)**：将 AI 响应的文本转换为音频并发送给客户端

## 配置说明

在 `config.json` 中添加以下配置：

```json
{
  "channels": {
    "pico": {
      "enabled": true,
      "token": "your-secure-token",
      "audio": {
        "enabled": true,
        "streaming": false,
        "asr_provider": "doubao",
        "tts_provider": "doubao"
      }
    }
  },
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

## 配置参数说明

### channels.pico.audio

| 参数 | 类型 | 说明 |
|------|------|------|
| `enabled` | boolean | 是否启用音频功能 |
| `streaming` | boolean | 是否使用流式模式（预留） |
| `asr_provider` | string | ASR 提供商名称，如 "doubao" |
| `tts_provider` | string | TTS 提供商名称，如 "doubao" |

### audio.providers.{provider_name}

通用字段（所有 Provider 都支持）：

| 参数 | 类型 | 说明 |
|------|------|------|
| `enabled` | boolean | 是否启用此 Provider |
| `format` | string | 音频格式（opus, pcm, mp3, wav） |
| `support_streaming` | boolean | 是否支持流式处理 |
| `internal` | object | Provider 特定的配置，由 Provider 自己解析 |

#### Doubao Provider 的 internal 字段

| 参数 | 类型 | 说明 |
|------|------|------|
| `appid` | string | 豆包应用的 AppID |
| `token` | string | 豆包 API Token |
| `cluster` | string | ASR 集群标识（如 "volcengine_streaming_asr"） |
| `voice` | string | TTS 语音标识（如 "zh_female_wanwanxiaohe_moon_bigtts"） |
| `api_base` | string | API 基础 URL（可选） |

#### 多 Provider 配置示例

```json
{
  "audio": {
    "providers": {
      "doubao_prod": {
        "enabled": true,
        "format": "opus",
        "internal": {
          "appid": "prod-app-id",
          "token": "prod-token"
        }
      },
      "doubao_test": {
        "enabled": false,
        "format": "pcm",
        "internal": {
          "appid": "test-app-id",
          "token": "test-token"
        }
      }
    }
  }
}
```

## 构建标签

使用以下构建标签来启用不同的 Provider：

```bash
# 启用 Doubao ASR 和 TTS
go build -tags "asr_doubao tts_doubao" ./cmd/picoclaw

# 启用所有 ASR 和 TTS
go build -tags "asr_all tts_all" ./cmd/picoclaw
```

## 协议消息类型

### 客户端发送

#### 音频数据块
```json
{
  "type": "audio.chunk",
  "id": "msg-123",
  "session_id": "session-456",
  "payload": {
    "data": "base64-encoded-audio-data",
    "format": "opus"
  }
}
```

#### 开始音频流
```json
{
  "type": "audio.start",
  "id": "msg-123",
  "session_id": "session-456"
}
```

#### 结束音频流
```json
{
  "type": "audio.stop",
  "id": "msg-123",
  "session_id": "session-456"
}
```

### 服务端发送

#### 音频数据
```json
{
  "type": "audio.data",
  "id": "msg-789",
  "session_id": "session-456",
  "timestamp": 1710000000000,
  "payload": {
    "data": "base64-encoded-audio-data",
    "format": "opus"
  }
}
```

## 实现状态

### 已实现
- [x] ASR Provider 接口和注册表
- [x] TTS Provider 接口和注册表
- [x] Doubao ASR Provider 骨架
- [x] Doubao TTS Provider 骨架
- [x] ASR Service 层
- [x] TTS Service 层
- [x] Pico Channel 配置支持
- [x] 音频消息协议
- [x] 基础音频处理流程

### 待实现
- [ ] Doubao ASR API 完整集成
- [ ] Doubao TTS API 完整集成
- [ ] 音频格式转换（Codec）
- [ ] 流式 ASR/TTS 支持
- [ ] 协调器（Coordinator）层
- [ ] 多 Provider 支持

## 音频格式处理

### 输入格式

Pico Channel 不再通过配置指定输入格式，而是在通信过程中从客户端消息中获取：

```json
{
  "type": "audio.chunk",
  "payload": {
    "data": "base64-audio-data",
    "format": "opus"  // 从消息中获取格式
  }
}
```

支持的输入格式：
- `opus` - 推荐使用，压缩率高
- `pcm` - 无损音频
- `wav` - WAV 容器格式

如果未指定格式，默认使用 `opus`。

### 输出格式

TTS 的输出格式由 Provider 配置决定（`audio.providers.doubao.format`）：

```json
{
  "audio": {
    "providers": {
      "doubao": {
        "format": "opus"
      }
    }
  }
}
```

服务端会在 `audio.data` 消息中返回实际使用的格式：

```json
{
  "type": "audio.data",
  "payload": {
    "data": "base64-audio-data",
    "format": "opus"
  }
}
```

## 注意事项

1. **API Key 安全**：请勿在代码中硬编码 Token，使用配置文件或环境变量
2. **音频格式**：目前推荐使用 Opus 格式，压缩率高且音质好
3. **延迟**：非流式模式会有一定延迟，流式模式将在后续版本支持
4. **错误处理**：ASR/TTS 失败时会返回错误消息，客户端应做好错误处理
5. **Provider 配置**：确保 `audio.providers` 中配置了对应 Provider 的参数，否则无法初始化
