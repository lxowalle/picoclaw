// Package factory provides factory functions for creating ASR and TTS services.
// This package exists to avoid circular imports between tts/asr packages and their service packages.
package factory

import (
	"fmt"

	"github.com/sipeed/picoclaw/pkg/asr"
	asrService "github.com/sipeed/picoclaw/pkg/asr/service"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tts"
	ttsService "github.com/sipeed/picoclaw/pkg/tts/service"
)

// NewASRService creates an ASR service from provider name and config.
// This is the recommended way for channels to create ASR services.
//
// Example:
//   svc, err := factory.NewASRService("doubao", cfg)
//   if err != nil {
//       log.Printf("Failed to create ASR service: %v", err)
//   }
func NewASRService(providerName string, cfg *config.Config) (*asrService.Service, error) {
	if providerName == "" {
		return nil, fmt.Errorf("provider name is required")
	}

	provider, err := asr.NewProviderFromConfig(providerName, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ASR provider %s: %w", providerName, err)
	}

	return asrService.NewService(provider), nil
}

// NewTTSService creates a TTS service from provider name and config.
// This is the recommended way for channels to create TTS services.
//
// Example:
//   svc, err := factory.NewTTSService("doubao", cfg)
//   if err != nil {
//       log.Printf("Failed to create TTS service: %v", err)
//   }
func NewTTSService(providerName string, cfg *config.Config) (*ttsService.Service, error) {
	if providerName == "" {
		return nil, fmt.Errorf("provider name is required")
	}

	provider, err := tts.NewProviderFromConfig(providerName, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create TTS provider %s: %w", providerName, err)
	}

	return ttsService.NewService(provider), nil
}
