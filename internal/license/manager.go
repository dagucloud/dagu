// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

const (
	heartbeatInterval = 24 * time.Hour

	licenseDiscoveryFailure    = "License discovery failed. Check the configured license file and server logs."
	licenseActivationFailure   = "License activation failed. Check the configured license key, network access, and server logs."
	licenseVerificationFailure = "License token verification failed. Check the configured token and server logs."
	licenseExpiredFailure      = "The Dagu license has expired."
	licenseRevokedFailure      = "The Dagu license has been revoked."
	licenseUnauthorizedFailure = "The Dagu license activation is no longer authorized."
)

// ManagerConfig holds the configuration for the license manager.
type ManagerConfig struct {
	LicenseDir string
	ConfigKey  string
	CloudURL   string
}

// ActivationResult is returned after a successful activation.
type ActivationResult struct {
	Plan     string
	Features []string
	Expiry   time.Time
}

// Manager orchestrates license discovery, activation, verification, and heartbeat.
type Manager struct {
	cfg    ManagerConfig
	state  *State
	store  ActivationStore
	client *CloudClient
	pubKey ed25519.PublicKey
	logger *slog.Logger

	statusMu sync.RWMutex
	source   DiscoverySource
	failure  string

	transitionMu sync.Mutex

	cancelMu         sync.Mutex
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	heartbeatRunning bool
}

// NewManager creates a new license manager.
func NewManager(cfg ManagerConfig, pubKey ed25519.PublicKey, store ActivationStore, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		cfg:    cfg,
		state:  &State{},
		store:  store,
		client: NewCloudClient(cfg.CloudURL),
		pubKey: pubKey,
		logger: logger,
	}
}

// Checker returns the Checker interface backed by the manager's state.
func (m *Manager) Checker() Checker {
	return m.state
}

// Source returns the discovery source of the current license.
func (m *Manager) Source() DiscoverySource {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.source
}

// Failure returns a user-facing explanation when a configured license could
// not be loaded or is no longer usable.
func (m *Manager) Failure() string {
	m.statusMu.RLock()
	failure := m.failure
	m.statusMu.RUnlock()
	if failure != "" {
		return failure
	}

	if m.state.isPastGracePeriod() {
		return licenseExpiredFailure
	}
	return ""
}

func (m *Manager) setSource(source DiscoverySource) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.source = source
}

func (m *Manager) setFailure(failure string) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.failure = failure
}

// Start performs discovery, optional activation, JWT verification, and starts the heartbeat loop.
// It always returns nil for graceful degradation: license errors are logged but never prevent
// the application from starting.
func (m *Manager) Start(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()

	m.setFailure("")
	var activationToPersist *ActivationData
	result, err := Discover(m.cfg.LicenseDir, m.cfg.ConfigKey, m.store)
	if err != nil {
		m.logger.Warn("License discovery failed", slog.String("error", err.Error()))
		m.setFailure(licenseDiscoveryFailure)
		return nil // graceful degradation
	}

	m.setSource(result.Source)

	if result.Source == SourceNone {
		m.logger.Debug("No license configured, running in community mode")
		return nil
	}

	// If we have a key but no token, activate first
	if result.LicenseKey != "" && result.Token == "" {
		activationResult, activateErr := m.activate(ctx, result.LicenseKey)
		if activateErr != nil {
			cached := m.loadCachedActivation(result.LicenseKey)
			if cached == nil {
				m.logger.Warn("License activation failed, running in community mode",
					slog.String("error", activateErr.Error()))
				m.setFailure(licenseActivationFailure)
				return nil // graceful degradation
			}
			m.logger.Info("License activation failed, using cached activation (offline mode)",
				slog.String("error", activateErr.Error()))
			result.Token = cached.Token
			result.Activation = cached
		} else {
			result.Token = activationResult.Token
			result.Activation = activationResult
			activationToPersist = activationResult
		}
	}

	// Verify the token
	claims, verifyErr := VerifyToken(m.pubKey, result.Token)
	tokenExpired := verifyErr != nil
	if tokenExpired {
		// Try lenient verification for grace period
		claims, verifyErr = VerifyTokenLenient(m.pubKey, result.Token)
		if verifyErr != nil {
			m.logger.Warn("License token verification failed",
				slog.String("error", verifyErr.Error()))
			m.setFailure(licenseVerificationFailure)
			return nil // graceful degradation
		}
	}

	m.state.Update(claims, result.Token)
	if activationToPersist != nil {
		m.saveActivation(activationToPersist)
	}
	if tokenExpired {
		if m.state.IsGracePeriod() {
			m.logger.Warn("License token is expired, operating in grace period")
		} else {
			m.logger.Warn("License token is expired and outside its grace period")
			m.setFailure(licenseExpiredFailure)
		}
	}

	m.logger.Info("License loaded",
		slog.String("plan", claims.Plan),
		slog.Any("features", claims.Features),
		slog.String("source", result.Source.String()),
	)

	// Start heartbeat loop if the source requires it
	if result.Source.NeedsHeartbeat() && result.Activation != nil {
		m.startHeartbeat(result.Activation)
	}

	return nil
}

// Stop cancels the heartbeat goroutine and waits for completion.
func (m *Manager) Stop() {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.stopHeartbeat()
}

func (m *Manager) stopHeartbeat() {
	m.cancelMu.Lock()
	m.heartbeatRunning = false
	cancel := m.cancel
	m.cancel = nil
	m.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

// Deactivate stops the heartbeat, clears in-memory state, and removes persisted activation data.
// It returns an error if the license was configured via an environment variable (the user must
// remove the env var instead) or if there is no active license to deactivate.
func (m *Manager) Deactivate(_ context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()

	if m.Source().IsEnv() {
		return fmt.Errorf("cannot deactivate: license is configured via environment variable; remove DAGU_LICENSE or DAGU_LICENSE_KEY instead")
	}
	if m.state.IsCommunity() {
		return fmt.Errorf("no active license to deactivate")
	}

	m.stopHeartbeat()
	m.state.Update(nil, "")
	m.setSource(SourceNone)
	m.setFailure("")

	if m.store != nil {
		if err := m.store.Remove(); err != nil {
			return fmt.Errorf("failed to remove activation data: %w", err)
		}
	}

	return nil
}

// ActivateWithKey performs activation with the given key and updates internal state.
// This is used by the API handler for frontend-initiated activation.
func (m *Manager) ActivateWithKey(ctx context.Context, key string) (*ActivationResult, error) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()

	ad, err := m.activate(ctx, key)
	if err != nil {
		return nil, err
	}

	claims, verifyErr := VerifyToken(m.pubKey, ad.Token)
	if verifyErr != nil {
		return nil, fmt.Errorf("activated token verification failed: %w", verifyErr)
	}

	m.stopHeartbeat()
	m.saveActivation(ad)
	m.setSource(SourceActivationFile)
	m.state.Update(claims, ad.Token)
	m.setFailure("")

	m.startHeartbeat(ad)

	result := &ActivationResult{
		Plan:     claims.Plan,
		Features: claims.Features,
	}
	if claims.ExpiresAt != nil {
		result.Expiry = claims.ExpiresAt.Time
	}
	return result, nil
}

func (m *Manager) activate(ctx context.Context, key string) (*ActivationData, error) {
	serverID, err := GetOrCreateServerID(m.cfg.LicenseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get server ID: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		m.logger.Warn("Failed to get hostname", slog.String("error", err.Error()))
		hostname = "unknown"
	}

	resp, err := m.client.Activate(ctx, ActivateRequest{
		Key:           key,
		ServerID:      serverID,
		MachineName:   hostname,
		ClientVersion: config.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("activation request failed: %w", err)
	}

	ad := &ActivationData{
		Token:           resp.Token,
		HeartbeatSecret: resp.HeartbeatSecret,
		LicenseKey:      key,
		ServerID:        serverID,
	}

	return ad, nil
}

func (m *Manager) saveActivation(ad *ActivationData) {
	if m.store == nil {
		return
	}
	if err := m.store.Save(ad); err != nil {
		m.logger.Warn("Failed to persist activation data", slog.String("error", err.Error()))
	}
}

func (m *Manager) loadCachedActivation(licenseKey string) *ActivationData {
	if m.store == nil {
		return nil
	}
	cached, err := m.store.Load()
	if err != nil {
		m.logger.Debug("Failed to load cached activation", slog.String("error", err.Error()))
		return nil
	}
	if cached == nil || cached.Token == "" || cached.LicenseKey != licenseKey {
		return nil
	}
	return cached
}

func (m *Manager) startHeartbeat(ad *ActivationData) {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	if m.heartbeatRunning {
		return
	}
	m.heartbeatRunning = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go m.heartbeatLoop(ctx, ad)
}

func (m *Manager) heartbeatLoop(ctx context.Context, ad *ActivationData) {
	defer m.wg.Done()

	// Immediate heartbeat on startup to refresh the JWT.
	m.doHeartbeat(ctx, ad)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.doHeartbeat(ctx, ad)
		}
	}
}

func (m *Manager) doHeartbeat(ctx context.Context, ad *ActivationData) {
	claims := m.state.Claims()
	if claims == nil {
		return
	}

	resp, err := m.client.Heartbeat(ctx, HeartbeatRequest{
		LicenseID:       claims.ID,
		ServerID:        ad.ServerID,
		HeartbeatSecret: ad.HeartbeatSecret,
		ClientVersion:   config.Version,
	})
	if err != nil {
		if cloudErr, ok := errors.AsType[*CloudError](err); ok {
			switch cloudErr.StatusCode {
			case 410: // Gone - license revoked
				m.logger.Error("License has been revoked, clearing in-memory state")
				m.state.Update(nil, "")
				m.setFailure(licenseRevokedFailure)
				return
			case 401: // Unauthorized - deactivated or credentials invalid
				m.logger.Error("License heartbeat unauthorized, license may have been deactivated",
					slog.String("error", cloudErr.Message))
				m.state.Update(nil, "")
				m.setFailure(licenseUnauthorizedFailure)
				return
			case 400: // Expired - keep cached token so runtime can enforce expiry/grace locally
				m.logger.Warn("License heartbeat reported an expired license, continuing with cached token",
					slog.String("error", cloudErr.Message))
				if !m.state.IsGracePeriod() {
					m.setFailure(licenseExpiredFailure)
				}
				return
			}
		}
		// Network error or other transient failure - continue with cached JWT
		m.logger.Warn("License heartbeat failed, continuing with cached token",
			slog.String("error", err.Error()))
		return
	}

	// Verify the refreshed token
	newClaims, verifyErr := VerifyToken(m.pubKey, resp.Token)
	if verifyErr != nil {
		m.logger.Warn("Refreshed token verification failed",
			slog.String("error", verifyErr.Error()))
		return
	}

	m.state.Update(newClaims, resp.Token)
	m.setFailure("")

	// Persist the refreshed token using a copy to avoid mutating the shared ActivationData.
	if m.store != nil {
		updated := *ad
		updated.Token = resp.Token
		if err := m.store.Save(&updated); err != nil {
			m.logger.Warn("Failed to persist refreshed token",
				slog.String("error", err.Error()))
		}
	}

	m.logger.Debug("License heartbeat successful",
		slog.String("plan", newClaims.Plan))
}
