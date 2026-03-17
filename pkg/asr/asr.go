// Package asr provides Automatic Speech Recognition (ASR) provider interfaces and registry.
package asr

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Format represents audio format types
type Format string

const (
	FormatPCM  Format = "pcm"
	FormatWAV  Format = "wav"
	FormatOpus Format = "opus"
	FormatMP3  Format = "mp3"
)

// TranscriptionResult represents the result of a transcription
type TranscriptionResult struct {
	Text      string
	IsFinal   bool
	Timestamp int64
}

// ASRCapabilities describes the capabilities of an ASR provider
type ASRCapabilities struct {
	SupportsStreaming bool
	SupportsFormat    []Format
	MaxAudioDuration  int // seconds
}

// Provider is the interface for ASR providers
type Provider interface {
	// Transcribe performs non-streaming speech recognition
	Transcribe(ctx context.Context, audio []byte, format Format) (string, error)

	// TranscribeStream performs streaming speech recognition (optional)
	TranscribeStream(ctx context.Context, audioStream <-chan []byte, format Format) (
		<-chan TranscriptionResult, error)

	// Name returns the provider name
	Name() string

	// Capabilities returns the provider capabilities
	Capabilities() ASRCapabilities
}

// ProviderConfig holds configuration for an ASR provider
type ProviderConfig struct {
	Enabled          bool   `json:"enabled"`
	SupportStreaming bool   `json:"support_streaming"`
	Format           string `json:"format"`
	APIKey           string `json:"api_key,omitempty"`
	AppID            string `json:"appid,omitempty"`
	Token            string `json:"token,omitempty"`
	Cluster          string `json:"cluster,omitempty"`
	Region           string `json:"region,omitempty"`
}

// providers registry
var providers = make(map[string]Provider)

// Register registers an ASR provider
func Register(name string, provider Provider) {
	providers[name] = provider
}

// Get retrieves an ASR provider by name
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

// providerFactories 存储 Provider 工厂函数
var providerFactories = make(map[string]func(*config.Config) Provider)

// RegisterFactory 注册 Provider 工厂函数
// Provider 包在 init() 中调用此函数注册自己
func RegisterFactory(name string, factory func(*config.Config) Provider) {
	providerFactories[name] = factory
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
			return nil, fmt.Errorf("failed to create ASR provider: %s", providerName)
		}
		return provider, nil
	}

	return nil, fmt.Errorf("unknown ASR provider: %s", providerName)
}
