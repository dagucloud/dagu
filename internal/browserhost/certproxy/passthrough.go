// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/x509roots/fallback/bundle"
	"golang.org/x/sync/singleflight"
)

// The guard passes the browser's own TLS to a site when the browser can
// never meet a request for a client certificate there, so the site sees
// the browser's handshake rather than the proxy's:
//
//	browser ── hello ──▶ guard ── hello ──▶ site      the answer is held
//	                       └─ probe: a handshake of its own with the site
//	pass:      the held answer ──▶ browser, then bytes both ways
//	otherwise: a new connection, with the browser's hello replayed into the
//	           proxy's TLS, as for every other host the guard carries

// probeRetryAfter is how long a site whose probe failed keeps the proxy's
// TLS before the guard probes it again.
const probeRetryAfter = time.Minute

// passVerdicts remembers, per host and port, whether the guard may pass the
// browser's TLS to the site.
type passVerdicts struct {
	flights singleflight.Group
	mu      sync.Mutex
	known   map[string]passVerdict
}

type passVerdict struct {
	pass bool
	// expires ends the verdict of a probe that failed; zero never does.
	expires time.Time
}

func newPassVerdicts() *passVerdicts {
	return &passVerdicts{known: map[string]passVerdict{}}
}

// lookup returns the verdict on key, if one holds at now.
func (v *passVerdicts) lookup(key string, now time.Time) (pass, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	verdict, ok := v.known[key]
	if !ok || !verdict.expires.IsZero() && now.After(verdict.expires) {
		return false, false
	}
	return verdict.pass, true
}

func (v *passVerdicts) remember(key string, verdict passVerdict) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.known[key] = verdict
}

// publicRoots returns the root certificates of Mozilla's program, which
// browsers trust wherever they run.
func publicRoots() *x509.CertPool {
	pool := x509.NewCertPool()
	for root := range bundle.Roots() {
		cert, err := x509.ParseCertificate(root.Certificate)
		if err != nil {
			continue
		}
		if root.Constraint != nil {
			pool.AddCertWithConstraint(cert, root.Constraint)
		} else {
			pool.AddCert(cert)
		}
	}
	return pool
}

// intercepted reports whether the guard is known to end the browser's TLS
// to host and port in the proxy.
func (s *server) intercepted(host, port string) bool {
	pass, ok := s.verdicts.lookup(net.JoinHostPort(host, port), time.Now())
	return ok && !pass
}

// passable reports whether the browser's TLS to host and port may pass to
// the site, probing it the first time. Connections to one site share its
// probe; one that stops waiting when ctx ends gets the proxy's TLS.
func (s *server) passable(ctx context.Context, host, port string) bool {
	key := net.JoinHostPort(host, port)
	if pass, ok := s.verdicts.lookup(key, time.Now()); ok {
		return pass
	}
	flight := s.verdicts.flights.DoChan(key, func() (any, error) {
		if pass, ok := s.verdicts.lookup(key, time.Now()); ok {
			return pass, nil
		}
		verdict := s.probe(host, port)
		s.verdicts.remember(key, verdict)
		return verdict.pass, nil
	})
	select {
	case result := <-flight:
		return result.Val.(bool)
	case <-ctx.Done():
		return false
	}
}

// probe makes a TLS handshake of the proxy's own with the site at host and
// port, sending nothing over it, to learn whether the browser's TLS may
// pass: the site chose TLS 1.3, asked for no client certificate, and showed
// a certificate a public root vouches for. Under TLS 1.3, nothing but a
// request for a certificate, which the guard cannot read in the browser's
// handshake, leads the browser to its certificate store. A site that only
// the system's or a ca_file's authorities vouch for keeps the proxy's TLS,
// which trusts them where the browser may not.
func (s *server) probe(host, port string) passVerdict {
	ctx, cancel := context.WithTimeout(s.ctx, handshakeTimeout)
	defer cancel()
	failed := passVerdict{expires: time.Now().Add(probeRetryAfter)}
	// The browser's own connection reports what reaching the site found.
	found := s.pac.routes(ctx, pacRequestURL(tlsScheme, host, port, "/"), host)
	raw, _, err := dialRoutes(ctx, found.routes, net.JoinHostPort(host, port), s.proxyTLS)
	if err != nil {
		return failed
	}
	defer func() { _ = raw.Close() }()
	var asks, public bool
	// No session cache: a resumed TLS 1.3 handshake never asks for a
	// certificate, so it would hide a site that does.
	conn := tls.Client(raw, &tls.Config{
		ServerName: host,
		NextProtos: []string{"h2", "http/1.1"},
		MinVersion: tls.VersionTLS12,
		// The probe sends no data; VerifyConnection judges the certificate.
		InsecureSkipVerify: true, //nolint:gosec // judged against public roots below
		VerifyConnection: func(state tls.ConnectionState) error {
			public = s.publiclyTrusted(host, state.PeerCertificates)
			// An error would end a TLS 1.3 handshake before the request
			// for a certificate, which follows, is seen.
			return nil
		},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			asks = true
			return &tls.Certificate{}, nil
		},
	})
	err = conn.HandshakeContext(ctx)
	switch {
	case asks:
		return passVerdict{}
	case err != nil:
		return failed
	}
	pass := conn.ConnectionState().Version == tls.VersionTLS13 && public
	if pass {
		s.verified.add(host, true)
	}
	return passVerdict{pass: pass}
}

// publiclyTrusted reports whether chain, as a server for host sent it,
// leads to a public root.
func (s *server) publiclyTrusted(host string, chain []*x509.Certificate) bool {
	if len(chain) == 0 || s.publicRoots == nil {
		return false
	}
	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{DNSName: host, Roots: s.publicRoots, Intermediates: intermediates})
	return err == nil
}

// passOrTunnel carries a connection the guard accepted for host: straight
// to the site when its verdict and its answer allow, otherwise through the
// proxy's TLS, as tunnel carries every other host. raw reaches the site,
// and the connection ends by deadline unless it passed.
func (s *server) passOrTunnel(client, raw net.Conn, via route, host, port string, deadline time.Time, pending int64) {
	defer s.pending.end(pending)
	s.pending.set(pending, PhaseHandshake)
	_ = client.SetReadDeadline(deadline)
	browserHello, err := readHello(client, handshakeTypeClientHello)
	_ = client.SetReadDeadline(time.Time{})
	replay := replayed(client, browserHello.records)
	if err != nil {
		// Nothing reached the site yet; the proxy's TLS answers whatever
		// the browser sent.
		s.tunnel(replay, raw, via, host, s.guardEntry, deadline, pending)
		return
	}
	ctx, cancel := context.WithDeadline(s.ctx, deadline)
	defer cancel()
	// The site gets the browser's hello at once, so its answer overlaps
	// the probe; the answer reaches the browser only once the site passes.
	var expired error
	if _, err := raw.Write(browserHello.records); err == nil && s.passable(ctx, host, port) {
		_ = raw.SetReadDeadline(deadline)
		answer, err := readHello(raw, handshakeTypeServerHello)
		_ = raw.SetReadDeadline(time.Time{})
		if err == nil && selectsTLS13(answer.message) {
			s.passThrough(client, raw, answer.records, pending)
			return
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			expired = context.DeadlineExceeded
		}
	}
	_ = raw.Close()
	if expired = cmp.Or(expired, ctx.Err()); expired != nil {
		_ = client.Close()
		s.reportHandshakeFailure(host, s.guardEntry, false, expired)
		return
	}
	raw, via, err = s.reach(ctx, pacRequestURL(tlsScheme, host, port, "/"), host, port, s.guardEntry)
	if err != nil {
		_ = client.Close()
		s.reportUnreachable(host, s.guardEntry, err)
		return
	}
	s.tunnel(replay, raw, via, host, s.guardEntry, deadline, pending)
}

// replayed returns conn, reading records first as if they were unread.
func replayed(conn net.Conn, records []byte) net.Conn {
	return &bufferedConn{Conn: conn, reader: bufio.NewReader(io.MultiReader(bytes.NewReader(records), conn))}
}

// passThrough relays bytes between the browser and the site, starting with
// the site's held answer. The connection waits on the site's answer from
// the browser's first bytes after it until the site sends again.
func (s *server) passThrough(client, raw net.Conn, answer []byte, pending int64) {
	if !s.track(client) {
		_ = client.Close()
		_ = raw.Close()
		return
	}
	defer s.untrack(client)
	if _, err := client.Write(answer); err != nil {
		_ = client.Close()
		_ = raw.Close()
		return
	}
	var asking sync.Once
	asked := make(chan struct{})
	go func() {
		buf := make([]byte, relayBufferBytes)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				asking.Do(func() {
					s.pending.set(pending, PhaseAnswer)
					close(asked)
				})
				if _, err := raw.Write(buf[:n]); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		_ = raw.SetReadDeadline(time.Now().Add(drainTimeout))
	}()
	buf := make([]byte, relayBufferBytes)
	for {
		n, err := raw.Read(buf)
		if n > 0 {
			select {
			case <-asked:
				s.pending.end(pending)
			default:
			}
			if _, err := client.Write(buf[:n]); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = client.Close()
	_ = raw.Close()
}
