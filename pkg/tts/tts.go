// Package tts provides Text-to-Speech (TTS) provider interfaces and registry.
package tts

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Format represents audio format types (same as ASR)
type Format string

const (
	FormatPCM  Format = "pcm"
	FormatWAV  Format = "wav"
	FormatOpus Format = "opus"
	FormatMP3  Format = "mp3"
)

// TTSResult represents the result of TTS synthesis
type TTSResult struct {
	Audio     []byte
	Format    Format
	IsFinal   bool
	Timestamp int64
}

// TTSCapabilities describes the capabilities of a TTS provider
type TTSCapabilities struct {
	SupportsStreaming bool
	MaxTextLength     int
	OutputFormats     []Format
}

// Provider is the interface for TTS providers
type Provider interface {
	// Synthesize performs non-streaming text-to-speech
	Synthesize(ctx context.Context, text string, voiceID string) (
		audio []byte, format Format, err error)

	// SynthesizeStream performs streaming text-to-speech (optional)
	SynthesizeStream(ctx context.Context, text string, voiceID string) (
		<-chan TTSResult, error)

	// Name returns the provider name
	Name() string

	// OutputFormat returns the default output format
	OutputFormat() Format

	// Capabilities returns the provider capabilities
	Capabilities() TTSCapabilities
}

// ProviderConfig holds configuration for a TTS provider
type ProviderConfig struct {
	Enabled          bool   `json:"enabled"`
	SupportStreaming bool   `json:"support_streaming"`
	Format           string `json:"format"`
	APIKey           string `json:"api_key,omitempty"`
	AppID            string `json:"appid,omitempty"`
	Token            string `json:"token,omitempty"`
	Voice            string `json:"voice,omitempty"`
	Region           string `json:"region,omitempty"`
}

// providers registry
var providers = make(map[string]Provider)

// providerFactories 存储 Provider 工厂函数
var providerFactories = make(map[string]func(*config.Config) Provider)

// Register registers a TTS provider
func Register(name string, provider Provider) {
	providers[name] = provider
}

// RegisterFactory 注册 Provider 工厂函数
// Provider 包在 init() 中调用此函数注册自己
func RegisterFactory(name string, factory func(*config.Config) Provider) {
	providerFactories[name] = factory
}

// Get retrieves a TTS provider by name
func Get(name string) (Provider, bool) {
	p, ok := providers[name]
	return p, ok
}

// List returns all registered provider names
func List() []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	return names
}

// NewProviderFromConfig 根据 provider 名称和配置创建 Provider
// Channel 调用此函数获取 Provider，然后用 service.NewService() 创建 Service
// 添加新 Provider 时，只需在 Provider 包的 init() 中注册工厂函数
func NewProviderFromConfig(providerName string, cfg *config.Config) (Provider, error) {
	// 1. 尝试从全局注册表获取（通过 build tags 编译进来的 Provider）
	if provider, ok := Get(providerName); ok {
		return provider, nil
	}

	// 2. 从工厂函数创建 Provider
	if factory, ok := providerFactories[providerName]; ok {
		provider := factory(cfg)
		if provider == nil {
			return nil, fmt.Errorf("failed to create TTS provider: %s", providerName)
		}
		return provider, nil
	}

	return nil, fmt.Errorf("unknown TTS provider: %s", providerName)
}
