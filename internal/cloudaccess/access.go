// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cloudrelay"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/license"
)

// requestTimeout bounds a ticket request and the relay handshake.
const requestTimeout = 30 * time.Second

// Errors the license settings page shows while no connection is held.
const (
	stateNotConnected = "This server is not connected to Dagu Cloud."
	stateUnreadable   = "The remote access settings could not be read."
)

// Config is what the service works with.
type Config struct {
	// Credentials returns the server's Dagu Cloud credentials, and false
	// while it is not connected.
	Credentials func() (license.CloudCredentials, bool)
	Settings    *Settings
	// Key verifies the assertions Dagu Cloud signs for calls.
	Key ed25519.PublicKey
	// HTTPClient reaches Dagu Cloud; nil means a client with a 30-second
	// timeout.
	HTTPClient *http.Client
	// Hello introduces the server to the relay; the service names its
	// process.
	Hello cloudrelay.Hello
}

// State is how this process's connection for calls stands.
type State struct {
	// Connected means calls from Dagu Cloud reach this process.
	Connected bool
	// Error says why the last connection failed or was refused.
	Error string
}

// timing is how the service paces itself; tests shorten it.
type timing struct {
	// check is how often the settings and the license are read again.
	check time.Duration
	// minBackoff and maxBackoff bound the wait before connecting again
	// after a connection failed.
	minBackoff time.Duration
	maxBackoff time.Duration
	// stable is how long a connection must have lasted for a failure after
	// it to start the backoff afresh.
	stable time.Duration
	// refused is how long the service waits after Dagu Cloud refused a
	// ticket, which asking again soon does not change.
	refused time.Duration
}

var defaultTiming = timing{
	check:      time.Second,
	minBackoff: time.Second,
	maxBackoff: time.Minute,
	stable:     time.Minute,
	refused:    10 * time.Minute,
}

// Service holds this process's relay connection for calls from Dagu Cloud
// while the server is connected to Dagu Cloud and remote access is on, as
// its Settings decide.
type Service struct {
	*Settings
	cfg    Config
	timing timing
	calls  *calls

	mu    sync.Mutex
	state State
}

// New creates a Service.
func New(cfg Config) *Service {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: requestTimeout}
	}
	// The relay keeps one connection per process, by this name.
	cfg.Hello.Instance = newInstance()
	return &Service{
		Settings: cfg.Settings,
		cfg:      cfg,
		timing:   defaultTiming,
		calls:    &calls{settings: cfg.Settings, credentials: cfg.Credentials, verifier: newVerifier(cfg.Key)},
	}
}

// State returns how the connection stands.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Run holds the connection until ctx is done, and serves with mcp each call
// whose assertion holds, as the user it names. Turning remote access off
// closes the connection, which cancels the calls in flight.
func (s *Service) Run(ctx context.Context, mcp http.Handler) {
	s.calls.mcp = mcp
	backoff := s.timing.minBackoff
	for {
		// The read below sees any change made before it.
		select {
		case <-s.changes():
		default:
		}
		wait := s.timing.check
		creds, connected := s.cfg.Credentials()
		status, err := s.Status(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Warn(ctx, "Remote access: the settings could not be read", tag.Error(err))
			s.setState(State{Error: stateUnreadable})
		case status.Level == config.RemoteAccessOff:
			s.setState(State{})
		case !connected:
			s.setState(State{Error: stateNotConnected})
		default:
			started := time.Now()
			err := s.connect(ctx, creds)
			if ctx.Err() != nil {
				return
			}
			refused, isRefused := errors.AsType[*cloudrelay.TicketRefusedError](err)
			switch {
			case err == nil:
				// The relay ended the connection on schedule, or remote
				// access was turned off.
				backoff = s.timing.minBackoff
				wait = 0
				s.setState(State{})
			case isRefused:
				wait = s.timing.refused
				s.setState(State{Error: refusalMessage(refused)})
			case errors.Is(err, cloudrelay.ErrDropped):
				wait = s.timing.refused
				s.setState(State{Error: err.Error()})
			default:
				if time.Since(started) >= s.timing.stable {
					backoff = s.timing.minBackoff
				}
				wait = backoff/2 + rand.N(backoff/2+1) //nolint:gosec // jitter, not cryptographic
				backoff = min(2*backoff, s.timing.maxBackoff)
				logger.Warn(ctx, "Remote access: the relay connection failed", tag.Error(err))
				s.setState(State{Error: err.Error()})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-s.changes():
		}
	}
}

// connect holds one relay connection until it ends, remote access is turned
// off, or the server leaves Dagu Cloud. It returns nil in the last two cases.
func (s *Service) connect(ctx context.Context, creds license.CloudCredentials) error {
	ticket, err := cloudrelay.RequestAccessTicket(ctx, s.cfg.HTTPClient, creds)
	if err != nil {
		return err
	}
	conn, err := cloudrelay.Dial(ctx, s.cfg.HTTPClient, ticket, s.cfg.Hello)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logger.Info(ctx, "Remote access: connected to the relay")
	s.setState(State{Connected: true})

	served := make(chan error, 1)
	go func() { served <- conn.Serve(ctx, cloudrelay.Handler{Call: s.calls}) }()
	for {
		select {
		case err := <-served:
			return err
		case <-time.After(s.timing.check):
		case <-s.changes():
		}
		status, err := s.Status(ctx)
		_, connected := s.cfg.Credentials()
		if err != nil || status.Level == config.RemoteAccessOff || !connected {
			cancel()
			<-served
			logger.Info(ctx, "Remote access: disconnected from the relay")
			return nil
		}
	}
}

func (s *Service) setState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// refusalMessage says why Dagu Cloud refused the connection: its own words
// when it names a reason, such as a workspace without a plan.
func refusalMessage(err *cloudrelay.TicketRefusedError) string {
	if err.StatusCode == http.StatusForbidden && err.Message != "" {
		return strings.ToUpper(err.Message[:1]) + err.Message[1:] + "."
	}
	return err.Error()
}

// newInstance names this process to the relay: 16 random bytes, unpadded
// base64url.
func newInstance() string {
	b := make([]byte, 16)
	_, _ = cryptorand.Read(b) // never fails
	return base64.RawURLEncoding.EncodeToString(b)
}
