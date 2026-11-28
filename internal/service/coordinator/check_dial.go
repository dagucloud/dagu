// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultProxyPort is the port gRPC dials on a proxy URL without one.
const defaultProxyPort = "443"

// proxyFromEnvironment selects the HTTPS proxy for a request, as gRPC does
// for its connections.
var proxyFromEnvironment = http.ProxyFromEnvironment

// dialCoordinator connects to address as gRPC does: through the HTTPS proxy
// of the environment when one applies, else directly. It returns the
// address of the proxy, empty without one.
func dialCoordinator(ctx context.Context, address string) (net.Conn, string, error) {
	var dialer net.Dialer
	proxy, err := proxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: address}})
	if err != nil {
		return nil, "", err
	}
	if proxy == nil {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		return conn, "", err
	}
	proxyAddr := proxyAddress(proxy.Host)
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, proxyAddr, err
	}
	if err := connectThrough(ctx, conn, address, proxy.User); err != nil {
		_ = conn.Close()
		return nil, proxyAddr, err
	}
	return conn, proxyAddr, nil
}

// proxyAddress returns host with a port, adding the one gRPC adds to a
// proxy without one.
func proxyAddress(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), defaultProxyPort)
}

// connectThrough asks the proxy on conn to tunnel to address. The proxy
// resolves address, so a name only it can resolve works.
func connectThrough(ctx context.Context, conn net.Conn, address string, user *url.Userinfo) error {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: address},
		Host:   address,
		Header: http.Header{},
	}
	if user != nil {
		password, _ := user.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}
	// A canceled context ends the exchange at once instead of at the
	// deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	if err := req.Write(conn); err != nil {
		stop()
		return fmt.Errorf("send CONNECT to the proxy: %w", err)
	}
	// The coordinator speaks only after the client does on TLS, and the h2c
	// probe reads nothing, so the reader holds no tunneled bytes.
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if !stop() {
		return fmt.Errorf("CONNECT to the proxy: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("read the proxy's answer to CONNECT: %w", err)
	}
	// The body of a refusal is left unread: the caller closes the
	// connection, and draining it could wait past a canceled context.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the proxy refused to connect to %s: %s", address, resp.Status)
	}
	return nil
}
