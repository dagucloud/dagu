// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// errPageSessionLost reports an operation whose connection to the page went
// away before it reported back, as when a click loads a new document.
var errPageSessionLost = errors.New("the browser lost its connection to the page")

// launcher starts or reattaches browser sessions.
type launcher interface {
	Launch(ctx context.Context, opts launchOptions) (engine, error)
	// Reattach connects to a browser that a previous step execution left
	// running.
	Reattach(ctx context.Context, handle browserHandle, opts launchOptions) (engine, error)
}

// engine drives one browser session.
type engine interface {
	Goto(ctx context.Context, url string, timeout time.Duration) error
	// Act performs one action described in natural language. Its error wraps
	// errPageSessionLost when the connection to the page went away before
	// the act reported back; the act may or may not have taken effect.
	Act(ctx context.Context, instruction string, variables map[string]string, timeout time.Duration) (actOutcome, error)
	// Replay performs one recorded action without a model call and reports
	// whether it succeeded. Its error wraps errPageSessionLost when the
	// connection to the page went away before the action reported back; the
	// action may or may not have taken effect.
	Replay(ctx context.Context, action recordedAction, variables map[string]string, timeout time.Duration) (bool, error)
	// DocumentID identifies the document the active page shows. It changes
	// whenever the page loads a new document.
	DocumentID(ctx context.Context) (string, error)
	Extract(ctx context.Context, instruction string, schema json.RawMessage, timeout time.Duration) (json.RawMessage, error)
	WaitForSelector(ctx context.Context, selector string, timeout time.Duration) error
	Screenshot(ctx context.Context) ([]byte, error)
	CurrentURL(ctx context.Context) (string, error)
	// PageText returns the visible text of the page.
	PageText(ctx context.Context) (string, error)
	// SelectorVisible reports whether a CSS selector matches a visible
	// element.
	SelectorVisible(ctx context.Context, selector string) (bool, error)
	// WaitForDownloads returns the names of downloads completed since the
	// previous call, after allowing grace for one to begin and waiting up to
	// timeout for running ones to finish.
	WaitForDownloads(ctx context.Context, grace, timeout time.Duration) ([]string, error)
	// TakeDialogs returns the JavaScript dialogs the browser accepted since
	// the previous call.
	TakeDialogs() []dialog
	// TakeBlockedRequests returns the number of requests allowed_domains
	// blocked since the previous call, by host. Its error reports that
	// later blocked requests go uncounted.
	TakeBlockedRequests() (map[string]int, error)
	Handle() browserHandle
	// Detach releases the session while the browser keeps running.
	Detach(ctx context.Context) error
	// Close terminates the browser.
	Close(ctx context.Context) error
}

// launchOptions configures a browser session.
type launchOptions struct {
	Executable     string
	Headless       bool
	Viewport       *viewport
	Proxy          string
	UserDataDir    string
	DownloadsDir   string
	AllowedDomains []string
	// NoSandbox turns off the browser sandbox.
	NoSandbox bool
	Generate  generateFunc
}

// browserHandle locates a running browser.
type browserHandle struct {
	CDPURL       string
	ExtensionID  string
	ExtensionDir string
	// BrowserPID is the browser process ID, or zero when it is unknown.
	BrowserPID int
}

// dialog is a JavaScript dialog the browser accepted.
type dialog struct {
	Type    string
	Message string
}

// recordedAction is one deterministic action an act operation performed.
type recordedAction struct {
	Selector    string   `json:"selector"`
	Description string   `json:"description,omitempty"`
	Method      string   `json:"method,omitempty"`
	Arguments   []string `json:"arguments,omitempty"`
}

type actOutcome struct {
	Actions []recordedAction
	Message string
	Success bool
}

// generateFunc answers one structured model request from the browser runtime.
type generateFunc func(ctx context.Context, req generateRequest) (generateResponse, error)

type generateRequest struct {
	System      string
	Messages    []generateMessage
	SchemaName  string
	Schema      json.RawMessage
	Temperature *float64
}

type generateMessage struct {
	Role string
	Text string
}

type generateResponse struct {
	JSON  json.RawMessage
	Usage tokenUsage
}

type tokenUsage struct {
	Input  int
	Output int
}

func (u tokenUsage) total() int {
	return u.Input + u.Output
}

func (u tokenUsage) sub(other tokenUsage) tokenUsage {
	return tokenUsage{Input: u.Input - other.Input, Output: u.Output - other.Output}
}
