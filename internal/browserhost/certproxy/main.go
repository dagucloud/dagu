// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
)

// Main is the cert-proxy command. It reads a Config as JSON from stdin,
// writes one line to stdout once the proxy accepts connections, closes
// stdout, and serves until the browser is gone or the process is told to
// stop.
func Main(ctx context.Context, stdin io.Reader, stdout io.WriteCloser) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := &readyWriter{w: stdout}
	var wire wireConfig
	if err := json.NewDecoder(stdin).Decode(&wire); err != nil {
		return out.fail(fmt.Errorf("read the certificate proxy config: %w", err))
	}
	if wire.Version != configVersion {
		return out.fail(fmt.Errorf("certificate proxy config version %d is not %d; the executable was likely replaced by another version", wire.Version, configVersion))
	}
	if err := Serve(ctx, wire.Config, out.ready); err != nil {
		return out.fail(err)
	}
	return nil
}

// readyWriter writes the one line a starting process reads, then closes
// stdout, which the proxy never writes again: the reader may be gone.
type readyWriter struct {
	w       io.WriteCloser
	written bool
}

func (r *readyWriter) ready(ready Ready) {
	r.write(readyLine{Ready: ready})
}

// fail reports err to the starting process, unless the proxy was already
// ready, and returns it.
func (r *readyWriter) fail(err error) error {
	r.write(readyLine{Error: err.Error()})
	return err
}

func (r *readyWriter) write(line readyLine) {
	if r.written {
		return
	}
	r.written = true
	data, _ := json.Marshal(line)
	_, _ = r.w.Write(append(data, '\n'))
	_ = r.w.Close()
}

// Serve runs the proxy until ctx ends or the browser at cfg.CDPURL is gone.
// It calls ready once the proxy accepts connections, before serving them.
func Serve(ctx context.Context, cfg Config, ready func(Ready)) error {
	var public *x509.CertPool
	if cfg.Guard {
		public = publicRoots()
	}
	return serve(ctx, cfg, public, ready)
}

// serve is Serve, with the guard passing the browser's own TLS to sites
// whose certificates the public authorities vouch for.
func serve(ctx context.Context, cfg Config, public *x509.CertPool, ready func(Ready)) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	entries := make([]*upstreamEntry, 0, len(cfg.Entries))
	for _, entry := range cfg.Entries {
		upstream, err := newUpstreamEntry(entry)
		if err != nil {
			return err
		}
		entries = append(entries, upstream)
	}
	authorities := make([]scopedAuthority, 0, len(cfg.TrustedCAs))
	for _, authority := range cfg.TrustedCAs {
		certs, err := trustedCertificates([]byte(authority.CAPEM))
		if err != nil {
			return fmt.Errorf("%s: %w", authority.Label, err)
		}
		authorities = append(authorities, scopedAuthority{hosts: authority.Hosts, certs: certs})
	}
	issuer, err := newLeafIssuer()
	if err != nil {
		return fmt.Errorf("create the certificate proxy key: %w", err)
	}
	system := cfg.SystemPAC
	evaluator, err := newPACEvaluator(system, systemPACEnv())
	if err != nil {
		// The browser runs the system script inside Dagu's; one that cannot
		// run would take the certificate hosts down with it.
		system = ""
		evaluator, _ = newPACEvaluator(system, systemPACEnv())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for the browser: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	var guard net.Listener
	guardPort := 0
	if cfg.Guard {
		if guard, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			_ = listener.Close()
			return fmt.Errorf("listen for the browser: %w", err)
		}
		guardPort = guard.Addr().(*net.TCPAddr).Port
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	manual := manualProxy(cfg.routedHosts())
	s := &server{
		ctx:         ctx,
		token:       cfg.Token,
		entries:     entries,
		trustedCAs:  authorities,
		pac:         evaluator,
		issuer:      issuer,
		events:      newEventLog(),
		verified:    newVerifiedHosts(),
		pending:     newPendingLog(),
		guardEntry:  newGuardEntry(cfg.Entries),
		publicRoots: public,
		verdicts:    newPassVerdicts(),
		tunnels:     map[net.Conn]struct{}{},
		guard:       guard,
		carryAll:    manual,
	}
	s.forward = newForwarder(s)
	script := composePAC(port, guardPort, cfg.routedHosts(), system)
	pacURL, served := pacLocation(script, controlURL(port, cfg.Token)+"/"+pacPath)
	if served {
		s.pacScript = script
	}
	httpServer := &http.Server{Handler: s, ReadHeaderTimeout: readHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	guardServer := &http.Server{Handler: guardHandler{s}, ReadHeaderTimeout: readHeaderTimeout}
	if guard != nil {
		// The guard's listener closes when the guard stops, which ends
		// only the guard's serving.
		go func() { _ = guardServer.Serve(guard) }()
	}
	ready(Ready{Port: port, SPKI: issuer.spki, PACURL: pacURL, GuardPort: guardPort, Manual: manual})

	grace := time.Duration(cfg.GraceSeconds) * time.Second
	if grace <= 0 {
		grace = defaultGrace
	}
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		watchBrowser(ctx, cfg.CDPURL, grace, browserPollInterval)
	}()
	var failure error
	select {
	case <-watched:
	case failure = <-serveErr:
	}
	cancel()
	<-watched
	_ = httpServer.Close()
	_ = guardServer.Close()
	s.closeTunnels()
	s.forward.closeIdle()
	if errors.Is(failure, http.ErrServerClosed) {
		return nil
	}
	return failure
}

// routedHosts returns the host patterns the browser sends to the proxy's
// certificate port: those of every entry and every trusted authority.
func (c Config) routedHosts() []string {
	var hosts []string
	for _, entry := range c.Entries {
		hosts = append(hosts, entry.Hosts...)
	}
	for _, authority := range c.TrustedCAs {
		hosts = append(hosts, authority.Hosts...)
	}
	return hosts
}

func (c Config) validate() error {
	switch {
	case c.Token == "":
		return errors.New("the certificate proxy config has no token")
	case c.CDPURL == "":
		return errors.New("the certificate proxy config names no browser")
	case len(c.Entries) == 0 && len(c.TrustedCAs) == 0 && !c.Guard:
		return errors.New("the certificate proxy config has no certificates and no guard")
	}
	for _, authority := range c.TrustedCAs {
		if len(authority.Hosts) == 0 {
			return fmt.Errorf("%s: hosts must list at least one host pattern", authority.Label)
		}
		for _, host := range authority.Hosts {
			if err := hostpattern.Validate(host); err != nil {
				return fmt.Errorf("%s: %w", authority.Label, err)
			}
		}
		if _, err := trustedCertificates([]byte(authority.CAPEM)); err != nil {
			return fmt.Errorf("%s: %w", authority.Label, err)
		}
	}
	return nil
}
