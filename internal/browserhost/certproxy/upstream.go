// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

const (
	// dialTimeout bounds each attempt to reach a server or proxy.
	dialTimeout = 15 * time.Second
	// maxProxyResponseBytes bounds the header of a proxy's CONNECT answer.
	maxProxyResponseBytes = 64 << 10
	// socksConnectOp names the failure of a SOCKS proxy to connect, as Go's
	// own SOCKS dialer does.
	socksConnectOp = "socks connect"
	// socks4Granted is the SOCKS4 reply code of an open connection.
	socks4Granted = 90
)

// PAC result keywords.
const (
	routeKeywordDirect = "DIRECT"
	routeKeywordProxy  = "PROXY"
	routeKeywordHTTPS  = "HTTPS"
	routeKeywordSOCKS  = "SOCKS"
	routeKeywordSOCKS5 = "SOCKS5"
)

// errProxyAuth reports an upstream proxy that asks for credentials.
var errProxyAuth = errors.New("asks for credentials, and proxies that need them are not supported for certificate hosts")

type routeKind int

const (
	routeDirect routeKind = iota
	// routeProxy tunnels through an HTTP proxy with CONNECT.
	routeProxy
	// routeHTTPS tunnels through an HTTP proxy reached over TLS.
	routeHTTPS
	routeSOCKS5
	// routeSOCKS4 tunnels through a SOCKS4 proxy, which takes an IPv4
	// address.
	routeSOCKS4
)

// route is one way to reach a server.
type route struct {
	kind routeKind
	// addr is the proxy's host and port.
	addr string
}

func (r route) String() string {
	switch r.kind {
	case routeProxy:
		return routeKeywordProxy + " " + r.addr
	case routeHTTPS:
		return routeKeywordHTTPS + " " + r.addr
	case routeSOCKS5:
		return routeKeywordSOCKS5 + " " + r.addr
	case routeSOCKS4:
		return routeKeywordSOCKS + " " + r.addr
	case routeDirect:
	}
	return routeKeywordDirect
}

// dialRoutes connects to target through the first of routes that reaches
// it. proxyTLS returns the TLS configuration for an HTTPS proxy on the way.
// As in Chrome, a route gives way to the next only when its proxy cannot be
// reached or a SOCKS proxy fails; an HTTP proxy that answers the CONNECT
// request without a tunnel, denying the site or asking for credentials,
// ends the search, so no later route bypasses it.
func dialRoutes(ctx context.Context, routes []route, target string, proxyTLS func(host string) *tls.Config) (net.Conn, route, error) {
	var errs []error
	for _, r := range routes {
		conn, answered, err := dialRoute(ctx, r, target, proxyTLS)
		if err == nil {
			return conn, r, nil
		}
		errs = append(errs, fmt.Errorf("through %s: %w", r, err))
		if answered || ctx.Err() != nil {
			break
		}
	}
	if len(errs) == 0 {
		return nil, route{}, errors.New("the device's proxy settings give it no route the certificate proxy supports")
	}
	return nil, route{}, errors.Join(errs...)
}

// dialRoute connects to target through r. It also reports whether an HTTP
// proxy answered the CONNECT request without opening a tunnel.
func dialRoute(ctx context.Context, r route, target string, proxyTLS func(host string) *tls.Config) (net.Conn, bool, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	switch r.kind {
	case routeProxy, routeHTTPS:
		conn, err := dialer.DialContext(ctx, "tcp", r.addr)
		if err != nil {
			return nil, false, err
		}
		if r.kind == routeHTTPS {
			host, _, _ := net.SplitHostPort(r.addr)
			tlsConn := tls.Client(conn, proxyTLS(host))
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, false, err
			}
			conn = tlsConn
		}
		if answered, err := connectTunnel(ctx, conn, r.addr, target); err != nil {
			_ = conn.Close()
			return nil, answered, err
		}
		return conn, false, nil
	case routeSOCKS5:
		socks, err := proxy.SOCKS5("tcp", r.addr, nil, dialer)
		if err != nil {
			return nil, false, err
		}
		conn, err := socks.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
		return conn, false, err
	case routeSOCKS4:
		conn, err := dialSOCKS4(ctx, dialer, r.addr, target)
		return conn, false, err
	case routeDirect:
	}
	conn, err := dialDirect(ctx, dialer, target)
	return conn, false, err
}

// connectTunnel asks the HTTP proxy at proxyAddr, reached over conn, for a
// tunnel to target. It also reports whether the proxy answered, which one
// that drops the connection first does not.
func connectTunnel(ctx context.Context, conn net.Conn, proxyAddr, target string) (bool, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dialTimeout)
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	status, err := requestTunnel(conn, target)
	if !stop() && err == nil {
		err = ctx.Err()
	}
	switch {
	case err != nil:
		return false, err
	case status == http.StatusProxyAuthRequired:
		return true, fmt.Errorf("the proxy %s %w", proxyAddr, errProxyAuth)
	case status != http.StatusOK:
		return true, fmt.Errorf("the proxy %s answered %d %s", proxyAddr, status, http.StatusText(status))
	}
	return false, conn.SetDeadline(time.Time{})
}

// dialSOCKS4 connects to target through the SOCKS4 proxy at proxyAddr. As
// Chrome does, it resolves target's host itself, to an IPv4 address. Every
// failure is the proxy's, after which Chrome tries the next route.
func dialSOCKS4(ctx context.Context, dialer *net.Dialer, proxyAddr, target string) (conn net.Conn, err error) {
	defer func() {
		if err != nil {
			err = &net.OpError{Op: socksConnectOp, Net: "tcp", Err: err}
		}
	}()
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, err
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	conn, err = dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dialTimeout)
	}
	_ = conn.SetDeadline(deadline)
	// Version 4, CONNECT, the port, the address, and an empty user ID.
	ip := addrs[0].Unmap().As4()
	request := binary.BigEndian.AppendUint16([]byte{4, 1}, uint16(portNumber)) //nolint:gosec // ParseUint bounds it to 16 bits
	request = append(request, ip[:]...)
	reply := make([]byte, 8)
	if _, err = conn.Write(append(request, 0)); err == nil {
		_, err = io.ReadFull(conn, reply)
	}
	if err == nil && reply[1] != socks4Granted {
		err = fmt.Errorf("the SOCKS proxy %s refused the connection to %s (code %d)", proxyAddr, target, reply[1])
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, conn.SetDeadline(time.Time{})
}

// requestTunnel sends a CONNECT request for target over conn and returns
// the status of the answer.
func requestTunnel(conn net.Conn, target string) (int, error) {
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		return 0, err
	}
	// A server behind the tunnel speaks only after the TLS client does, so
	// nothing past the answer is buffered. The body of a refusal is left
	// unread; the caller closes the connection instead.
	response, err := http.ReadResponse(bufio.NewReader(io.LimitReader(conn, maxProxyResponseBytes)), &http.Request{Method: http.MethodConnect}) //nolint:bodyclose // see above
	if err != nil {
		return 0, err
	}
	return response.StatusCode, nil
}
