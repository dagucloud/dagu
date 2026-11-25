// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stepoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/oauth"
	"github.com/dagucloud/dagu/v2/internal/license"
)

const (
	cloudTokenPath = "/api/v1/servers/google/token"
	// cloudTokenMargin is how long before its expiry a Dagu Cloud token is
	// replaced.
	cloudTokenMargin = 5 * time.Minute
	// cloudReplyLimit bounds what is read of Dagu Cloud's answer.
	cloudReplyLimit = 64 << 10
)

var cloudHTTPClient = &http.Client{Timeout: 30 * time.Second}

type cloudTokenRequest struct {
	LicenseID       string   `json:"license_id"`
	ServerID        string   `json:"server_id"`
	HeartbeatSecret string   `json:"heartbeat_secret"`
	Account         string   `json:"account"`
	Scopes          []string `json:"scopes"`
}

type cloudTokenReply struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// cloudError is Dagu Cloud's refusal of a token request. ConnectURL, when
// set, is where the account is connected in Dagu Console.
type cloudError struct {
	status     int
	account    string
	Code       string `json:"code"`
	Message    string `json:"message"`
	ConnectURL string `json:"connect_url"`
}

func (e *cloudError) Error() string {
	reason := e.Code
	if reason == "" {
		reason = "HTTP " + strconv.Itoa(e.status)
	}
	msg := fmt.Sprintf("Dagu Cloud cannot issue a token for %s (%s)", e.account, reason)
	if message := strings.TrimRight(strings.TrimSpace(e.Message), "."); message != "" {
		msg += ": " + message
	}
	if e.ConnectURL != "" {
		msg += "; connect the account in Dagu Console: " + e.ConnectURL
	}
	return msg
}

// cloudSource returns the source of the tokens Dagu Cloud issues for account.
func cloudSource(creds license.CloudCredentials, account string, scopes []string) *oauth.Source {
	key := strings.Join(append([]string{creds.CloudURL, creds.LicenseID, creds.ServerID, creds.HeartbeatSecret, account}, scopes...), "\x00")
	return oauth.NewFetchSource(key, func(ctx context.Context) (*oauth2.Token, error) {
		return requestCloudToken(ctx, creds, account, scopes)
	})
}

func requestCloudToken(ctx context.Context, creds license.CloudCredentials, account string, scopes []string) (*oauth2.Token, error) {
	body, err := json.Marshal(cloudTokenRequest{
		LicenseID:       creds.LicenseID,
		ServerID:        creds.ServerID,
		HeartbeatSecret: creds.HeartbeatSecret,
		Account:         account,
		Scopes:          scopes,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(creds.CloudURL, "/")+cloudTokenPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dagu-oss/"+config.Version)
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask Dagu Cloud for an access token: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	reply, err := io.ReadAll(io.LimitReader(res.Body, cloudReplyLimit))
	if err != nil {
		return nil, fmt.Errorf("read the Dagu Cloud token reply: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		refusal := &cloudError{status: res.StatusCode, account: account}
		_ = json.Unmarshal(reply, refusal)
		return nil, refusal
	}
	var token cloudTokenReply
	if err := json.Unmarshal(reply, &token); err != nil {
		return nil, fmt.Errorf("read the Dagu Cloud token reply: %w", err)
	}
	if token.AccessToken == "" || token.ExpiresAt.IsZero() {
		return nil, errors.New("the Dagu Cloud token reply has no access_token or expires_at")
	}
	return &oauth2.Token{
		AccessToken: token.AccessToken,
		TokenType:   "Bearer",
		Expiry:      token.ExpiresAt.Add(-cloudTokenMargin),
	}, nil
}
