// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package healthcheck

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const (
	statusMigrating = "migrating"
	// gateRetryAfter is the Retry-After, in seconds, of a request the gate
	// refuses.
	gateRetryAfter  = "10"
	gateReadTimeout = 10 * time.Second
	gateStopTimeout = 5 * time.Second
)

// MigrationGate answers HTTP requests while a process waits for, or runs,
// the migration of state to projects. A health path gets 200 with the
// migration's progress, so an orchestrator neither restarts the process nor
// holds a rolling update; every other request gets 503.
type MigrationGate struct {
	report  func() any
	servers []*http.Server
	// listeners are the bound addresses, in the order of the endpoints.
	listeners []net.Listener
	serving   sync.WaitGroup
}

// GateEndpoint is an address the gate listens on, and the path of the
// health endpoint the process serves there.
type GateEndpoint struct {
	Addr       string
	HealthPath string
	// TLS, when set, serves HTTPS.
	TLS *tls.Config
}

// gateResponse is the body of a health request the gate answers.
type gateResponse struct {
	Status    string `json:"status"`
	Migration any    `json:"migration"`
}

// StartMigrationGate listens on every endpoint and answers until Stop. The
// health body carries what report returns, called once per request.
func StartMigrationGate(ctx context.Context, endpoints []GateEndpoint, report func() any) (*MigrationGate, error) {
	g := &MigrationGate{report: report}
	for _, endpoint := range endpoints {
		listener, err := net.Listen("tcp", endpoint.Addr)
		if err != nil {
			_ = g.Stop(ctx)
			return nil, fmt.Errorf("listen on %s while migrating state: %w", endpoint.Addr, err)
		}
		server := &http.Server{
			Handler:           g.handler(endpoint.HealthPath),
			ReadHeaderTimeout: gateReadTimeout,
			// Bounds the read of a body the gate never uses.
			ReadTimeout: gateReadTimeout,
			TLSConfig:   endpoint.TLS,
		}
		g.servers = append(g.servers, server)
		g.listeners = append(g.listeners, listener)
		g.serving.Add(1)
		go g.serve(ctx, server, listener)
	}
	return g, nil
}

func (g *MigrationGate) serve(ctx context.Context, server *http.Server, listener net.Listener) {
	defer g.serving.Done()
	var err error
	if server.TLSConfig != nil {
		err = server.ServeTLS(listener, "", "")
	} else {
		err = server.Serve(listener)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(ctx, "Migration health gate stopped", tag.Addr(listener.Addr().String()), tag.Error(err))
	}
}

func (g *MigrationGate) handler(healthPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != healthPath || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			w.Header().Set("Retry-After", gateRetryAfter)
			http.Error(w, "Dagu is migrating state to projects; retry later", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(gateResponse{Status: statusMigrating, Migration: g.report()}); err != nil {
			logger.Error(r.Context(), "Failed to encode migration health response", tag.Error(err))
		}
	})
}

// Stop closes every listener and returns once the gate has stopped, so the
// process can bind the same addresses.
func (g *MigrationGate) Stop(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gateStopTimeout)
	defer cancel()
	var errs []error
	for _, server := range g.servers {
		errs = append(errs, server.Shutdown(shutdownCtx))
	}
	g.serving.Wait()
	return errors.Join(errs...)
}
