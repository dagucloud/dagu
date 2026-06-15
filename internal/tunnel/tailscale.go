// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tailscale.com/tsnet"
)

// TailscaleProvider implements the Provider interface for Tailscale.
type TailscaleProvider struct {
	config    *TailscaleConfig
	stateDir  string
	mu        sync.RWMutex
	status    Status
	publicURL string
	startedAt time.Time
	errMsg    string
	server    *tsnet.Server
	listener  net.Listener
	cancel    context.CancelFunc
	done      chan struct{}
	ready     chan struct{} // Closed when tunnel is connected and URL is available
}

// NewTailscaleProvider creates a new Tailscale tunnel provider.
// Note: cfg.Hostname should be set by the caller (loader sets default to AppSlug)
func NewTailscaleProvider(cfg *TailscaleConfig, dataDir string) (*TailscaleProvider, error) {
	// Set state directory
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(dataDir, "tailscale")
	}

	// Ensure state directory exists
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create tailscale state directory: %w", err)
	}

	return &TailscaleProvider{
		config:   cfg,
		stateDir: stateDir,
		status:   StatusDisabled,
	}, nil
}

// Name returns the provider name.
func (p *TailscaleProvider) Name() ProviderType {
	return ProviderTailscale
}

// Start initiates the Tailscale connection.
// It blocks until the tunnel is connected and the URL is available, or until timeout/error.
func (p *TailscaleProvider) Start(ctx context.Context, localAddr string) error {
	p.mu.Lock()
	if p.status == StatusConnected || p.status == StatusConnecting {
		p.mu.Unlock()
		return nil
	}

	p.status = StatusConnecting
	p.startedAt = time.Now()
	p.errMsg = ""
	p.ready = make(chan struct{})

	// Create a cancellable context for the tunnel
	tunnelCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})

	// Create tsnet server using hostname from config (defaulted to AppSlug by loader)
	srv := &tsnet.Server{
		Hostname: p.config.Hostname,
		Dir:      p.stateDir,
	}

	// Set auth key if provided
	if p.config.AuthKey != "" {
		srv.AuthKey = p.config.AuthKey
	}

	p.server = srv
	p.mu.Unlock()

	// Start the tunnel in a goroutine
	go p.runTunnel(tunnelCtx, localAddr)

	// Wait for tunnel to be ready (with timeout)
	select {
	case <-p.ready:
		// Tunnel is connected and URL is available
		return nil
	case <-p.done:
		// Tunnel goroutine exited (likely error)
		p.mu.RLock()
		errMsg := p.errMsg
		p.mu.RUnlock()
		if errMsg != "" {
			return fmt.Errorf("tunnel failed: %s", errMsg)
		}
		return fmt.Errorf("tunnel exited unexpectedly")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runTunnel runs the Tailscale tunnel.
func (p *TailscaleProvider) runTunnel(ctx context.Context, localAddr string) {
	defer close(p.done)

	// Start the tsnet server
	if err := p.server.Start(); err != nil {
		p.setError(fmt.Sprintf("failed to start tailscale: %v", err))
		return
	}

	// Get the local client to check status
	lc, err := p.server.LocalClient()
	if err != nil {
		p.setError(fmt.Sprintf("failed to get tailscale client: %v", err))
		return
	}

	// Wait for the connection to be ready (with timeout)
	connectionTimeout := time.After(60 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-connectionTimeout:
			p.setError("timeout waiting for tailscale connection (60s)")
			return
		default:
		}

		status, err := lc.Status(ctx)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if status.BackendState == "Running" {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Get the DNS name
	status, err := lc.Status(ctx)
	if err != nil {
		p.setError(fmt.Sprintf("failed to get tailscale status: %v", err))
		return
	}

	dnsName := status.Self.DNSName
	if dnsName != "" && dnsName[len(dnsName)-1] == '.' {
		dnsName = dnsName[:len(dnsName)-1]
	}

	// Set URL scheme based on mode
	scheme := "http"
	if p.config.Funnel || p.config.HTTPS {
		scheme = "https"
	}

	p.mu.Lock()
	p.publicURL = fmt.Sprintf("%s://%s", scheme, dnsName)
	p.status = StatusConnected
	p.mu.Unlock()

	// Create listener based on mode
	var ln net.Listener
	if p.config.Funnel {
		// Use Funnel for public access (requires TLS)
		ln, err = p.server.ListenFunnel("tcp", ":443")
		if err != nil {
			p.setError(fmt.Sprintf("failed to start funnel listener: %v", err))
			return
		}
	} else if p.config.HTTPS {
		// Use TLS for tailnet-only access (requires enabling HTTPS in Tailscale admin)
		ln, err = p.server.ListenTLS("tcp", ":443")
		if err != nil {
			p.setError(fmt.Sprintf("failed to start TLS listener: %v", err))
			return
		}
	} else {
		// For tailnet-only access, use plain HTTP (WireGuard already encrypts)
		// This avoids requiring HTTPS certificates to be enabled in Tailscale admin
		ln, err = p.server.Listen("tcp", ":80")
		if err != nil {
			p.setError(fmt.Sprintf("failed to start listener: %v", err))
			return
		}
	}

	p.mu.Lock()
	p.listener = ln
	ready := p.ready
	p.mu.Unlock()

	// Signal that we're ready AFTER listener is created
	if ready != nil {
		close(ready)
	}

	// Create reverse proxy to local server
	// Use 127.0.0.1 if the host is 0.0.0.0 (can't connect to 0.0.0.0)
	proxyAddr := localAddr
	if host, port, err := net.SplitHostPort(localAddr); err == nil && host == "0.0.0.0" {
		proxyAddr = net.JoinHostPort("127.0.0.1", port)
	}
	targetURL, err := url.Parse(fmt.Sprintf("http://%s", proxyAddr))
	if err != nil {
		p.setError(fmt.Sprintf("invalid local address: %v", err))
		_ = ln.Close()
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		// Log detailed error server-side for debugging, but don't expose to clients
		p.setError(fmt.Sprintf("proxy error: %v", err))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "Tunnel proxy error")
	}

	// Start HTTP server on the Tailscale listener
	httpSrv := &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run the server
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		if ctx.Err() == nil {
			p.setError(fmt.Sprintf("tunnel server error: %v", err))
		}
	}
}

// Stop gracefully shuts down the tunnel.
func (p *TailscaleProvider) Stop(ctx context.Context) error {
	p.mu.Lock()
	cancel := p.cancel
	server := p.server
	listener := p.listener
	done := p.done
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if listener != nil {
		_ = listener.Close()
	}

	if server != nil {
		_ = server.Close()
	}

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.mu.Lock()
	p.status = StatusDisabled
	p.publicURL = ""
	p.cancel = nil
	p.server = nil
	p.listener = nil
	p.done = nil
	p.ready = nil
	p.mu.Unlock()

	return nil
}

// Info returns current tunnel information.
func (p *TailscaleProvider) Info() Info {
	p.mu.RLock()
	defer p.mu.RUnlock()

	mode := "direct"
	if p.config.Funnel {
		mode = "funnel"
	}

	return Info{
		Provider:  ProviderTailscale,
		Status:    p.status,
		PublicURL: p.publicURL,
		Error:     p.errMsg,
		StartedAt: p.startedAt,
		Mode:      mode,
		IsPublic:  p.config.Funnel,
	}
}

// PublicURL returns the public URL when connected.
func (p *TailscaleProvider) PublicURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.publicURL
}

// IsPublic returns true if Funnel is enabled.
func (p *TailscaleProvider) IsPublic() bool {
	return p.config.Funnel
}

// setError sets the error status.
func (p *TailscaleProvider) setError(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = StatusError
	p.errMsg = msg
}
