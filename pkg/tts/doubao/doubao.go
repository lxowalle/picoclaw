// Package doubao provides Doubao (Volcano Engine) TTS implementation.
package doubao

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tts"
)

// Provider implements the tts.Provider interface for Doubao
type Provider struct {
	appID     string
	token     string
	voice     string
	apiBase   string
	outFormat tts.Format
}

// Config holds the configuration for Doubao TTS
type Config struct {
	AppID   string
	Token   string
	Voice   string
	APIBase string
	Format  string
}

// New creates a new Doubao TTS provider
func New(config Config) *Provider {
	apiBase := config.APIBase
	if apiBase == "" {
		apiBase = "https://openspeech.bytedance.com/api/v1"
	}

	format := tts.FormatOpus
	if config.Format != "" {
		format = tts.Format(config.Format)
	}

	return &Provider{
		appID:     config.AppID,
		token:     config.Token,
		voice:     config.Voice,
		apiBase:   apiBase,
		outFormat: format,
	}
}

// Name returns the provider name
func (p *Provider) Name() string {
	return "doubao"
}

// OutputFormat returns the default output format
func (p *Provider) OutputFormat() tts.Format {
	return p.outFormat
}

// Capabilities returns the provider capabilities
func (p *Provider) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{
		SupportsStreaming: true,
		MaxTextLength:     5000,
		OutputFormats:     []tts.Format{tts.FormatOpus, tts.FormatPCM, tts.FormatMP3},
	}
}

// Synthesize performs non-streaming text-to-speech
func (p *Provider) Synthesize(ctx context.Context, text string, voiceID string) (
	audio []byte, format tts.Format, err error) {
	if p.token == "" {
		return nil, "", fmt.Errorf("doubao TTS: token not configured")
	}

	voice := voiceID
	if voice == "" {
		voice = p.voice
	}
	if voice == "" {
		voice = "zh_female_wanwanxiaohe_moon_bigtts"
	}

	// TODO: Implement actual Doubao TTS API call
	// This is a placeholder implementation
	// In production, this should:
	// 1. Send text to Doubao TTS API
	// 2. Receive audio data
	// 3. Return audio bytes and format

	return nil, "", fmt.Errorf("doubao TTS: not fully implemented")
}

// SynthesizeStream performs streaming text-to-speech
func (p *Provider) SynthesizeStream(ctx context.Context, text string, voiceID string) (
	<-chan tts.TTSResult, error) {
	if p.token == "" {
		return nil, fmt.Errorf("doubao TTS: token not configured")
	}

	voice := voiceID
	if voice == "" {
		voice = p.voice
	}
	if voice == "" {
		voice = "zh_female_wanwanxiaohe_moon_bigtts"
	}

	// TODO: Implement actual Doubao streaming TTS
	// This is a placeholder implementation
	// In production, this should:
	// 1. Establish WebSocket connection to Doubao streaming TTS
	// 2. Send text
	// 3. Stream back audio chunks

	results := make(chan tts.TTSResult)

	go func() {
		defer close(results)
		// Placeholder implementation
		select {
		case <-ctx.Done():
			return
		default:
			// Send placeholder result
			results <- tts.TTSResult{
				Audio:     []byte{},
				Format:    p.outFormat,
				IsFinal:   true,
				Timestamp: 0,
			}
		}
	}()

	return results, nil
}

// DefaultConfig returns the default configuration
func DefaultConfig() Config {
	return Config{
		Voice:   "zh_female_wanwanxiaohe_moon_bigtts",
		Format:  "opus",
		APIBase: "https://openspeech.bytedance.com/api/v1",
	}
}

// NewFromConfig creates a new Doubao TTS provider from application config
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

	// Get format from provider config or internal
	format := providerCfg.Format
	if format == "" {
		format = getString("format", "opus")
	}

	return New(Config{
		AppID:   getString("appid", ""),
		Token:   getString("token", ""),
		Voice:   getString("voice", "zh_female_wanwanxiaohe_moon_bigtts"),
		APIBase: getString("api_base", "https://openspeech.bytedance.com/api/v1"),
		Format:  format,
	})
}

func init() {
	// 注册 Doubao Provider 工厂函数
	// Channel 可以通过 tts.NewProviderFromConfig("doubao", cfg) 创建 Provider
	tts.RegisterFactory("doubao", func(cfg *config.Config) tts.Provider {
		return NewFromConfig(cfg)
	})
}
