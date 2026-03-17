// Package service provides TTS service layer with provider selection and failover.
package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tts"
	"github.com/sipeed/picoclaw/pkg/tts/selector"
)

// Service provides high-level TTS functionality with automatic provider selection and failover
type Service struct {
	selector    *selector.TTSSelector
	mu          sync.RWMutex
	healthCheck *time.Ticker
	stopCh      chan struct{}
}

// NewServiceFromConfig creates a TTS service from configuration.
// It creates a selector with all configured providers and starts health monitoring.
func NewServiceFromConfig(cfg *config.Config) (*Service, error) {
	sel, err := selector.NewTTSSelector(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create TTS selector: %w", err)
	}

	svc := &Service{
		selector: sel,
		stopCh:   make(chan struct{}),
	}

	// Start health check goroutine
	svc.startHealthCheck()

	return svc, nil
}

// NewService creates a TTS service with a single provider (for backward compatibility)
func NewService(provider tts.Provider) *Service {
	// Create a selector with single provider
	cfg := &config.Config{
		Audio: config.AudioConfig{
			Enabled: true,
			TTS: config.AudioTTSConfig{
				Enabled:  true,
				Provider: provider.Name(),
			},
			Providers: map[string]config.AudioProviderConfig{
				provider.Name(): {Enabled: true},
			},
		},
	}

	sel, _ := selector.NewTTSSelector(cfg)
	if sel == nil {
		sel = &selector.TTSSelector{}
	}

	return &Service{
		selector: sel,
		stopCh:   make(chan struct{}),
	}
}

// Synthesize performs text-to-speech with automatic provider failover
func (s *Service) Synthesize(ctx context.Context, text string, voiceID string, targetFormat tts.Format) ([]byte, error) {
	// Try providers in priority order
	providers := s.selector.GetProviders()

	for range providers {
		provider, name, err := s.selector.GetProvider()
		if err != nil {
			continue
		}

		// Attempt synthesis
		audio, sourceFormat, err := provider.Synthesize(ctx, text, voiceID)
		if err != nil {
			logger.WarnCF("tts-service", "Provider failed, marking for failover", map[string]any{
				"provider": name,
				"error":    err.Error(),
			})
			s.selector.MarkFailed(name, err)
			continue
		}

		// Success - reset provider status
		s.selector.Reset(name)

		// Handle format conversion if needed
		if sourceFormat != targetFormat {
			logger.DebugCF("tts-service", "Format conversion needed", map[string]any{
				"from": sourceFormat,
				"to":   targetFormat,
			})
			// TODO: Implement format transcoding
		}

		return audio, nil
	}

	return nil, fmt.Errorf("all TTS providers failed")
}

// SynthesizeStream performs streaming text-to-speech with failover support
func (s *Service) SynthesizeStream(ctx context.Context, text string, voiceID string, targetFormat tts.Format) (
	<-chan tts.TTSResult, error) {

	provider, name, err := s.selector.GetProvider()
	if err != nil {
		return nil, fmt.Errorf("no TTS provider available: %w", err)
	}

	// Note: Streaming failover is complex and typically requires session recovery
	results, err := provider.SynthesizeStream(ctx, text, voiceID)
	if err != nil {
		s.selector.MarkFailed(name, err)
		return nil, fmt.Errorf("streaming synthesis failed: %w", err)
	}

	return results, nil
}

// Name returns the current provider name
func (s *Service) Name() string {
	_, name, _ := s.selector.GetProvider()
	return name
}

// OutputFormat returns the output format of the primary provider
func (s *Service) OutputFormat() tts.Format {
	return s.selector.OutputFormat()
}

// Capabilities returns the capabilities of the current provider
func (s *Service) Capabilities() tts.TTSCapabilities {
	provider, _, _ := s.selector.GetProvider()
	if provider == nil {
		return tts.TTSCapabilities{}
	}
	return provider.Capabilities()
}

// startHealthCheck starts periodic health checks
func (s *Service) startHealthCheck() {
	s.healthCheck = time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-s.healthCheck.C:
				s.selector.HealthCheck(context.Background())
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop stops the service and health checks
func (s *Service) Stop() {
	close(s.stopCh)
	if s.healthCheck != nil {
		s.healthCheck.Stop()
	}
}

// GetSelector returns the underlying selector (for testing/debugging)
func (s *Service) GetSelector() *selector.TTSSelector {
	return s.selector
}
