// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mailbox reads and organizes email in an IMAP mailbox.
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

const dialTimeout = 30 * time.Second

// Account is a mail account whose values are already resolved.
type Account struct {
	Server   Server
	Username string
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
}

// Dial connects to the account's IMAP server and signs in. Canceling ctx
// closes the connection.
func Dial(ctx context.Context, account Account) (*Client, error) {
	server := account.Server
	address := net.JoinHostPort(server.Host, server.Port)
	tlsConfig := &tls.Config{
		ServerName: server.Host,
		MinVersion: tls.VersionTLS12,
		// Operators opt in per server, for self-signed certificates.
		InsecureSkipVerify: server.SkipTLSVerify, //nolint:gosec
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	options := &imapclient.Options{TLSConfig: tlsConfig, Dialer: dialer}

	var client *imapclient.Client
	switch server.Security {
	case SecurityTLS:
		conn, err := (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		client = imapclient.New(conn, options)
	case SecurityStartTLS:
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		client, err = imapclient.NewStartTLS(conn, options)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("STARTTLS failed: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported IMAP security %q", server.Security)
	}

	c := &Client{
		imap: client,
		stop: context.AfterFunc(ctx, func() { _ = client.Close() }),
	}
	if err := c.authenticate(ctx, account); err != nil {
		c.stop()
		_ = client.Close()
		return nil, err
	}
	return c, nil
}

// Close signs out and closes the connection.
func (c *Client) Close() error {
	c.stop()
	_ = c.imap.Logout().Wait()
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
