// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// ConnectState describes the progress of a request to connect this server to
// Dagu Console.
type ConnectState string

// Connection request states.
const (
	ConnectIdle    ConnectState = "idle"
	ConnectPending ConnectState = "pending"
	ConnectGranted ConnectState = "granted"
	ConnectFailed  ConnectState = "failed"
	ConnectExpired ConnectState = "expired"
)

const (
	connectPollInterval = 3 * time.Second
	// connectTTL matches how long Dagu Console keeps an approval.
	connectTTL = 15 * time.Minute
	// connectCodeLength is how much of the code both sides display.
	connectCodeLength = 8

	connectUnsupportedFailure  = "Dagu Console does not support connecting servers yet. Use a license key instead."
	connectVerificationFailure = "Dagu Console returned a license this server could not verify."
	connectExpiredFailure      = "The connection request expired before it was approved. Start a new one."
	connectRejectedFailure     = "Dagu Console rejected the connection request."
	connectLicensedFailure     = "This server got a license another way while the request waited for approval."
)

var (
	// ErrManagedExternally is returned when the license comes from
	// DAGU_LICENSE, DAGU_LICENSE_KEY, or license.key, which would replace a
	// connected license on restart.
	ErrManagedExternally = errors.New("the license is set by DAGU_LICENSE, DAGU_LICENSE_KEY, or license.key; remove it to connect from Dagu Console")
	// ErrAlreadyLicensed is returned when connecting a server that already
	// has a usable license.
	ErrAlreadyLicensed = errors.New("this server already has an active license; disconnect it first")
	// ErrNoCheckIn is returned when refreshing a license that does not check
	// in with Dagu Console.
	ErrNoCheckIn = errors.New("this license does not check in with Dagu Console")

	// errLicensedMeanwhile reports an approval that arrived after the server
	// was licensed another way.
	errLicensedMeanwhile = errors.New("licensed while the request waited")
)

// ConnectStatus reports the current request to connect to Dagu Console.
type ConnectStatus struct {
	State ConnectState
	// URL opens the approval page in Dagu Console.
	URL string
	// Code is displayed on both sides so the approver can confirm the request.
	Code      string
	ExpiresAt time.Time
	// Error explains a failed request.
	Error string
}

type connectSession struct {
	// secret proves to Dagu Console that this server started the request;
	// only its SHA-256 digest leaves the server.
	secret   string
	serverID string
	cancel   context.CancelFunc
	done     chan struct{}

	mu     sync.Mutex
	status ConnectStatus
}

func (s *connectSession) snapshot() ConnectStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *connectSession) finish(state ConnectState, failure string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = state
	s.status.Error = failure
}

func (s *connectSession) stop() {
	s.cancel()
	<-s.done
}

// Connect starts a request to connect this server to Dagu Console, or returns
// the pending one. An owner approves it by opening the returned URL; the
// license is then installed in the background.
func (m *Manager) Connect() (ConnectStatus, error) {
	m.connectMu.Lock()
	defer m.connectMu.Unlock()
	if err := m.checkConnectable(); err != nil {
		return ConnectStatus{}, err
	}
	if s := m.connect; s != nil {
		if status := s.snapshot(); status.State == ConnectPending {
			return status, nil
		}
		s.stop()
		m.connect = nil
	}

	serverID, err := GetOrCreateServerID(m.cfg.LicenseDir)
	if err != nil {
		return ConnectStatus{}, fmt.Errorf("failed to get server ID: %w", err)
	}
	secret, err := randomHex(32)
	if err != nil {
		return ConnectStatus{}, err
	}
	code := connectionCode(secret)
	expiresAt := time.Now().Add(m.connectTTL)
	query := url.Values{"code": {code}, "name": {m.serverName}, "server_id": {serverID}}

	ctx, cancel := context.WithDeadline(context.Background(), expiresAt)
	s := &connectSession{
		secret:   secret,
		serverID: serverID,
		cancel:   cancel,
		done:     make(chan struct{}),
		status: ConnectStatus{
			State:     ConnectPending,
			URL:       m.client.baseURL + "/servers/connect?" + query.Encode(),
			Code:      strings.ToUpper(code[:connectCodeLength]),
			ExpiresAt: expiresAt,
		},
	}
	m.connect = s
	go m.pollConnect(ctx, s)
	return s.snapshot(), nil
}

// ConnectStatus returns the state of the latest connection request.
func (m *Manager) ConnectStatus() ConnectStatus {
	m.connectMu.Lock()
	s := m.connect
	m.connectMu.Unlock()
	if s == nil {
		return ConnectStatus{State: ConnectIdle}
	}
	return s.snapshot()
}

// CancelConnect abandons the pending connection request. It must not be
// called while holding transitionMu, which the request takes to install an
// approved license.
func (m *Manager) CancelConnect() {
	m.connectMu.Lock()
	s := m.connect
	m.connect = nil
	m.connectMu.Unlock()
	if s != nil {
		s.stop()
	}
}

// Refresh checks in with Dagu Console now instead of at the next hourly
// check-in, for example right after the workspace's plan changed.
func (m *Manager) Refresh(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()

	ad := m.currentActivation()
	if ad == nil || m.state.IsCommunity() || !m.Source().NeedsHeartbeat() {
		return ErrNoCheckIn
	}
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	return m.checkIn(ctx, ad)
}

// checkConnectable reports why this server cannot request a license. It holds
// transitionMu so that a license being installed concurrently is seen.
func (m *Manager) checkConnectable() error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.licenseConfigured() {
		return ErrManagedExternally
	}
	if HasActiveLicense(m.state) {
		return ErrAlreadyLicensed
	}
	return nil
}

// licenseConfigured reports whether a license source that outranks a
// connected license on restart is set.
func (m *Manager) licenseConfigured() bool {
	return os.Getenv("DAGU_LICENSE") != "" || os.Getenv("DAGU_LICENSE_KEY") != "" || m.cfg.ConfigKey != ""
}

func (m *Manager) pollConnect(ctx context.Context, s *connectSession) {
	defer close(s.done)
	ticker := time.NewTicker(m.connectPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.finish(ConnectExpired, connectExpiredFailure)
			}
			return
		case <-ticker.C:
		}

		resp, err := m.client.Connect(ctx, ConnectRequest{
			ConnectionSecret: s.secret,
			ServerID:         s.serverID,
			ServerName:       m.serverName,
			ClientVersion:    config.Version,
		})
		if err != nil {
			if failure, final := connectFailure(err); final && ctx.Err() == nil {
				s.finish(ConnectFailed, failure)
				return
			}
			m.logger.Debug("Dagu Console connection poll failed", slog.String("error", err.Error()))
			continue
		}
		if resp.Status != connectResponseGranted {
			continue
		}

		if err := m.completeConnect(ctx, s, resp); err != nil {
			switch {
			case ctx.Err() != nil:
			case errors.Is(err, errLicensedMeanwhile):
				s.finish(ConnectFailed, connectLicensedFailure)
			default:
				m.logger.Warn("Failed to install the license from Dagu Console", slog.String("error", err.Error()))
				s.finish(ConnectFailed, connectVerificationFailure)
			}
			return
		}
		s.finish(ConnectGranted, "")
		m.logger.Info("Connected to Dagu Console")
		return
	}
}

// connectFailure returns the message for an error that ends a connection
// request. Network failures, rate limits, and server errors are retried.
func connectFailure(err error) (string, bool) {
	cloudErr, ok := errors.AsType[*CloudError](err)
	if !ok || cloudErr.StatusCode < 400 || cloudErr.StatusCode >= 500 || cloudErr.StatusCode == http.StatusTooManyRequests {
		return "", false
	}
	switch {
	case cloudErr.StatusCode == http.StatusNotFound:
		return connectUnsupportedFailure, true
	case cloudErr.Message == "":
		return connectRejectedFailure, true
	}
	return cloudErr.Message, true
}

func (m *Manager) completeConnect(ctx context.Context, s *connectSession, resp *ConnectResponse) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	// A request cancelled while waiting for the lock must not replace the
	// license installed by whoever cancelled it.
	if err := ctx.Err(); err != nil {
		return err
	}
	ad := &ActivationData{
		Token:           resp.Token,
		HeartbeatSecret: resp.HeartbeatSecret,
		ServerID:        s.serverID,
		Via:             ConnectedViaConsole,
		CheckedInAt:     time.Now(),
	}
	if current := m.state.Claims(); current != nil && HasActiveLicense(m.state) {
		granted, err := VerifyToken(m.pubKey, resp.Token)
		if err != nil {
			return err
		}
		// A grant for the license already in use replaced that activation's
		// credentials, so it must be installed. A grant for another license
		// would hold a second slot; give it back.
		if granted.ID != current.ID {
			m.releaseGrant(ctx, granted.ID, ad)
			return errLicensedMeanwhile
		}
	}
	_, err := m.installActivation(ad)
	return err
}

// releaseGrant gives back a slot granted to a request that was not installed.
func (m *Manager) releaseGrant(ctx context.Context, licenseID string, ad *ActivationData) {
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	err := m.client.Release(ctx, ReleaseRequest{
		LicenseID:       licenseID,
		ServerID:        ad.ServerID,
		HeartbeatSecret: ad.HeartbeatSecret,
	})
	if err != nil {
		m.logger.Warn("Failed to give back an unused slot in Dagu Console", slog.String("error", err.Error()))
	}
}

// connectionCode is the public identifier of a connection request: the
// lowercase hex SHA-256 digest of the secret's hex encoding.
func connectionCode(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate connection secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}
