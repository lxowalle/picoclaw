// Package service provides ASR service layer with provider selection and failover.
package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/asr"
	"github.com/sipeed/picoclaw/pkg/asr/selector"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// Service provides high-level ASR functionality with automatic provider selection and failover
type Service struct {
	selector    *selector.ASRSelector
	mu          sync.RWMutex
	healthCheck *time.Ticker
	stopCh      chan struct{}
}

// NewServiceFromConfig creates an ASR service from configuration.
// It creates a selector with all configured providers and starts health monitoring.
func NewServiceFromConfig(cfg *config.Config) (*Service, error) {
	sel, err := selector.NewASRSelector(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ASR selector: %w", err)
	}

	svc := &Service{
		selector: sel,
		stopCh:   make(chan struct{}),
	}

	// Start health check goroutine
	svc.startHealthCheck()

	return svc, nil
}

// NewService creates an ASR service with a single provider (for backward compatibility)
func NewService(provider asr.Provider) *Service {
	// Create a selector with single provider
	cfg := &config.Config{
		Audio: config.AudioConfig{
			Enabled: true,
			ASR: config.AudioASRConfig{
				Enabled:  true,
				Provider: provider.Name(),
			},
			Providers: map[string]config.AudioProviderConfig{
				provider.Name(): {Enabled: true},
			},
		},
	}

	sel, _ := selector.NewASRSelector(cfg)
	if sel == nil {
		// Fallback: create selector manually
		sel = &selector.ASRSelector{}
	}

	return &Service{
		selector: sel,
		stopCh:   make(chan struct{}),
	}
}

// Recognize performs speech recognition with automatic provider failover
func (s *Service) Recognize(ctx context.Context, audio []byte, format asr.Format) (string, error) {
	// Try providers in priority order
	providers := s.selector.GetProviders()

	for range providers {
		provider, name, err := s.selector.GetProvider()
		if err != nil {
			continue
		}

		// Check format support
		if !s.providerSupportsFormat(provider, format) {
			logger.DebugCF("asr-service", "Provider doesn't support format, trying next", map[string]any{
				"provider": name,
				"format":   format,
			})
			continue
		}

		// Attempt transcription
		text, err := provider.Transcribe(ctx, audio, format)
		if err != nil {
			logger.WarnCF("asr-service", "Provider failed, marking for failover", map[string]any{
				"provider": name,
				"error":    err.Error(),
			})
			s.selector.MarkFailed(name, err)
			continue
		}

		// Success - reset provider status if it was degraded
		s.selector.Reset(name)
		return text, nil
	}

	return "", fmt.Errorf("all ASR providers failed")
}

// RecognizeStream performs streaming speech recognition with failover support
func (s *Service) RecognizeStream(ctx context.Context, audioStream <-chan []byte, format asr.Format) (
	<-chan asr.TranscriptionResult, error) {

	provider, name, err := s.selector.GetProvider()
	if err != nil {
		return nil, fmt.Errorf("no ASR provider available: %w", err)
	}

	// Note: Streaming failover is more complex and typically requires session recovery
	// For now, we use the primary provider and let the caller handle failures
	results, err := provider.TranscribeStream(ctx, audioStream, format)
	if err != nil {
		s.selector.MarkFailed(name, err)
		return nil, fmt.Errorf("streaming recognition failed: %w", err)
	}

	return results, nil
}

// Name returns the current provider name
func (s *Service) Name() string {
	_, name, _ := s.selector.GetProvider()
	return name
}

// Capabilities returns the capabilities of the current provider
func (s *Service) Capabilities() asr.ASRCapabilities {
	provider, _, _ := s.selector.GetProvider()
	if provider == nil {
		return asr.ASRCapabilities{}
	}
	return provider.Capabilities()
}

// providerSupportsFormat checks if a provider supports a specific format
func (s *Service) providerSupportsFormat(provider asr.Provider, format asr.Format) bool {
	if provider == nil {
		return false
	}
	caps := provider.Capabilities()
	for _, f := range caps.SupportsFormat {
		if f == format {
			return true
		}
	}
	return false
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
func (s *Service) GetSelector() *selector.ASRSelector {
	return s.selector
}
