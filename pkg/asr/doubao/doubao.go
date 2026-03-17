// Package doubao provides Doubao (Volcano Engine) ASR implementation.
package doubao

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/asr"
	"github.com/sipeed/picoclaw/pkg/config"
)

// Provider implements the asr.Provider interface for Doubao
type Provider struct {
	appID   string
	token   string
	cluster string
	apiBase string
}

// Config holds the configuration for Doubao ASR
type Config struct {
	AppID   string
	Token   string
	Cluster string
	APIBase string
}

// New creates a new Doubao ASR provider
func New(config Config) *Provider {
	apiBase := config.APIBase
	if apiBase == "" {
		apiBase = "wss://openspeech.bytedance.com/api/v1"
	}
	return &Provider{
		appID:   config.AppID,
		token:   config.Token,
		cluster: config.Cluster,
		apiBase: apiBase,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "doubao"
}

// Capabilities returns the provider capabilities
func (p *Provider) Capabilities() asr.ASRCapabilities {
	return asr.ASRCapabilities{
		SupportsStreaming: true,
		SupportsFormat:    []asr.Format{asr.FormatPCM, asr.FormatOpus},
		MaxAudioDuration:  300, // 5 minutes
	}
}

// Transcribe performs non-streaming speech recognition
func (p *Provider) Transcribe(ctx context.Context, audio []byte, format asr.Format) (string, error) {
	if p.token == "" {
		return "", fmt.Errorf("doubao ASR: token not configured")
	}

	// TODO: Implement actual Doubao ASR API call
	// This is a placeholder implementation
	// In production, this should:
	// 1. Convert audio to supported format if needed
	// 2. Send request to Doubao ASR API
	// 3. Parse response and return text

	return "", fmt.Errorf("doubao ASR: not fully implemented")
}

// TranscribeStream performs streaming speech recognition
func (p *Provider) TranscribeStream(ctx context.Context, audioStream <-chan []byte, format asr.Format) (
	<-chan asr.TranscriptionResult, error) {
	if p.token == "" {
		return nil, fmt.Errorf("doubao ASR: token not configured")
	}

	// TODO: Implement actual Doubao streaming ASR
	// This is a placeholder implementation
	// In production, this should:
	// 1. Establish WebSocket connection to Doubao streaming ASR
	// 2. Send audio chunks as they arrive
	// 3. Stream back transcription results

	results := make(chan asr.TranscriptionResult)

	go func() {
		defer close(results)
		// Placeholder: just drain the audio stream
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-audioStream:
				if !ok {
					return
				}
				// Process audio chunk and send results
			}
		}
	}()

	return results, nil
}

// DefaultConfig returns the default configuration
func DefaultConfig() Config {
	return Config{
		Cluster: "volcengine_streaming_asr",
		APIBase: "wss://openspeech.bytedance.com/api/v1",
	}
}

// NewFromConfig creates a new Doubao ASR provider from application config
// It parses the internal configuration from cfg.Audio.Providers["doubao"].Internal
func NewFromConfig(cfg *config.Config) *Provider {
	providerCfg, ok := cfg.Audio.Providers["doubao"]
	if !ok {
		return New(DefaultConfig())
	}

	// Parse internal configuration
	internal := providerCfg.Internal
	if internal == nil {
		internal = make(map[string]interface{})
	}

	// Helper function to get string value from internal config
	getString := func(key string, defaultValue string) string {
		if val, ok := internal[key].(string); ok && val != "" {
			return val
		}
		return defaultValue
	}

	return New(Config{
		AppID:   getString("appid", ""),
		Token:   getString("token", ""),
		Cluster: getString("cluster", "volcengine_streaming_asr"),
		APIBase: getString("api_base", "wss://openspeech.bytedance.com/api/v1"),
	})
}

func init() {
	// 注册 Doubao Provider 工厂函数
	// Channel 可以通过 asr.NewProviderFromConfig("doubao", cfg) 创建 Provider
	asr.RegisterFactory("doubao", func(cfg *config.Config) asr.Provider {
		return NewFromConfig(cfg)
	})
}
