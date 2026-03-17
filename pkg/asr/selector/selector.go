// Package selector provides provider selection with failover support.
package selector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/asr"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ProviderStatus tracks the status of a provider
type ProviderStatus int

const (
	StatusHealthy ProviderStatus = iota
	StatusDegraded
	StatusUnavailable
)

// ProviderEntry wraps a provider with its metadata
type ProviderEntry struct {
	Provider asr.Provider
	Name     string
	Status   ProviderStatus
	Priority int // Lower number = higher priority (0 = primary)
	LastErr  error
	LastUsed time.Time
}

// ASRSelector manages multiple ASR providers with failover support
type ASRSelector struct {
	providers []*ProviderEntry
	mu        sync.RWMutex
}

// NewASRSelector creates a selector from configuration.
// It creates providers in priority order (0 = highest priority)
func NewASRSelector(cfg *config.Config) (*ASRSelector, error) {
	if !cfg.Audio.Enabled {
		return nil, fmt.Errorf("audio not enabled")
	}

	selector := &ASRSelector{
		providers: make([]*ProviderEntry, 0),
	}

	// Get provider list from config
	// Priority: primary provider first, then fallbacks
	providerNames := getProviderList(cfg.Audio.ASR.Provider, cfg.Audio.Providers)

	for i, name := range providerNames {
		provider, err := asr.NewProviderFromConfig(name, cfg)
		if err != nil {
			logger.WarnCF("asr-selector", "Failed to create provider", map[string]any{
				"provider": name,
				"error":    err.Error(),
			})
			continue
		}

		entry := &ProviderEntry{
			Provider: provider,
			Name:     name,
			Status:   StatusHealthy,
			Priority: i,
		}
		selector.providers = append(selector.providers, entry)
		logger.InfoCF("asr-selector", "Provider registered", map[string]any{
			"provider": name,
			"priority": i,
		})
	}

	if len(selector.providers) == 0 {
		return nil, fmt.Errorf("no ASR providers available")
	}

	return selector, nil
}

// getProviderList extracts provider names from config
// Returns primary provider first, then any additional providers
func getProviderList(primary string, providersCfg map[string]config.AudioProviderConfig) []string {
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
// It tries providers in priority order, marking failed ones as degraded
func (s *ASRSelector) GetProvider() (asr.Provider, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.providers {
		if entry.Status == StatusUnavailable {
			continue
		}

		entry.LastUsed = time.Now()
		return entry.Provider, entry.Name, nil
	}

	// All providers unavailable, try to use the first one anyway
	if len(s.providers) > 0 {
		logger.WarnCF("asr-selector", "All providers degraded, using primary", nil)
		return s.providers[0].Provider, s.providers[0].Name, nil
	}

	return nil, "", fmt.Errorf("no providers available")
}

// MarkFailed marks a provider as failed (for failover)
func (s *ASRSelector) MarkFailed(providerName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.providers {
		if entry.Name == providerName {
			entry.LastErr = err
			if entry.Status == StatusHealthy {
				entry.Status = StatusDegraded
				logger.WarnCF("asr-selector", "Provider marked as degraded", map[string]any{
					"provider": providerName,
					"error":    err.Error(),
				})
			} else if entry.Status == StatusDegraded {
				entry.Status = StatusUnavailable
				logger.ErrorCF("asr-selector", "Provider marked as unavailable", map[string]any{
					"provider": providerName,
					"error":    err.Error(),
				})
			}
			break
		}
	}
}

// Reset resets provider status (e.g., on health check success)
func (s *ASRSelector) Reset(providerName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.providers {
		if entry.Name == providerName {
			if entry.Status != StatusHealthy {
				logger.InfoCF("asr-selector", "Provider restored to healthy", map[string]any{
					"provider": providerName,
				})
			}
			entry.Status = StatusHealthy
			entry.LastErr = nil
			break
		}
	}
}

// GetProviders returns all provider names for health checks
func (s *ASRSelector) GetProviders() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, len(s.providers))
	for i, entry := range s.providers {
		names[i] = entry.Name
	}
	return names
}

// HealthCheck performs a health check on all providers
func (s *ASRSelector) HealthCheck(ctx context.Context) {
	for _, entry := range s.providers {
		// Try a simple transcription to check health
		// In production, this should be a lightweight check
		// For now, just check if provider is not nil
		if entry.Provider == nil {
			s.MarkFailed(entry.Name, fmt.Errorf("provider is nil"))
		} else {
			s.Reset(entry.Name)
		}
	}
}
