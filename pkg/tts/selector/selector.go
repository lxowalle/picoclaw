// Package selector provides provider selection with failover support.
package selector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tts"
)

// ProviderStatus tracks the status of a provider
type TTSProviderStatus int

const (
	TTSStatusHealthy TTSProviderStatus = iota
	TTSStatusDegraded
	TTSStatusUnavailable
)

// TTSProviderEntry wraps a provider with its metadata
type TTSProviderEntry struct {
	Provider tts.Provider
	Name     string
	Status   TTSProviderStatus
	Priority int // Lower number = higher priority (0 = primary)
	LastErr  error
	LastUsed time.Time
}

// TTSSelector manages multiple TTS providers with failover support
type TTSSelector struct {
	providers []*TTSProviderEntry
	mu        sync.RWMutex
}

// NewTTSSelector creates a selector from configuration.
// It creates providers in priority order (0 = highest priority)
func NewTTSSelector(cfg *config.Config) (*TTSSelector, error) {
	if !cfg.Audio.Enabled {
		return nil, fmt.Errorf("audio not enabled")
	}

	selector := &TTSSelector{
		providers: make([]*TTSProviderEntry, 0),
	}

	// Get provider list from config
	providerNames := getTTSProviderList(cfg.Audio.TTS.Provider, cfg.Audio.Providers)

	for i, name := range providerNames {
		provider, err := tts.NewProviderFromConfig(name, cfg)
		if err != nil {
			logger.WarnCF("tts-selector", "Failed to create provider", map[string]any{
				"provider": name,
				"error":    err.Error(),
			})
			continue
		}

		entry := &TTSProviderEntry{
			Provider: provider,
			Name:     name,
			Status:   TTSStatusHealthy,
			Priority: i,
		}
		selector.providers = append(selector.providers, entry)
		logger.InfoCF("tts-selector", "Provider registered", map[string]any{
			"provider": name,
			"priority": i,
		})
	}

	if len(selector.providers) == 0 {
		return nil, fmt.Errorf("no TTS providers available")
	}

	return selector, nil
}

// getTTSProviderList extracts provider names from config
func getTTSProviderList(primary string, providersCfg map[string]config.AudioProviderConfig) []string {
	names := make([]string, 0)

	// Add primary provider first
	if primary != "" {
		names = append(names, primary)
	}

	// Add other enabled providers as fallbacks
	for name, cfg := range providersCfg {
		if cfg.Enabled && name != primary {
			names = append(names, name)
		}
	}

	return names
}

// GetProvider returns the best available provider
func (s *TTSSelector) GetProvider() (tts.Provider, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.providers {
		if entry.Status == TTSStatusUnavailable {
			continue
		}

		entry.LastUsed = time.Now()
		return entry.Provider, entry.Name, nil
	}

	// All providers unavailable, try to use the first one anyway
	if len(s.providers) > 0 {
		logger.WarnCF("tts-selector", "All providers degraded, using primary", nil)
		return s.providers[0].Provider, s.providers[0].Name, nil
	}

	return nil, "", fmt.Errorf("no providers available")
}

// MarkFailed marks a provider as failed (for failover)
func (s *TTSSelector) MarkFailed(providerName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.providers {
		if entry.Name == providerName {
			entry.LastErr = err
			if entry.Status == TTSStatusHealthy {
				entry.Status = TTSStatusDegraded
				logger.WarnCF("tts-selector", "Provider marked as degraded", map[string]any{
					"provider": providerName,
					"error":    err.Error(),
				})
			} else if entry.Status == TTSStatusDegraded {
				entry.Status = TTSStatusUnavailable
				logger.ErrorCF("tts-selector", "Provider marked as unavailable", map[string]any{
					"provider": providerName,
					"error":    err.Error(),
				})
			}
			break
		}
	}
}

// Reset resets provider status
func (s *TTSSelector) Reset(providerName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.providers {
		if entry.Name == providerName {
			if entry.Status != TTSStatusHealthy {
				logger.InfoCF("tts-selector", "Provider restored to healthy", map[string]any{
					"provider": providerName,
				})
			}
			entry.Status = TTSStatusHealthy
			entry.LastErr = nil
			break
		}
	}
}

// GetProviders returns all provider names
func (s *TTSSelector) GetProviders() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, len(s.providers))
	for i, entry := range s.providers {
		names[i] = entry.Name
	}
	return names
}

// HealthCheck performs a health check on all providers
func (s *TTSSelector) HealthCheck(ctx context.Context) {
	for _, entry := range s.providers {
		if entry.Provider == nil {
			s.MarkFailed(entry.Name, fmt.Errorf("provider is nil"))
		} else {
			s.Reset(entry.Name)
		}
	}
}

// OutputFormat returns the output format of the primary provider
func (s *TTSSelector) OutputFormat() tts.Format {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.providers) > 0 && s.providers[0].Provider != nil {
		return s.providers[0].Provider.OutputFormat()
	}
	return tts.FormatOpus
}
