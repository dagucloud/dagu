// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// Config holds the configuration for tunnel services.
type Config struct {
	Enabled   bool
	Tailscale TailscaleConfig

	// Security options
	AllowTerminal bool
	AllowedIPs    []string
	RateLimiting  RateLimitConfig
}

// TailscaleConfig holds Tailscale settings.
type TailscaleConfig struct {
	AuthKey  string // Optional - auth key for headless login
	Hostname string // Machine name in tailnet
	Funnel   bool   // Enable Tailscale Funnel for public access
	HTTPS    bool   // Use HTTPS for tailnet-only access (requires enabling HTTPS in Tailscale admin)
	StateDir string // State directory
}

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	Enabled              bool
	LoginAttempts        int
	WindowSeconds        int
	BlockDurationSeconds int
}

// Service manages tunnel lifecycle.
type Service struct {
	config   *Config
	provider Provider
	mu       sync.RWMutex
	running  bool
	urlFile  string // Path to store the tunnel URL
}

// NewService creates a new tunnel service based on configuration.
func NewService(cfg *Config, dataDir string) (*Service, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	s := &Service{
		config:  cfg,
		urlFile: filepath.Join(dataDir, "tunnel_url"),
	}

	// Create Tailscale provider (only supported provider)
	var err error
	s.provider, err = NewTailscaleProvider(&cfg.Tailscale, dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create tailscale provider: %w", err)
	}

	return s, nil
}

// Start begins the tunnel service.
func (s *Service) Start(ctx context.Context, localAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	if err := s.provider.Start(ctx, localAddr); err != nil {
		return err
	}

	s.running = true

	// Store the tunnel URL for recovery/debugging
	if url := s.provider.PublicURL(); url != "" {
		s.saveURL(url)
	}

	return nil
}

// Stop halts the tunnel service.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if err := s.provider.Stop(ctx); err != nil {
		return err
	}

	s.running = false
	return nil
}

// Info returns the current tunnel information.
func (s *Service) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.provider == nil {
		return Info{Status: StatusDisabled}
	}

	return s.provider.Info()
}

// PublicURL returns the public URL of the tunnel.
func (s *Service) PublicURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.provider == nil {
		return ""
	}

	return s.provider.PublicURL()
}

// IsPublic returns true if the tunnel exposes the service publicly.
func (s *Service) IsPublic() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.provider == nil {
		return false
	}

	return s.provider.IsPublic()
}

// IsEnabled returns true if tunneling is enabled.
func (s *Service) IsEnabled() bool {
	return s != nil && s.config != nil && s.config.Enabled
}

// IsRunning returns true if the tunnel is currently running.
func (s *Service) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Config returns the tunnel configuration.
func (s *Service) Config() *Config {
	return s.config
}

// saveURL saves the tunnel URL to a file for recovery/debugging.
func (s *Service) saveURL(url string) {
	if s.urlFile == "" {
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(s.urlFile)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return
	}

	_ = fileutil.WriteFileAtomic(s.urlFile, []byte(url), 0600)
}

// LoadLastURL loads the last known tunnel URL from file.
func (s *Service) LoadLastURL() string {
	if s.urlFile == "" {
		return ""
	}

	data, err := os.ReadFile(s.urlFile)
	if err != nil {
		return ""
	}

	return string(data)
}
