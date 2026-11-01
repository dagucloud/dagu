// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cloudrelay connects a Dagu server to the Dagu Cloud relay, which
// holds the server's public webhook URLs and hands over what senders post to
// them, and carries calls from Dagu Cloud to the server.
package cloudrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
)

const (
	ticketPath = "/api/v1/servers/relay"
	// ticketReplyLimit bounds what is read of Dagu Cloud's answer.
	ticketReplyLimit = 64 << 10
)

// Ticket is what a server opens its relay connection with. It is good for a
// few minutes, so every connection asks for a new one.
type Ticket struct {
	Token string `json:"token"`
	// URL is the relay's WebSocket endpoint.
	URL string `json:"url"`
	// MaxHooks is the most public webhook URLs the server's plan allows.
	MaxHooks int `json:"max_hooks"`
}

// TicketRefusedError means Dagu Cloud refused a ticket to these credentials:
// the server is not connected, or not allowed public webhook URLs. Asking
// again soon does not help.
type TicketRefusedError struct {
	StatusCode int
	Message    string
}

func (e *TicketRefusedError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Dagu Cloud refused a relay ticket (%d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Dagu Cloud refused a relay ticket (%d)", e.StatusCode)
}

// purposeAccess asks for the ticket of a connection that takes calls from Dagu
// Cloud.
const purposeAccess = "access"

// RequestTicket asks Dagu Cloud for a relay ticket with the server's license
// credentials: the scheduler's, which takes webhooks.
func RequestTicket(ctx context.Context, client *http.Client, creds license.CloudCredentials) (Ticket, error) {
	return requestTicket(ctx, client, creds, "")
}

// RequestAccessTicket asks Dagu Cloud for the ticket of a connection that takes
// calls from Dagu Cloud. Dagu Cloud refuses it while the server's workspace has
// neither a plan nor a trial.
func RequestAccessTicket(ctx context.Context, client *http.Client, creds license.CloudCredentials) (Ticket, error) {
	return requestTicket(ctx, client, creds, purposeAccess)
}

func requestTicket(ctx context.Context, client *http.Client, creds license.CloudCredentials, purpose string) (Ticket, error) {
	fields := map[string]string{
		"license_id":       creds.LicenseID,
		"server_id":        creds.ServerID,
		"heartbeat_secret": creds.HeartbeatSecret,
	}
	if purpose != "" {
		fields["purpose"] = purpose
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return Ticket{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(creds.CloudURL, "/")+ticketPath, bytes.NewReader(body))
	if err != nil {
		return Ticket{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent())
	res, err := client.Do(req)
	if err != nil {
		return Ticket{}, fmt.Errorf("ask Dagu Cloud for a relay ticket: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	reply, err := io.ReadAll(io.LimitReader(res.Body, ticketReplyLimit))
	if err != nil {
		return Ticket{}, fmt.Errorf("read the relay ticket: %w", err)
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return Ticket{}, &TicketRefusedError{StatusCode: res.StatusCode, Message: replyMessage(reply)}
	default:
		return Ticket{}, fmt.Errorf("relay ticket: Dagu Cloud answered %d: %s", res.StatusCode, replyMessage(reply))
	}
	var ticket Ticket
	if err := json.Unmarshal(reply, &ticket); err != nil {
		return Ticket{}, fmt.Errorf("read the relay ticket: %w", err)
	}
	if ticket.Token == "" || ticket.URL == "" {
		return Ticket{}, errors.New("relay ticket: Dagu Cloud sent no token or URL")
	}
	return ticket, nil
}

// replyMessage is the message of an error Dagu Cloud sent, or the start of
// the reply.
func replyMessage(reply []byte) string {
	var body struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(reply, &body) == nil {
		if body.Message != "" {
			return body.Message
		}
		if body.Error != "" {
			return body.Error
		}
	}
	text := strings.TrimSpace(string(reply))
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

func userAgent() string {
	return "dagu-oss/" + config.Version
}
