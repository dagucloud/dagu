// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mailbox reads and organizes email in an IMAP mailbox or, through the
// Gmail API, in a Gmail mailbox.
package mailbox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"golang.org/x/oauth2"
)

// Security modes of Server.
const (
	SecurityTLS      = "tls"
	SecurityStartTLS = "starttls"
)

const (
	dialTimeout   = 30 * time.Second
	logoutTimeout = 5 * time.Second
	ioIdleTimeout = 2 * time.Minute
)

// Account is a mail account whose values are already resolved.
type Account struct {
	Server Server
	// GmailEndpoint is the Gmail API endpoint DialGmail uses; empty for
	// Google's.
	GmailEndpoint string
	Username      string
	// Password authenticates with LOGIN. Token is used when Password is empty.
	Password string
	Token    func(context.Context) (*oauth2.Token, error)
}

// Server is an IMAP server.
type Server struct {
	Host          string
	Port          string
	Security      string
	SkipTLSVerify bool
}

// Client is an authenticated IMAP connection.
type Client struct {
	imap       *imapclient.Client
	stop       func() bool
	specialUse map[imap.MailboxAttr]string
	// logoutTimeout bounds how long Close waits for the server's LOGOUT reply.
	logoutTimeout time.Duration
}

// Dial connects to the account's IMAP server and signs in. Canceling ctx
// closes the connection.
func Dial(ctx context.Context, account Account) (*Client, error) {
	return dial(ctx, account, ioIdleTimeout)
}

func dial(ctx context.Context, account Account, idle time.Duration) (*Client, error) {
	server := account.Server
	address := net.JoinHostPort(server.Host, server.Port)
	tlsConfig := &tls.Config{
		ServerName: server.Host,
		MinVersion: tls.VersionTLS12,
		// Operators opt in per server, for self-signed certificates.
		InsecureSkipVerify: server.SkipTLSVerify, //nolint:gosec
	}
	if server.Security != SecurityTLS && server.Security != SecurityStartTLS {
		return nil, fmt.Errorf("unsupported IMAP security %q", server.Security)
	}
	raw, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	// Both modes run TLS over this connection, so its bound covers every read
	// and write, including those of STARTTLS after the upgrade.
	conn := &idleConn{Conn: raw, timeout: idle}
	options := &imapclient.Options{TLSConfig: tlsConfig}

	var client *imapclient.Client
	if server.Security == SecurityTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		client = imapclient.New(tlsConn, options)
	} else {
		client, err = imapclient.NewStartTLS(conn, options)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("STARTTLS failed: %w", err)
		}
	}

	c := &Client{
		imap:          client,
		stop:          context.AfterFunc(ctx, func() { _ = client.Close() }),
		logoutTimeout: logoutTimeout,
	}
	if err := c.authenticate(ctx, account); err != nil {
		c.stop()
		_ = client.Close()
		return nil, err
	}
	return c, nil
}

// Close signs out and closes the connection. It waits a bounded time for the
// server to acknowledge the sign-out.
func (c *Client) Close() error {
	c.stop()
	loggedOut := make(chan struct{})
	go func() {
		// Closing the connection below also ends this wait.
		_ = c.imap.Logout().Wait()
		close(loggedOut)
	}()
	select {
	case <-loggedOut:
	case <-time.After(c.logoutTimeout):
	}
	return c.imap.Close()
}

func (c *Client) authenticate(ctx context.Context, account Account) error {
	if account.Password != "" {
		if err := c.imap.Login(account.Username, account.Password).Wait(); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
		return nil
	}
	if account.Token == nil {
		return errors.New("no password or OAuth token source")
	}
	token, err := account.Token(ctx)
	if err != nil {
		return err
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return errors.New("OAuth provider returned an empty access token")
	}
	if !c.imap.Caps().Has(imap.AuthCap("XOAUTH2")) {
		return errors.New("IMAP server does not offer AUTH=XOAUTH2")
	}
	if err := c.imap.Authenticate(&xoauth2Client{username: account.Username, token: token.AccessToken}); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	return nil
}

// idleConn fails a read or write that makes no progress within timeout. The
// IMAP client sets no deadline while waiting for a response to begin, so
// without this a server that stops answering would hold the step forever.
// The bound restarts on every read and write, so a slow but steady transfer
// of a large attachment still completes.
type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleConn) Read(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

func (c *idleConn) Write(b []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

// xoauth2Client implements the SASL XOAUTH2 mechanism.
type xoauth2Client struct {
	username string
	token    string
}

func (a *xoauth2Client) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + a.username + "\x01auth=Bearer " + a.token + "\x01\x01"), nil
}

// Next answers a failure challenge with an empty response, which ends the
// exchange so the server reports the failure.
func (a *xoauth2Client) Next([]byte) ([]byte, error) {
	return []byte{}, nil
}
