// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package google connects Google accounts with the authorization code
// grant. After consent it checks each service once: an MCP service lists
// its tools through the tool catalog, and a REST service reads a profile.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/connection/toolcatalog"
)

//nolint:gosec // Fixed provider endpoints, not credentials.
const (
	authURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL    = "https://oauth2.googleapis.com/token"
	userinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	revokeURL   = "https://oauth2.googleapis.com/revoke"
)

// Pages that the fixes link to.
const (
	clientsPage     = "https://console.cloud.google.com/auth/clients"
	audiencePage    = "https://console.cloud.google.com/auth/audience"
	enableAPIsPage  = "https://console.cloud.google.com/apis/enableflow?apiid="
	apiControlsPage = "https://admin.google.com/ac/owl"
)

// Consent errors that Google reports and that have a fix.
const (
	errAccessDenied        = "access_denied"
	errAdminPolicyEnforced = "admin_policy_enforced"
	errRedirectURIMismatch = "redirect_uri_mismatch"
	errInvalidClient       = "invalid_client"
)

const (
	// errInvalidToken is the revocation endpoint's answer for a token that
	// is already revoked or expired.
	errInvalidToken = "invalid_token"
	// clientIDSuffix ends every Google OAuth client ID.
	clientIDSuffix = ".apps.googleusercontent.com"
	// emailScope is requested by every connection. Google reports it back
	// as emailScopeAlias.
	emailScope      = "email"
	emailScopeAlias = "https://www.googleapis.com/auth/userinfo.email"
	// maxBody bounds the response body that an identity read or a REST
	// probe reads.
	maxBody = 64 << 10
	// absentID names no document, sheet, or presentation, so its read
	// answers Not Found once the token and the API work.
	absentID = "dagu-connection-check"
	// errorInfoType and reasonServiceDisabled mark the error of a Google
	// API that the client's project has not enabled.
	errorInfoType         = "type.googleapis.com/google.rpc.ErrorInfo"
	reasonServiceDisabled = "SERVICE_DISABLED"
	// statusNotFound is the status of a Google API's error for a resource
	// that does not exist.
	statusNotFound    = "NOT_FOUND"
	insufficientScope = "insufficient_scope"
	// testingLimit is how long Google keeps a refresh token of an app in
	// Testing status.
	testingLimit = 7 * 24 * time.Hour
)

// tokenFields are the fields of Google's token response.
var tokenFields = []string{
	"access_token", "token_type", "expires_in", "refresh_token",
	"refresh_token_expires_in", "scope", "id_token",
}

// restProbe is the read that checks a REST service.
type restProbe struct {
	url string
	// api is the ID of the API that the read calls.
	api string
	// absent marks a read of absentID, which passes with Not Found.
	absent bool
}

// restProbes read a profile of each service that has a REST API, or, for
// an API without one, a resource that does not exist.
var restProbes = map[string]restProbe{
	"gmail":    {url: "https://gmail.googleapis.com/gmail/v1/users/me/profile", api: "gmail.googleapis.com"},
	"calendar": {url: "https://www.googleapis.com/calendar/v3/users/me/calendarList?maxResults=1", api: "calendar-json.googleapis.com"},
	"drive":    {url: "https://www.googleapis.com/drive/v3/about?fields=user", api: "drive.googleapis.com"},
	"docs":     {url: "https://docs.googleapis.com/v1/documents/" + absentID, api: "docs.googleapis.com", absent: true},
	"sheets":   {url: "https://sheets.googleapis.com/v4/spreadsheets/" + absentID, api: "sheets.googleapis.com", absent: true},
	"slides":   {url: "https://slides.googleapis.com/v1/presentations/" + absentID, api: "slides.googleapis.com", absent: true},
	"chat":     {url: "https://chat.googleapis.com/v1/spaces?pageSize=1", api: "chat.googleapis.com"},
	"people":   {url: "https://people.googleapis.com/v1/people/me?personFields=names", api: "people.googleapis.com"},
}

var (
	_ connect.Provider    = (*Provider)(nil)
	_ connect.Advisor     = (*Provider)(nil)
	_ connect.Revoker     = (*Provider)(nil)
	_ connect.GrantSharer = (*Provider)(nil)
	_ connect.Guide       = (*Provider)(nil)
)

// Config is what a Provider needs.
type Config struct {
	// Catalog saves the tools that the probe of an MCP service lists.
	Catalog *toolcatalog.Reader

	// transport replaces http.DefaultTransport in tests.
	transport http.RoundTripper
}

// Provider connects Google accounts.
type Provider struct {
	catalog   *toolcatalog.Reader
	transport http.RoundTripper
	client    *http.Client
}

// New returns a provider over cfg.
func New(cfg Config) (*Provider, error) {
	if cfg.Catalog == nil {
		return nil, errors.New("google provider: a tool catalog reader is required")
	}
	transport := cfg.transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Provider{
		catalog:   cfg.Catalog,
		transport: transport,
		// No endpoint redirects, and net/http would forward the token on a
		// redirect to the same host or a subdomain over any scheme or port.
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// clientFile is a client JSON that the Google Cloud console downloads. The
// field that is set names the client's type.
type clientFile struct {
	Web       *clientInfo `json:"web"`
	Installed *clientInfo `json:"installed"`
}

type clientInfo struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris"`
}

// ParseClientFile returns the app of a Google client JSON as downloaded: a
// web client, or an installed one, which is a desktop client. A web
// client's redirect URIs must list the server callback that opts give.
// Google accepts any loopback port for a desktop client, so its list is
// not checked.
func (p *Provider) ParseClientFile(data []byte, opts connect.RedirectOptions) (*connection.App, error) {
	var file clientFile
	// The decoder's error may quote the file, which holds the secret.
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, invalid("the Google client file is not JSON")
	}
	info, clientType := file.Web, connection.ClientTypeWeb
	switch {
	case file.Web != nil && file.Installed != nil:
		return nil, invalid("the Google client file describes both a web and a desktop client")
	case file.Installed != nil:
		info, clientType = file.Installed, connection.ClientTypeDesktop
	case file.Web == nil:
		return nil, invalid("the Google client file describes neither a web nor a desktop client")
	}
	if info.ClientID == "" || info.ClientSecret == "" {
		return nil, invalid("the Google client file has no client ID or secret")
	}
	app := &connection.App{
		Provider:     connection.ProviderGoogle,
		Kind:         connection.AppKindBYO,
		ClientID:     info.ClientID,
		ClientSecret: info.ClientSecret,
		ClientType:   clientType,
	}
	if clientType == connection.ClientTypeWeb {
		r, err := connect.ChooseRedirect(app, opts)
		if err == nil && !slices.Contains(info.RedirectURIs, r.URI) {
			return nil, invalid("the client does not list the redirect URI %s: add it under Authorized redirect URIs on %s, then download the client JSON again", r.URI, clientPage(app))
		}
	}
	return app, nil
}

// Endpoint returns Google's authorization and token URLs.
func (p *Provider) Endpoint(*connection.App) (oauth2.Endpoint, error) {
	return oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams}, nil
}

// AuthorizationParams asks for offline access and a consent screen, so
// Google returns a refresh token on every connect.
func (p *Provider) AuthorizationParams(*connection.Connection) []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.ApprovalForce}
}

// Exchange trades code for tokens. Their granted scopes name email as it
// was requested, so email never shows as missing.
func (p *Provider) Exchange(ctx context.Context, cfg *oauth2.Config, _ *connection.Connection, code string, opts ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	token, err := cfg.Exchange(context.WithValue(ctx, oauth2.HTTPClient, p.client), code, opts...)
	if err != nil {
		return nil, err
	}
	return withRequestedNames(token), nil
}

// Identity reads the account's sub and email, and vouches for the email
// when Google reports it as verified.
func (p *Provider) Identity(ctx context.Context, _ *connection.App, token *oauth2.Token) (connect.Identity, error) {
	status, body, err := p.get(ctx, userinfoURL, token.AccessToken)
	if err != nil {
		return connect.Identity{}, err
	}
	if status != http.StatusOK {
		return connect.Identity{}, httpError("Google's userinfo endpoint", status, body)
	}
	var info struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified flag   `json:"email_verified"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return connect.Identity{}, fmt.Errorf("decode Google's userinfo: %w", err)
	}
	id := connect.Identity{Account: info.Email, AccountID: info.Sub}
	if info.EmailVerified {
		id.VouchedEmail = info.Email
	}
	return id, nil
}

// Probe checks conn's services together. A service whose scopes Google
// did not grant reports them without a call. With an agent app, a
// service with an MCP server lists its tools through the catalog; any
// other service reads its REST profile.
func (p *Provider) Probe(ctx context.Context, app *connection.App, conn *connection.Connection, token *oauth2.Token) []connection.Probe {
	type result struct {
		index int
		err   string
	}
	granted := grantedScopes(token, conn)
	results := make(chan result, len(conn.Services))
	for i, svc := range conn.Services {
		go func() {
			results <- result{index: i, err: p.probe(ctx, app, conn, svc, token, granted)}
		}()
	}
	probes := make([]connection.Probe, len(conn.Services))
	done := make([]bool, len(conn.Services))
	record := func(r result) {
		probes[r.index] = connection.Probe{Service: conn.Services[r.index].Name, Error: r.err}
		done[r.index] = true
	}
	for range conn.Services {
		select {
		case r := <-results:
			record(r)
		case <-ctx.Done():
			// Results that arrived with the deadline still count.
			for drained := false; !drained; {
				select {
				case r := <-results:
					record(r)
				default:
					drained = true
				}
			}
			for i, svc := range conn.Services {
				if !done[i] {
					probes[i] = connection.Probe{Service: svc.Name, Error: "the check did not finish: " + ctx.Err().Error()}
				}
			}
			return probes
		}
	}
	return probes
}

// probe checks svc and returns its error, or "" when it works.
func (p *Provider) probe(ctx context.Context, app *connection.App, conn *connection.Connection, svc connection.Service, token *oauth2.Token, granted []string) string {
	if missing := conn.MissingScopes(svc, granted); len(missing) > 0 {
		return fmt.Sprintf("Google did not grant %s. Reconnect and allow every access that the service asks for.", strings.Join(missing, ", "))
	}
	preset, ok := connection.LookupPreset(connection.ProviderGoogle, svc.Name)
	if !ok {
		return fmt.Sprintf("Google has no service %q", svc.Name)
	}
	if app.Agent && preset.MCPURL != "" {
		return p.probeMCP(ctx, app, conn, preset, token)
	}
	return p.probeREST(ctx, preset, token)
}

func (p *Provider) probeMCP(ctx context.Context, app *connection.App, conn *connection.Connection, preset connection.Preset, token *oauth2.Token) string {
	lister := toolcatalog.NewMCPLister(consentSource{token: token}, p.transport)
	_, err := p.catalog.Probe(ctx, conn, preset.Name, lister)
	var rejected *toolcatalog.RejectedError
	switch {
	case err == nil:
		return ""
	case !errors.As(err, &rejected) || rejected.Code == insufficientScope:
		return err.Error()
	}
	if e := parseAPIError([]byte(rejected.Body)); e.reason == reasonServiceDisabled {
		return e.disabled(preset.APIs)
	}
	return fmt.Sprintf("Google rejected the client, which may lack the agent flag: select \"Use this client for an AI-powered agent\" on %s. %v", clientPage(app), err)
}

func (p *Provider) probeREST(ctx context.Context, preset connection.Preset, token *oauth2.Token) string {
	probe, ok := restProbes[preset.Name]
	if !ok {
		return fmt.Sprintf("%s has no REST API, so only a client with the agent flag can use it, through its MCP server", preset.Name)
	}
	status, body, err := p.get(ctx, probe.url, token.AccessToken)
	if err != nil {
		return err.Error()
	}
	e := parseAPIError(body)
	switch {
	case status >= 200 && status < 300:
		return ""
	// Only the API's own answer for the absent resource passes, not a 404
	// of a wrong path or of Google's front end.
	case status == http.StatusNotFound && probe.absent && e.status == statusNotFound:
		return ""
	case e.reason == reasonServiceDisabled:
		return e.disabled([]string{probe.api})
	}
	return httpError(preset.Name, status, body).Error()
}

// ConsentFix returns the fix of RFC 025 for a consent error that Google
// reports.
func (p *Provider) ConsentFix(code string, app *connection.App, redirectURI string) string {
	switch code {
	case errAccessDenied:
		return "The account declined, or the app is in Testing status and the account is not one of its test users. To add a test user, open " + audiencePage + " and add the account under Test users."
	case errAdminPolicyEnforced:
		return "The Workspace admin blocks this client. Ask them to trust client ID " + app.ClientID + " under Security > Access and data control > API controls in the Admin console, " + apiControlsPage + "."
	case errRedirectURIMismatch:
		return "Register the redirect URI " + redirectURI + " under Authorized redirect URIs on " + clientPage(app) + "."
	case errInvalidClient:
		return "The client ID or secret is wrong, or the client was deleted. Download the client JSON again from " + clientsPage + " and upload it."
	}
	return ""
}

// RefreshTokenFix explains a refresh token that Google ends. Google reports
// the end for time-based access, which the account chose at consent. A
// token that ends within seven days may instead meet Google's limit for an
// app in Testing status, so only then does the fix name that limit.
func (p *Provider) RefreshTokenFix(remaining time.Duration) string {
	fix := "The account granted access for a limited time. Reconnect before it ends, and grant access without a time limit to keep the connection working."
	if remaining <= testingLimit {
		fix += " If the app is in Testing status, Google also ends its refresh tokens after seven days. Publishing the app to Production, or making it Internal, on " + audiencePage + " removes that limit."
	}
	return fix
}

// SetupLinks returns the Google Cloud console's pages for clients and
// their audience, and the Admin console's API controls.
func (p *Provider) SetupLinks() connect.SetupLinks {
	return connect.SetupLinks{Clients: clientsPage, ConsentScreen: audiencePage, AdminTrust: apiControlsPage}
}

// EnableAPIsLink returns the Google Cloud console's page that enables apis
// in the project that the admin picks.
func (p *Provider) EnableAPIsLink(apis []string) string {
	if len(apis) == 0 {
		return ""
	}
	return enableAPIsLink(apis)
}

// RevokeScope reports that revoking a Google refresh token may end the
// account's whole grant, as Google documents for its revocation endpoint:
// the tokens of every client in the client's project.
func (p *Provider) RevokeScope() connect.RevokeScope {
	return connect.RevokesGrant
}

// SameGrant reports whether a and b belong to one Google Cloud project:
// Google's revocation ends an account's tokens for every client of the
// project. A Google client ID starts with its project number. When either
// ID does not, the apps count as one grant.
func (p *Provider) SameGrant(a, b *connection.App) bool {
	pa, okA := projectNumber(a.ClientID)
	pb, okB := projectNumber(b.ClientID)
	return !okA || !okB || pa == pb
}

// projectNumber returns the project number that starts a Google client ID,
// <number>-<hash>.apps.googleusercontent.com.
func projectNumber(clientID string) (string, bool) {
	number, rest, ok := strings.Cut(clientID, "-")
	if !ok || number == "" || !strings.HasSuffix(rest, clientIDSuffix) {
		return "", false
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	return number, true
}

// Revoke revokes refreshToken at Google's revocation endpoint. A token
// that Google reports as invalid_token is already unusable, so its
// revocation succeeds.
func (p *Provider) Revoke(ctx context.Context, _ *connection.App, refreshToken string) error {
	form := url.Values{"token": {refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &e)
	switch {
	case e.Error == errInvalidToken:
		return nil
	case e.Error == "":
		return fmt.Errorf("revoke at Google: HTTP %d", resp.StatusCode)
	case e.Description == "":
		return fmt.Errorf("revoke at Google: HTTP %d: %s", resp.StatusCode, e.Error)
	}
	return fmt.Errorf("revoke at Google: HTTP %d: %s: %s", resp.StatusCode, e.Error, e.Description)
}

// get sends a GET with token to url, and returns the status and at most
// maxBody bytes of the body.
func (p *Provider) get(ctx context.Context, url, token string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// consentSource hands the access token of a consent, which is not live
// yet, to the catalog's listing. No newer token exists, so a rejection
// gets the same one.
type consentSource struct {
	token *oauth2.Token
}

var _ broker.Source = consentSource{}

func (s consentSource) Token(context.Context, *connection.Connection, string, time.Duration) (string, time.Duration, error) {
	var expiresIn time.Duration
	if !s.token.Expiry.IsZero() {
		expiresIn = time.Until(s.token.Expiry)
	}
	return s.token.AccessToken, expiresIn, nil
}

func (s consentSource) Rejected(ctx context.Context, conn *connection.Connection, resource, _ string, minTTL time.Duration) (string, time.Duration, error) {
	return s.Token(ctx, conn, resource, minTTL)
}

// withRequestedNames returns token with emailScopeAlias in its scope named
// emailScope, as it was requested.
func withRequestedNames(token *oauth2.Token) *oauth2.Token {
	scope, ok := token.Extra("scope").(string)
	if !ok {
		return token
	}
	scopes := strings.Fields(scope)
	for i, s := range scopes {
		if s == emailScopeAlias {
			scopes[i] = emailScope
		}
	}
	extra := make(map[string]any, len(tokenFields))
	for _, field := range tokenFields {
		if v := token.Extra(field); v != nil && v != "" {
			extra[field] = v
		}
	}
	extra["scope"] = strings.Join(scopes, " ")
	return token.WithExtra(extra)
}

// grantedScopes returns the scopes that token was granted. A response
// without scope grants the requested scopes (RFC 6749, section 5.1).
func grantedScopes(token *oauth2.Token, conn *connection.Connection) []string {
	if scope, ok := token.Extra("scope").(string); ok && scope != "" {
		return strings.Fields(scope)
	}
	return conn.RequestedScopes()
}

// apiError is what the JSON error of a Google API (AIP-193) tells. Its
// fields are empty for any other body.
type apiError struct {
	message string
	// status is the error's canonical code, such as NOT_FOUND.
	status string
	// reason and service come from its ErrorInfo detail. service is the
	// ID of the API that the reason is about, such as a disabled one.
	reason  string
	service string
}

func parseAPIError(body []byte) apiError {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Type     string            `json:"@type"`
				Reason   string            `json:"reason"`
				Metadata map[string]string `json:"metadata"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return apiError{}
	}
	parsed := apiError{message: e.Error.Message, status: e.Error.Status}
	for _, d := range e.Error.Details {
		if d.Type == errorInfoType {
			parsed.reason, parsed.service = d.Reason, d.Metadata["service"]
			break
		}
	}
	return parsed
}

// disabled says to enable the API that the error names, or else apis.
func (e apiError) disabled(apis []string) string {
	if e.service != "" {
		apis = []string{e.service}
	}
	return fmt.Sprintf("Enable %s in the client's Google Cloud project on %s. Google says: %s",
		strings.Join(apis, " and "), enableAPIsLink(apis), e.message)
}

func enableAPIsLink(apis []string) string {
	return enableAPIsPage + strings.Join(apis, ",")
}

// httpError reports an unexpected answer of what, with Google's message.
func httpError(what string, status int, body []byte) error {
	if message := parseAPIError(body).message; message != "" {
		return fmt.Errorf("%s answered HTTP %d: %s", what, status, message)
	}
	return fmt.Errorf("%s answered HTTP %d", what, status)
}

// flag is a JSON boolean that also reads the string "true", as Google's
// ID token example writes email_verified.
type flag bool

func (f *flag) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*f = flag(v)
	case string:
		*f = flag(v == "true")
	}
	return nil
}

func clientPage(app *connection.App) string {
	return clientsPage + "/" + app.ClientID
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", connection.ErrInvalid, fmt.Sprintf(format, args...))
}
