// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
)

// errPageSessionLost reports an operation whose connection to the page went
// away before it reported back, as when a click loads a new document.
var errPageSessionLost = errors.New("the browser lost its connection to the page")

// errPageContextLost is an errPageSessionLost whose operation lost the script
// context it ran in to a new document. That document may be a frame's, which
// leaves the page's own document as it was.
var errPageContextLost = fmt.Errorf("%w: a new document replaced its script context", errPageSessionLost)

// launcher starts or reattaches browser sessions.
type launcher interface {
	Launch(ctx context.Context, opts launchOptions) (engine, error)
	// Reattach connects to a browser that a previous step execution left
	// running.
	Reattach(ctx context.Context, handle browserHandle, opts launchOptions) (engine, error)
}

// engine drives one browser session.
type engine interface {
	// Goto loads url in the page and returns once the page settled on the
	// document it brought, the browser's error page included. A load that
	// brings no new document, such as a download, an answer with no
	// content, or a move to a fragment, returns at once.
	Goto(ctx context.Context, url string, timeout time.Duration) error
	// Act performs one action described in natural language. A message
	// naming a variable leaves out its value when secret reports the
	// variable, and always when secret is nil. Its error wraps
	// errPageSessionLost when the connection to the page went away before
	// the act reported back, and errPageContextLost when its script context
	// did; the act may or may not have taken effect.
	Act(ctx context.Context, instruction string, variables map[string]string, secret func(name string) bool, timeout time.Duration) (actOutcome, error)
	// Replay performs one recorded action without a model call and reports
	// whether it succeeded. An action whose element is not visible, or is
	// no longer the element it recorded, is not performed and does not
	// succeed. Its error wraps errPageSessionLost when
	// the connection to the page went away before the action reported back,
	// and errPageContextLost when its script context did; the action may or
	// may not have taken effect.
	Replay(ctx context.Context, action recordedAction, variables map[string]string, timeout time.Duration) (bool, error)
	// DocumentID identifies the document the active page shows. It changes
	// whenever the page loads a new document.
	DocumentID(ctx context.Context) (string, error)
	Extract(ctx context.Context, instruction string, schema json.RawMessage, timeout time.Duration) (json.RawMessage, error)
	// Observe returns the elements the model finds for an instruction, the
	// best match first, without acting on them.
	Observe(ctx context.Context, instruction string, timeout time.Duration) ([]recordedAction, error)
	// Upload gives files to a file input and reports how they reached it,
	// as uploadDirect, uploadPicker, or uploadNearby. With an empty
	// selector, the only file input of the page and the frames of its
	// origin it shows gets them. Otherwise the element at selector gets them when it is a
	// file input or holds exactly one; else a click on it opens a file
	// picker that gets them; else the one file input nearest it does.
	Upload(ctx context.Context, selector string, files []uploadFile, timeout time.Duration) (string, error)
	// WaitForSelector waits up to timeout for selector to match a visible
	// element: a CSS selector as SelectorVisible matches it, and a selector
	// that hops into frames with >>, or an XPath, as written.
	WaitForSelector(ctx context.Context, selector string, timeout time.Duration) error
	Screenshot(ctx context.Context) ([]byte, error)
	CurrentURL(ctx context.Context) (string, error)
	// PageText returns the visible text of the page and the frames of its
	// origin it shows.
	PageText(ctx context.Context) (string, error)
	// SelectorVisible reports whether a CSS selector matches a visible
	// element of the page or the frames of its origin it shows, inside open
	// shadow roots too.
	SelectorVisible(ctx context.Context, selector string) (bool, error)
	// Snapshot returns the active page's accessibility tree, the one an act
	// shows the model, without a model call.
	Snapshot(ctx context.Context) (pageSnapshot, error)
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
	// TakeCertificateEvents returns what the proxy presenting client
	// certificates reported since the previous call, or nothing when the
	// browser presents none. Its error reports that the proxy no longer
	// answers.
	TakeCertificateEvents(ctx context.Context) ([]certproxy.Event, error)
	// PageOutcome describes what the browser shows: a page, or the error
	// page it shows in place of one it could not load, and whether its host
	// answered with a bot check.
	PageOutcome(ctx context.Context) (pageOutcome, error)
	// PendingConnections returns the connections the certificate proxy has
	// not yet heard a site answer on, or nothing without a proxy.
	PendingConnections(ctx context.Context) ([]certproxy.Pending, error)
	// CertificateVerified reports whether the certificate proxy verified the
	// server certificate of host on a route it still carries; false without
	// a proxy.
	CertificateVerified(ctx context.Context, host string) (bool, error)
	// Writes returns what the write gate decided after the event numbered
	// after, or nothing without a gate. Its error reports that the gate no
	// longer answers, so the browser's writes go undecided.
	Writes(ctx context.Context, after int64) ([]writegate.Event, error)
	// BeginWrites tells the write gate which writes the operation starting
	// may send, for at most lease; EndWrites that it ended. Without a gate
	// they do nothing.
	BeginWrites(ctx context.Context, signatures []string, lease time.Duration) error
	EndWrites(ctx context.Context) error
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
	// ClientCertificates are presented, through a proxy, to the hosts they
	// name.
	ClientCertificates []certproxy.Entry
	// TrustedCAs add server trust, through the same proxy, to the hosts
	// they name.
	TrustedCAs []certproxy.TrustedCA
	// Guard sends the browser's other https: requests through the proxy
	// too.
	Guard bool
	// WriteGate decides the browser's writes from before its first page
	// loads; nil runs no gate.
	WriteGate *writeGateOptions
	// Warn reports what the launch could not set up as configured without
	// failing.
	Warn func(string)
	// BrowserDir keeps what launches learn about browser executables; empty
	// keeps nothing, so every headless launch learns it again.
	BrowserDir string
	// MaxOpen is the most browsers open at once under BrowserDir; a launch
	// waits while that many are. Zero sets no limit.
	MaxOpen int
	// Waiting reports, once, that the launch waits for another browser to
	// close.
	Waiting func()
}

// browserHandle locates a running browser.
type browserHandle struct {
	CDPURL       string
	ExtensionID  string
	ExtensionDir string
	// BrowserPID is the browser process ID, or zero when it is unknown.
	BrowserPID int
	// BrowserStartedAt is the browser process start time in Unix
	// milliseconds, or zero when it is unknown. With BrowserPID it tells the
	// browser apart from a later process that reuses its ID.
	BrowserStartedAt int64
	// CertProxy is the proxy presenting the browser's client certificates,
	// or nil when it presents none.
	CertProxy *certproxy.Handle
	// WriteGate is the gate deciding the browser's writes, or nil.
	WriteGate *writegate.Handle
}

// writeGateOptions configures the write gate a browser starts with.
type writeGateOptions struct {
	Mode   string
	Policy writegate.Policy
	// Secrets holds the values of the secrets the policy's sign-in rules
	// name.
	Secrets map[string]string
}

// pageOutcome is what the browser shows after it opened a page.
type pageOutcome struct {
	// Code is the network error code, such as ERR_BAD_SSL_CLIENT_AUTH_CERT,
	// of the error page the browser shows in place of a page it could not
	// load, or "" when it shows a page.
	Code string `json:"code"`
	// Host is the page's host, or that of the page that did not load when
	// the error page names it.
	Host string `json:"host"`
	// Status is the HTTP status the page came with, or zero when unknown.
	Status int    `json:"status"`
	Title  string `json:"title"`
	// Text is the start of the page's visible text.
	Text string `json:"text"`
	// BotCheck names the check the site answered with in place of the page,
	// such as browserhost.BotCheckCloudflare, or is empty.
	BotCheck string `json:"-"`
}

// pageSnapshot is the accessibility tree of a page.
type pageSnapshot struct {
	// Tree has one node per line, "[id] role: name", indented two spaces
	// per level.
	Tree string
	// URLs maps the IDs of link nodes to the absolute addresses they link
	// to.
	URLs map[string]string
	// XPaths maps the IDs of nodes to the XPaths that find them on the page.
	XPaths map[string]string
	// Attributes maps the IDs of controls the tree leaves unnamed to their
	// name attribute, or else their id.
	Attributes map[string]string
	URL        string
	Title      string
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
	// Target identifies the element at Selector as the page's accessibility
	// tree showed it before the action ran, so a replay acts only on that
	// element. It is nil when the element was not identified.
	Target *recordedTarget `json:"target,omitempty"`
}

// recordedTarget identifies the element an action acted on.
type recordedTarget struct {
	Role string `json:"role"`
	// Name holds a variable's placeholder in place of its value.
	Name string `json:"name,omitempty"`
	// Lookalikes counts the elements of the role and name on the page when
	// it had more than one, such as a Delete button in each row of a list,
	// and is 0 otherwise.
	Lookalikes int `json:"lookalikes,omitempty"`
	// Context tells the element apart from its lookalikes: a digest of the
	// text of its ancestor Depth levels up, the largest holding none of the
	// others. Both are empty when its parent already holds another.
	Context string `json:"context,omitempty"`
	Depth   int    `json:"depth,omitempty"`
	// ContextVariables names the variables whose placeholders the text
	// Context digests held, which a replay finds the element by.
	ContextVariables []string `json:"context_variables,omitempty"`
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
