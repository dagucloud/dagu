// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// DefaultSessionIdleTimeout is how long a session's browser waits for
	// the next command unless the session says otherwise.
	DefaultSessionIdleTimeout = 30 * time.Minute
	// MaxSessionIdleTimeout bounds how long a session's browser waits.
	MaxSessionIdleTimeout = 24 * time.Hour
	// DefaultOutlineChars bounds the outline a command reports after an
	// operation, and DefaultDescribeChars the one it reports when asked.
	DefaultOutlineChars  = 4000
	DefaultDescribeChars = 8000
	// sessionDownloadGrace is how long a command waits, after an act, for
	// a download the act started to begin.
	sessionDownloadGrace = time.Second
	// maxSessionTreeChars bounds the raw accessibility tree describe returns.
	maxSessionTreeChars = 200_000
	sessionIDLength     = 10
	sessionDirMode      = 0o700
)

// Session error codes, which a command reports with its error.
const (
	CodeInvalidInput    = "invalid_input"
	CodeSessionNotFound = "session_not_found"
	CodeSessionBusy     = "session_busy"
	CodeSessionEnded    = "session_ended"
	CodeModelRequired   = "model_required"
	CodeProfileInUse    = "profile_in_use"
	CodeLaunchFailed    = "launch_failed"
	CodeOperationFailed = "operation_failed"
	CodeExportInvalid   = "export_invalid"
	// CodeFailed reports any other failure, such as a file that could not
	// be written.
	CodeFailed = "failed"
)

// SessionError is a failed session command, with a code a program can act
// on.
type SessionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *SessionError) Error() string { return e.Message }

// sessionIDPattern matches the IDs sessions are given: ten letters and
// digits from the base32 alphabet, in lowercase.
var sessionIDPattern = regexp.MustCompile(`^[a-z2-7]{10}$`)

var errNoModel = errors.New("this browser session has no model; open one with a model to act, extract, or judge a statement")

// Sessions runs browser sessions: browsers kept open between commands that
// each run one operation of a browser step, so a person or an agent can
// work a site the way a step will, look at the page after every
// operation, and keep what worked as a step.
type Sessions struct {
	launcher    launcher
	newProvider providerFactory
	now         func() time.Time
	// wait returns once d has passed on the clock now reads, or ctx ended.
	wait func(ctx context.Context, d time.Duration) error
}

// NewSessions returns the browser sessions of this host.
func NewSessions() *Sessions {
	return &Sessions{launcher: stagehandLauncher{}, now: time.Now, wait: sleepContext}
}

// sleepContext returns once d has passed, or ctx ended.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// SessionOptions configures the browser a session opens.
type SessionOptions struct {
	// URL is the page to open, if any.
	URL string
	// Profile keeps the browser's cookies and storage between sessions and
	// steps that name it.
	Profile        string
	Headless       bool
	Viewport       *SessionViewport
	Executable     string
	Proxy          string
	AllowedDomains []string
	// IdleTimeout closes the browser when no command arrives for that long;
	// zero means DefaultSessionIdleTimeout.
	IdleTimeout time.Duration
	// LLM is the model acts, extracts, and judged conditions ask; nil opens
	// a session without one.
	LLM *ir.LLMConfig
	// OutlineChars bounds the page outline the result reports; zero omits
	// it.
	OutlineChars int
}

// SessionViewport is the size of the browser's page area.
type SessionViewport struct {
	Width  int
	Height int
}

// SessionPageView is the page a command left the browser on.
type SessionPageView struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Outline describes the page; empty when not asked for.
	Outline          string `json:"outline,omitempty"`
	OutlineTruncated bool   `json:"outline_truncated,omitempty"`
}

// SessionRefusal is a site answering with a refusal or a bot check in place
// of the page the browser asked for.
type SessionRefusal struct {
	Host   string `json:"host"`
	Status int    `json:"status"`
	// BotCheck names the check the site answered with, such as cloudflare.
	BotCheck string `json:"bot_check,omitempty"`
	// Message says what happened in a sentence.
	Message string `json:"message"`
}

// OpenResult reports an opened session.
type OpenResult struct {
	ID string `json:"id"`
	// TLSVerified confirms the proxy verified the final HTTPS page's host.
	// False means no such evidence was available, not a certificate failure.
	TLSVerified bool `json:"tls_verified"`
	// CDPURL is the browser's DevTools address, where an application can
	// show the page and let a person act on it.
	CDPURL       string    `json:"cdp_url"`
	Headless     bool      `json:"headless"`
	Profile      string    `json:"profile,omitempty"`
	Model        string    `json:"model,omitempty"`
	IdleDeadline time.Time `json:"idle_deadline"`
	Warnings     []string  `json:"warnings"`
	// Refused is the refusal the page shows, if any.
	Refused *SessionRefusal `json:"refused,omitempty"`
	SessionPageView
}

// DoRequest runs one operation in a session.
type DoRequest struct {
	ID string
	// Operation is one item of a browser step's with.do.
	Operation json.RawMessage
	// Variables holds the values of the variables the operation uses.
	Variables map[string]SessionVariable
	// OutlineChars bounds the page outline the result reports; zero omits
	// it.
	OutlineChars int
}

// SessionAction is one action an act performed on the page.
type SessionAction struct {
	Selector    string   `json:"selector"`
	Description string   `json:"description,omitempty"`
	Method      string   `json:"method,omitempty"`
	Arguments   []string `json:"arguments,omitempty"`
}

// SessionDialog is a JavaScript dialog the browser accepted.
type SessionDialog struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// DoResult reports what an operation did.
type DoResult struct {
	ID string `json:"id"`
	// Index is the operation's place in the session's history, which
	// export refers to.
	Index  int    `json:"index"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Error is why the operation failed.
	Error   *SessionError   `json:"error,omitempty"`
	Actions []SessionAction `json:"actions"`
	// Act is the act a step writes for an element operation.
	Act string `json:"act,omitempty"`
	// Into is the into a step writes for an element upload.
	Into string `json:"into,omitempty"`
	// Recorded reports that a step exported from the session replays the
	// act's or the upload's actions.
	Recorded bool           `json:"recorded"`
	Outputs  map[string]any `json:"outputs,omitempty"`
	// Screenshot is the file a screenshot operation saved.
	Screenshot   string          `json:"screenshot,omitempty"`
	Downloads    []string        `json:"downloads"`
	Dialogs      []SessionDialog `json:"dialogs"`
	Blocked      map[string]int  `json:"blocked,omitempty"`
	Tokens       int             `json:"tokens"`
	DurationMS   int64           `json:"duration_ms"`
	Warnings     []string        `json:"warnings"`
	IdleDeadline time.Time       `json:"idle_deadline"`
	// Refused is the refusal the page shows after the operation, if any.
	Refused *SessionRefusal `json:"refused,omitempty"`
	SessionPageView
}

// DescribeRequest asks what a session's page shows.
type DescribeRequest struct {
	ID string
	// Find shows only the outline's entries containing it.
	Find string
	// MaxChars bounds the outline; zero means DefaultDescribeChars.
	MaxChars int
	// Tree returns the page's accessibility tree as the model sees it, in
	// place of the outline.
	Tree bool
	// Screenshot saves a screenshot of the page under this name.
	Screenshot string
}

// DescribeResult reports what a session's page shows.
type DescribeResult struct {
	ID string `json:"id"`
	SessionPageView
	Tree         string    `json:"tree,omitempty"`
	Matches      *int      `json:"matches,omitempty"`
	Screenshot   string    `json:"screenshot,omitempty"`
	IdleDeadline time.Time `json:"idle_deadline"`
	Warnings     []string  `json:"warnings"`
	// Refused is the refusal the page shows, if any.
	Refused *SessionRefusal `json:"refused,omitempty"`
}

// SessionSummary describes a session for a list.
type SessionSummary struct {
	ID string `json:"id"`
	// State is idle, busy while a command runs, or ended once the browser
	// closed.
	State        string     `json:"state"`
	URL          string     `json:"url"`
	Title        string     `json:"title"`
	CDPURL       string     `json:"cdp_url,omitempty"`
	Profile      string     `json:"profile,omitempty"`
	Headless     bool       `json:"headless"`
	Model        string     `json:"model,omitempty"`
	Operations   int        `json:"operations"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   time.Time  `json:"last_used_at"`
	IdleDeadline *time.Time `json:"idle_deadline,omitempty"`
}

// Session summary states.
const (
	SessionIdle  = "idle"
	SessionBusy  = "busy"
	SessionEnded = "ended"
)

// sessionDirs locates the browser state of this host.
func sessionDirs(ctx context.Context) (browserDir string, store *browserhost.Store, err error) {
	dataDir := cmnconfig.GetConfig(ctx).Paths.DataDir
	if dataDir == "" {
		return "", nil, errors.New("browser: the Dagu data directory is not configured")
	}
	browserDir = filepath.Join(dataDir, browserhost.DataDirName)
	return browserDir, browserhost.NewInteractiveStore(browserDir), nil
}

// Open launches a session's browser and leaves it waiting for commands.
func (s *Sessions) Open(ctx context.Context, opts SessionOptions) (OpenResult, error) {
	if err := opts.validate(); err != nil {
		return OpenResult{}, err
	}
	browserDir, store, err := sessionDirs(ctx)
	if err != nil {
		return OpenResult{}, err
	}
	idle := cmp.Or(opts.IdleTimeout, DefaultSessionIdleTimeout)
	masker := agentstep.NewMasker(nil, nil)
	bridge, generate, err := s.modelBridge(ctx, opts.LLM, masker)
	if err != nil {
		return OpenResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("model: %v", err)}
	}
	sweepCtx, cancel := context.WithTimeout(ctx, sweepBudget)
	_ = browserhost.SweepAll(sweepCtx, browserDir, s.now(), nil)
	cancel()
	certificates, warnings, err := loadClientCertificates(ctx, nil, "")
	if err != nil {
		return OpenResult{}, &SessionError{Code: CodeLaunchFailed, Message: err.Error()}
	}
	authorities, err := loadTrustedCAs(ctx)
	if err != nil {
		return OpenResult{}, &SessionError{Code: CodeLaunchFailed, Message: err.Error()}
	}

	id, workDir, err := reserveSession(store)
	if err != nil {
		return OpenResult{}, err
	}
	lock := store.SessionLock(id)
	if err := lock.TryLock(); err != nil {
		_ = os.RemoveAll(workDir)
		return OpenResult{}, fmt.Errorf("lock browser session: %w", err)
	}
	stop := agentstep.KeepLockAlive(ctx, lock)
	defer func() { stop(); _ = lock.Unlock() }()

	var lease *profileLease
	userDataDir := ""
	if opts.Profile != "" {
		if lease, err = acquireProfile(ctx, browserDir, opts.Profile, id, false); err != nil {
			_ = store.Purge(id)
			return OpenResult{}, sessionProfileError(err)
		}
		defer lease.release()
		userDataDir = lease.dir
	} else if userDataDir, err = os.MkdirTemp("", "dagu-browser-"); err != nil {
		_ = store.Purge(id)
		return OpenResult{}, fmt.Errorf("create browser profile directory: %w", err)
	}
	downloadsDir := filepath.Join(workDir, "downloads")
	if err := os.MkdirAll(downloadsDir, sessionDirMode); err != nil {
		_ = store.Purge(id)
		return OpenResult{}, err
	}
	launch := launchOptions{
		Executable:     opts.Executable,
		Headless:       opts.Headless,
		Proxy:          opts.Proxy,
		UserDataDir:    userDataDir,
		DownloadsDir:   downloadsDir,
		AllowedDomains: opts.AllowedDomains,
		NoSandbox:      cmnconfig.GetConfig(ctx).Browser.NoSandbox,
		Generate:       generate,

		ClientCertificates: certificates,
		TrustedCAs:         authorities,
		Guard:              guardsCertificates(ctx, certificates),
		Warn:               func(message string) { warnings = append(warnings, message) },
		BrowserDir:         browserDir,
	}
	if opts.Viewport != nil {
		launch.Viewport = &viewport{Width: opts.Viewport.Width, Height: opts.Viewport.Height}
	}
	eng, err := s.launcher.Launch(ctx, launch)
	if err != nil {
		if lease == nil {
			_ = os.RemoveAll(userDataDir)
		}
		_ = store.Purge(id)
		return OpenResult{}, &SessionError{Code: CodeLaunchFailed, Message: err.Error()}
	}
	handle := eng.Handle()
	now := s.now()
	state := sessionState{
		Version:     sessionStateVersion,
		CreatedAt:   now,
		LastUsedAt:  now,
		IdleTimeout: idle.String(),
		StartURL:    opts.URL,
		Browser: browserOptions{
			Headless:       &opts.Headless,
			Executable:     opts.Executable,
			Viewport:       launch.Viewport,
			Proxy:          opts.Proxy,
			AllowedDomains: opts.AllowedDomains,
			Profile:        opts.Profile,
		},
		LLM: opts.LLM,
	}
	record := browserhost.Record{
		ID:               id,
		CDPURL:           handle.CDPURL,
		ExtensionID:      handle.ExtensionID,
		ExtensionDir:     handle.ExtensionDir,
		UserDataDir:      userDataDir,
		OwnsUserDataDir:  lease == nil,
		Profile:          opts.Profile,
		DownloadsDir:     downloadsDir,
		BrowserPID:       handle.BrowserPID,
		BrowserStartedAt: handle.BrowserStartedAt,
		WorkDir:          workDir,
	}
	recordCertProxy(&record, handle.CertProxy)
	if err := saveSessionRecord(store, &record, state, browserhost.StateRunning, time.Time{}); err != nil {
		return OpenResult{}, errors.Join(err, closeSession(ctx, eng, store, record))
	}
	if warning := jobWarning(); warning != "" {
		warnings = append(warnings, warning)
	}
	op := s.operator(eng, bridge, state, sessionValues{}, workDir, &warnings)
	if opts.URL != "" {
		if _, err := op.gotoURL(ctx, -1, opts.URL, defaultOperationTimeout); err != nil {
			message := "open " + opts.URL + ": " + err.Error()
			if refused, ok := op.pageRefusal(ctx); ok {
				message += "; " + refused.String()
			}
			return OpenResult{}, errors.Join(&SessionError{Code: CodeOperationFailed, Message: message}, closeSession(ctx, eng, store, record))
		}
	}
	if err := op.checkPage(ctx, -1); err != nil {
		return OpenResult{}, errors.Join(&SessionError{Code: CodeOperationFailed, Message: err.Error()}, closeSession(ctx, eng, store, record))
	}
	view, pageURL := s.view(ctx, eng, masker, opts.OutlineChars, &warnings)
	refused := pageRefusal(ctx, op, sessionPage{}, pageURL)
	state.Page = sessionPage{URL: pageURL, Title: view.Title, Refused: refused}
	deadline := now.Add(idle)
	if err := s.park(ctx, eng, store, &record, state, deadline); err != nil {
		return OpenResult{}, err
	}
	return OpenResult{
		ID:              id,
		TLSVerified:     op.tlsVerified(ctx, pageURL),
		CDPURL:          record.CDPURL,
		Headless:        opts.Headless,
		Profile:         opts.Profile,
		Model:           state.modelLabel(),
		IdleDeadline:    deadline,
		Warnings:        nonNil(warnings),
		Refused:         sessionRefusal(refused, masker),
		SessionPageView: view,
	}, nil
}

func (o SessionOptions) validate() error {
	invalid := func(format string, args ...any) error {
		return &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf(format, args...)}
	}
	if o.IdleTimeout < 0 || o.IdleTimeout > MaxSessionIdleTimeout {
		return invalid("the idle timeout must be between 0 and %s", MaxSessionIdleTimeout)
	}
	if o.Viewport != nil && (o.Viewport.Width < 1 || o.Viewport.Height < 1) {
		return invalid("the viewport must be at least 1x1")
	}
	if o.Profile != "" && !agentstep.FileNamePattern.MatchString(o.Profile) {
		return invalid("profile %q must match %s", o.Profile, agentstep.FileNamePattern)
	}
	for _, pattern := range o.AllowedDomains {
		if err := validateDomainPattern(pattern); err != nil {
			return invalid("%v", err)
		}
	}
	if o.URL != "" {
		if err := checkAllowedDomain(o.URL, o.AllowedDomains); err != nil {
			return invalid("%v", err)
		}
	}
	if o.OutlineChars < 0 {
		return invalid("the outline size must not be negative")
	}
	return nil
}

// modelBridge returns the bridge to the session's model and the function
// the browser runtime asks it through. Without a model, every request
// fails.
func (s *Sessions) modelBridge(ctx context.Context, cfg *ir.LLMConfig, masker *masking.Masker) (*modelBridge, generateFunc, error) {
	if _, ok := runtime.LookupEnv(ctx); !ok {
		ctx = runtime.WithEnv(ctx, runtime.Env{Scope: sessionModelEnv(cfg)})
	}
	if cfg == nil {
		noModel := func(context.Context, generateRequest) (generateResponse, error) {
			return generateResponse{}, errNoModel
		}
		return &modelBridge{masker: masker, runCtx: ctx}, noModel, nil
	}
	bridge, err := newModelBridge(ctx, cfg, masker, io.Discard, s.newProvider)
	if err != nil {
		return nil, nil, err
	}
	return bridge, bridge.generate, nil
}

// envReference matches a ${NAME} reference, naming the variable.
var envReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// sessionModelEnv returns the variables a session's model settings read:
// each model's API key variable and the variables its base URL names, from
// the process's environment. A step declares these under its DAG's secrets
// and env; a session has no DAG, and the process's other variables stay out
// of reach, as they do for a step.
func sessionModelEnv(cfg *ir.LLMConfig) *cmnvalue.EnvScope {
	scope := cmnvalue.NewEnvScope(nil, false)
	if cfg == nil {
		return scope
	}
	for _, model := range cfg.GetModels() {
		effective := runtime.EffectiveLLMConfig(cfg, model)
		var names []string
		keyName := effective.APIKeyName
		if keyName == "" {
			if provider, err := llmpkg.ParseProviderType(effective.Provider); err == nil {
				keyName = llmpkg.DefaultAPIKeyEnvVar(provider)
			}
		}
		for _, m := range envReference.FindAllStringSubmatch(runtime.NormalizeEnvVarExpr(keyName)+effective.BaseURL, -1) {
			names = append(names, m[1])
		}
		for _, name := range names {
			if value, ok := os.LookupEnv(name); ok {
				scope = scope.WithEntry(name, value, cmnvalue.EnvSourceSecret)
			}
		}
	}
	return scope
}

// operator returns the operator that runs a command's operation.
func (s *Sessions) operator(eng engine, bridge *modelBridge, state sessionState, values sessionValues, workDir string, warnings *[]string) *operator {
	variables := values.all
	if variables == nil {
		variables = map[string]string{}
	}
	return &operator{
		eng:            eng,
		bridge:         bridge,
		variables:      variables,
		secret:         func(name string) bool { return holdsSecret(variables[name], values.secrets) },
		allowedDomains: state.Browser.AllowedDomains,
		recorder:       sessionRecorder{},
		shots:          agentstep.NewArtifactStore(workDir, "", "screenshots"),
		warn: func(_ int, message string) {
			*warnings = append(*warnings, message)
		},
		certificateProxy: eng.Handle().CertProxy != nil,
		certificateEvent: func(_ int, event certproxy.Event) {
			if event.Kind != certproxy.EventConnect {
				*warnings = append(*warnings, "browser TLS: "+describeCertificateEvent(event))
			}
		},
	}
}

// sessionRecorder lets acts record the page they start on without
// replaying, since a session works the page as it is, except the action an
// element operation chose, which its act replays.
type sessionRecorder struct {
	preset []recordedAction
}

func (r sessionRecorder) lookup(string) ([]recordedAction, bool) { return r.preset, len(r.preset) > 0 }
func (sessionRecorder) stage(string, []recordedAction)           {}
func (sessionRecorder) drop(string)                              {}

// checkOperation refuses an operation the session cannot run as given: an
// act using a variable no value was given for, one needing a model the
// session lacks, or one whose words hold a secret's value. An element
// operation's act replays the action chosen for it, so it needs no model.
func checkOperation(op operation, state sessionState, values sessionValues, element bool) error {
	if op.Act != nil {
		for _, name := range agentstep.VariableReferences(op.Act.Instruction) {
			if _, ok := values.all[name]; !ok {
				return &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("the instruction uses %%%s%%, which no variable sets; give it under variables", name)}
			}
		}
	}
	if op.needsModel() && state.LLM == nil && !element {
		return &SessionError{Code: CodeModelRequired, Message: errNoModel.Error()}
	}
	if err := agentstep.CheckSecrets(executorType, []agentstep.OperationTexts{{Kind: op.kind(), Texts: op.promptTexts()}}, values.secrets); err != nil {
		return &SessionError{Code: CodeInvalidInput, Message: err.Error()}
	}
	return nil
}

// Do runs one operation in a session. An operation that fails leaves the
// session open: the result reports the failure and the error is a
// SessionError with CodeOperationFailed.
func (s *Sessions) Do(ctx context.Context, req DoRequest) (DoResult, error) {
	element, isElement, err := parseElementOperation(req.Operation)
	if err != nil {
		return DoResult{}, err
	}
	var op operation
	if !isElement {
		if op, err = parseSessionOperation(req.Operation); err != nil {
			return DoResult{}, err
		}
	}
	if req.OutlineChars < 0 {
		return DoResult{}, &SessionError{Code: CodeInvalidInput, Message: "the outline size must not be negative"}
	}
	use, err := s.use(ctx, req.ID)
	if err != nil {
		return DoResult{}, err
	}
	defer use.release()
	state := use.state
	if len(state.Ops) >= maxSessionOperations {
		return DoResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("the session has run %d operations; export it or close it", maxSessionOperations)}
	}
	values, err := resolveVariables(&state, req.Variables)
	if err != nil {
		return DoResult{}, err
	}
	if !isElement {
		if err := checkOperation(op, state, values, false); err != nil {
			return DoResult{}, err
		}
	}
	masker := agentstep.NewMasker(values.secrets, nil)
	bridge, generate, err := s.modelBridge(ctx, state.LLM, masker)
	if err != nil {
		return DoResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("model: %v", masker.MaskString(err.Error()))}
	}
	eng, err := use.attach(ctx, generate)
	if err != nil {
		return DoResult{}, err
	}
	// An element operation is the act or upload it stands for, with its
	// action as its recording.
	var preset []recordedAction
	if isElement {
		snap, err := eng.Snapshot(ctx)
		if err != nil {
			return DoResult{}, &SessionError{Code: CodeOperationFailed, Message: "read the page: " + masker.MaskString(err.Error())}
		}
		instruction, action, err := elementAct(snap, element, values.all)
		if err != nil {
			return DoResult{}, &SessionError{Code: CodeInvalidInput, Message: masker.MaskString(err.Error())}
		}
		if op, err = element.operation(instruction); err != nil {
			return DoResult{}, err
		}
		preset = []recordedAction{action}
		if err := checkOperation(op, state, values, true); err != nil {
			return DoResult{}, err
		}
	}

	began := time.Now()
	index := len(state.Ops)
	var warnings []string
	operator := s.operator(eng, bridge, state, values, use.record.WorkDir, &warnings)
	operator.recorder = sessionRecorder{preset: preset}
	urlBefore, _ := eng.CurrentURL(ctx)
	if state.Page.URL != "" && urlBefore != state.Page.URL {
		warnings = append(warnings, fmt.Sprintf("the page changed since the last command, from %s to %s", state.Page.URL, urlBefore))
	}
	if op.Extract != nil {
		warnings = append(warnings, extractWarnings(*op.Extract)...)
	}
	result := DoResult{ID: req.ID, Index: index, Kind: op.kind(), Status: OperationDone}
	switch {
	case isElement && op.Act != nil:
		result.Act = op.Act.Instruction
	case isElement && op.Upload != nil:
		result.Into = op.Upload.Into
	}
	canonical, err := json.Marshal(op)
	if err != nil {
		return DoResult{}, err
	}
	entry := sessionOp{Kind: op.kind(), Op: canonical, Status: OperationDone, URLBefore: urlBefore, At: s.now()}
	if op.Act != nil {
		entry.Variables = agentstep.VariableReferences(op.Act.Instruction)
	}
	var opErr error
	skipped := false
	if op.When != nil {
		holds, reason, err := operator.await(ctx, *op.When, op.When.window(0), op.timeout())
		switch {
		case err != nil:
			opErr = fmt.Errorf("evaluate when: %w", err)
		case !holds:
			skipped = true
			result.Status, result.Detail = OperationSkipped, reason
			entry.Status = OperationSkipped
		}
	}
	if opErr == nil && !skipped {
		var done opResult
		done, opErr = operator.runOperation(ctx, index, op)
		if opErr == nil {
			opErr = operator.checkPage(ctx, index)
		}
		if opErr == nil {
			result.Detail = done.Detail
			if done.PageURL != "" {
				entry.URLBefore = done.PageURL
			}
			result.Actions = sessionActions(done.Actions, masker)
			if op.Act != nil || op.Upload != nil {
				entry.Recordable = done.PageURL != "" && len(done.Actions) > 0
				if entry.Recordable && holdsValue(done.Actions, values.all) {
					entry.Recordable = false
					warnings = append(warnings, "the operation's actions hold a variable's value, so an exported step asks the model for this operation on every run")
				}
				// Only actions an export replays are kept, and those hold no
				// variable's value.
				if entry.Recordable {
					entry.Actions = done.Actions
				}
				result.Recorded = entry.Recordable
			}
			if op.Extract != nil {
				result.Outputs = done.Values
				entry.Outputs = slices.Sorted(maps.Keys(done.Values))
			}
			if op.Screenshot != "" && len(done.Files) > 0 {
				result.Screenshot = filepath.Join(use.record.WorkDir, done.Files[0])
			}
		}
	}
	if opErr != nil {
		message := masker.MaskString(opErr.Error())
		result.Status = OperationFailed
		result.Error = &SessionError{Code: CodeOperationFailed, Message: fmt.Sprintf("%s failed: %s", op.kind(), message)}
		entry.Status, entry.Error = OperationFailed, message
	}
	for _, d := range eng.TakeDialogs() {
		result.Dialogs = append(result.Dialogs, SessionDialog{Type: d.Type, Message: masker.MaskString(d.Message)})
	}
	if blocked, err := eng.TakeBlockedRequests(); err == nil && len(blocked) > 0 {
		result.Blocked = blocked
	}
	if op.Act != nil || op.Goto != "" {
		grace := time.Duration(0)
		if op.Act != nil {
			grace = sessionDownloadGrace
		}
		names, err := eng.WaitForDownloads(ctx, grace, op.timeout())
		for _, name := range names {
			result.Downloads = append(result.Downloads, filepath.Join(use.record.DownloadsDir, name))
		}
		if err != nil {
			warnings = append(warnings, "wait for downloads: "+masker.MaskString(err.Error()))
		}
	}
	view, pageURL := s.view(ctx, eng, masker, req.OutlineChars, &warnings)
	// Reading a browser that stopped answering would only wait for it again,
	// so a page still at the address last seen keeps the refusal seen there.
	refused := state.Page.Refused
	switch {
	case !errors.Is(opErr, errBrowserUnresponsive):
		refused = pageRefusal(ctx, operator, state.Page, pageURL)
	case pageURL != state.Page.URL:
		refused = nil
	}
	if refused != nil && result.Error != nil {
		note := masker.MaskString(refused.String())
		result.Error.Message += "; " + note
		entry.Error += "; " + note
	}
	result.Refused = sessionRefusal(refused, masker)
	entry.URLAfter = pageURL
	state.Ops = append(state.Ops, entry)
	state.Page = sessionPage{URL: pageURL, Title: view.Title, Refused: refused}
	usage := bridge.totals()
	state.Usage.Input += usage.Input
	state.Usage.Output += usage.Output
	result.Tokens = usage.total()
	result.DurationMS = time.Since(began).Milliseconds()
	if result.Detail != "" {
		result.Detail = masker.MaskString(result.Detail)
	}
	result.Outputs = maskOutputs(masker, result.Outputs)
	result.Actions = nonNil(result.Actions)
	result.Downloads = nonNil(result.Downloads)
	result.Dialogs = nonNil(result.Dialogs)
	result.SessionPageView = view
	result.IdleDeadline = s.now().Add(state.idleTimeout())
	if err := use.park(ctx, state, result.IdleDeadline); err != nil {
		return result, err
	}
	result.Warnings = nonNil(maskAll(masker, warnings))
	if result.Error != nil {
		return result, result.Error
	}
	return result, nil
}

// Describe reports what a session's page shows, without asking a model.
func (s *Sessions) Describe(ctx context.Context, req DescribeRequest) (DescribeResult, error) {
	if req.Screenshot != "" && !agentstep.FileNamePattern.MatchString(req.Screenshot) {
		return DescribeResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("screenshot name %q must match %s", req.Screenshot, agentstep.FileNamePattern)}
	}
	if req.MaxChars < 0 {
		return DescribeResult{}, &SessionError{Code: CodeInvalidInput, Message: "the outline size must not be negative"}
	}
	use, err := s.use(ctx, req.ID)
	if err != nil {
		return DescribeResult{}, err
	}
	defer use.release()
	state := use.state
	values, err := resolveVariables(&state, nil)
	if err != nil {
		return DescribeResult{}, err
	}
	masker := agentstep.NewMasker(values.secrets, nil)
	eng, err := use.attach(ctx, func(context.Context, generateRequest) (generateResponse, error) {
		return generateResponse{}, errNoModel
	})
	if err != nil {
		return DescribeResult{}, err
	}
	var warnings []string
	result := DescribeResult{ID: req.ID}
	snap, err := eng.Snapshot(ctx)
	if err != nil {
		_ = use.park(ctx, state, s.now().Add(state.idleTimeout()))
		return DescribeResult{}, &SessionError{Code: CodeOperationFailed, Message: "read the page: " + masker.MaskString(err.Error())}
	}
	result.URL, result.Title = masker.MaskString(snap.URL), masker.MaskString(snap.Title)
	if req.Tree {
		tree := snap.Tree
		if len(tree) > maxSessionTreeChars {
			tree = tree[:maxSessionTreeChars]
			warnings = append(warnings, fmt.Sprintf("the tree is cut at %d characters", maxSessionTreeChars))
		}
		result.Tree = masker.MaskString(tree)
	} else {
		text, truncated, matches := renderOutline(snap, outlineOptions{Find: req.Find, MaxChars: cmp.Or(req.MaxChars, DefaultDescribeChars)})
		result.Outline, result.OutlineTruncated = masker.MaskString(text), truncated
		if req.Find != "" {
			result.Matches = &matches
		}
	}
	if req.Screenshot != "" {
		data, err := eng.Screenshot(ctx)
		if err == nil {
			var rel string
			rel, err = agentstep.NewArtifactStore(use.record.WorkDir, "", "screenshots").WriteScreenshot(req.Screenshot, data)
			result.Screenshot = filepath.Join(use.record.WorkDir, rel)
		}
		if err != nil {
			warnings = append(warnings, "screenshot: "+masker.MaskString(err.Error()))
		}
	}
	refused := pageRefusal(ctx, &operator{eng: eng, certificateProxy: eng.Handle().CertProxy != nil}, state.Page, snap.URL)
	result.Refused = sessionRefusal(refused, masker)
	state.Page = sessionPage{URL: snap.URL, Title: result.Title, Refused: refused}
	result.IdleDeadline = s.now().Add(state.idleTimeout())
	if err := use.park(ctx, state, result.IdleDeadline); err != nil {
		return result, err
	}
	result.Warnings = nonNil(warnings)
	return result, nil
}

// CloseRequest closes a session.
type CloseRequest struct {
	ID string
	// Force closes the session even while a command holds it.
	Force bool
	// Keep ends the session instead of removing it: its browser closes and
	// its profile is free, and its history can still be exported for as long
	// as an ended session's.
	Keep bool
}

// Close closes a session's browser and removes the session, or with Keep
// ends it.
func (s *Sessions) Close(ctx context.Context, req CloseRequest) error {
	id := req.ID
	if !sessionIDPattern.MatchString(id) {
		return sessionNotFound(id)
	}
	_, store, err := sessionDirs(ctx)
	if err != nil {
		return err
	}
	lock := store.SessionLock(id)
	if err := lock.TryLock(); err != nil && !req.Force {
		return sessionBusy(id, err)
	}
	record, err := store.Load(id)
	if errors.Is(err, os.ErrNotExist) {
		_ = store.Purge(id)
		return sessionNotFound(id)
	}
	if err != nil {
		return err
	}
	if req.Keep {
		defer func() { _ = lock.Unlock() }()
		if record.State == browserhost.StateEnded {
			return nil
		}
		_, err := browserhost.Retire(ctx, store, record, s.now().Add(browserhost.SessionRetention))
		return err
	}
	if record.State != browserhost.StateEnded {
		if err := browserhost.Release(ctx, store, record); err != nil {
			_ = lock.Unlock()
			return err
		}
	}
	return store.Purge(id)
}

// List returns this host's sessions, closing those nothing will use
// again first.
func (s *Sessions) List(ctx context.Context) ([]SessionSummary, error) {
	browserDir, store, err := sessionDirs(ctx)
	if err != nil {
		return nil, err
	}
	sweepCtx, cancel := context.WithTimeout(ctx, sweepBudget)
	_ = browserhost.SweepAll(sweepCtx, browserDir, s.now(), nil)
	cancel()
	records, err := store.List()
	if err != nil {
		return nil, err
	}
	summaries := make([]SessionSummary, 0, len(records))
	for _, record := range records {
		state, err := decodeSessionState(record.Interactive)
		if err != nil {
			continue
		}
		summary := SessionSummary{
			ID:         record.ID,
			URL:        state.Page.URL,
			Title:      state.Page.Title,
			Profile:    record.Profile,
			Headless:   state.Browser.Headless == nil || *state.Browser.Headless,
			Model:      state.modelLabel(),
			Operations: len(state.Ops),
			CreatedAt:  state.CreatedAt,
			LastUsedAt: state.LastUsedAt,
		}
		switch record.State {
		case browserhost.StateEnded:
			summary.State = SessionEnded
		case browserhost.StateRunning:
			summary.State, summary.CDPURL = SessionBusy, record.CDPURL
		default:
			summary.State, summary.CDPURL = SessionIdle, record.CDPURL
			deadline := record.Deadline
			summary.IdleDeadline = &deadline
		}
		summaries = append(summaries, summary)
	}
	slices.SortFunc(summaries, func(a, b SessionSummary) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return summaries, nil
}

// sessionUse is one command's hold on a session.
type sessionUse struct {
	sessions *Sessions
	store    *browserhost.Store
	lock     dirlock.DirLock
	stop     context.CancelFunc
	record   browserhost.Record
	state    sessionState
	eng      engine
}

// use takes a session for one command: it locks the session and checks
// that its browser still waits for commands.
func (s *Sessions) use(ctx context.Context, id string) (*sessionUse, error) {
	if !sessionIDPattern.MatchString(id) {
		return nil, sessionNotFound(id)
	}
	_, store, err := sessionDirs(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := store.Load(id); errors.Is(err, os.ErrNotExist) {
		return nil, sessionNotFound(id)
	}
	lock := store.SessionLock(id)
	if err := lock.TryLock(); err != nil {
		return nil, sessionBusy(id, err)
	}
	use := &sessionUse{sessions: s, store: store, lock: lock, stop: agentstep.KeepLockAlive(ctx, lock)}
	// The record is read again under the lock, since a sweep or another
	// command may have changed it.
	record, err := store.Load(id)
	if err != nil {
		use.release()
		if errors.Is(err, os.ErrNotExist) {
			return nil, sessionNotFound(id)
		}
		return nil, err
	}
	now := s.now()
	switch record.State {
	case browserhost.StateEnded:
		use.release()
		return nil, sessionEnded(id, "its browser closed")
	case browserhost.StateInteractive:
		if !record.Deadline.IsZero() && now.After(record.Deadline) {
			_, retireErr := browserhost.Retire(ctx, store, record, now.Add(browserhost.SessionRetention))
			use.release()
			return nil, errors.Join(sessionEnded(id, "it was idle past its timeout"), retireErr)
		}
	case browserhost.StateRunning:
		// The lock shows the command that set this state is gone.
	case browserhost.StateDetached:
		use.release()
		return nil, sessionEnded(id, "its record is in a state sessions do not use")
	}
	state, err := decodeSessionState(record.Interactive)
	if err != nil {
		use.release()
		return nil, err
	}
	use.record, use.state = record, state
	return use, nil
}

// attach reattaches to the session's browser for the command.
func (u *sessionUse) attach(ctx context.Context, generate generateFunc) (engine, error) {
	record := u.record
	eng, err := u.sessions.launcher.Reattach(ctx, recordedHandle(record), launchOptions{
		DownloadsDir:   record.DownloadsDir,
		AllowedDomains: u.state.Browser.AllowedDomains,
		Generate:       generate,
	})
	if err != nil {
		if probeErr := browserhost.Probe(ctx, record.CDPURL); errors.Is(probeErr, browserhost.ErrUnreachable) {
			_, retireErr := browserhost.Retire(ctx, u.store, record, u.sessions.now().Add(browserhost.SessionRetention))
			return nil, errors.Join(sessionEnded(record.ID, "its browser is no longer running"), retireErr)
		}
		return nil, fmt.Errorf("reattach the session's browser: %w", err)
	}
	u.eng = eng
	if err := saveSessionRecord(u.store, &u.record, u.state, browserhost.StateRunning, time.Time{}); err != nil {
		return nil, errors.Join(err, eng.Detach(context.WithoutCancel(ctx)))
	}
	return eng, nil
}

// park leaves the browser waiting for the next command until deadline.
func (u *sessionUse) park(ctx context.Context, state sessionState, deadline time.Time) error {
	state.LastUsedAt = u.sessions.now()
	u.state = state
	eng := u.eng
	u.eng = nil
	return u.sessions.park(ctx, eng, u.store, &u.record, state, deadline)
}

func (s *Sessions) park(ctx context.Context, eng engine, store *browserhost.Store, record *browserhost.Record, state sessionState, deadline time.Time) error {
	var errs []error
	if eng != nil {
		errs = append(errs, eng.Detach(context.WithoutCancel(ctx)))
	}
	errs = append(errs, saveSessionRecord(store, record, state, browserhost.StateInteractive, deadline))
	return errors.Join(errs...)
}

func (u *sessionUse) release() {
	if u.eng != nil {
		_ = u.eng.Detach(context.Background())
		u.eng = nil
	}
	u.stop()
	_ = u.lock.Unlock()
}

// saveSessionRecord saves the record in state, holding the session's
// state, until deadline.
func saveSessionRecord(store *browserhost.Store, record *browserhost.Record, state sessionState, recordState browserhost.State, deadline time.Time) error {
	payload, err := state.encode()
	if err != nil {
		return err
	}
	record.Interactive = payload
	record.State = recordState
	record.Deadline = deadline
	if recordState == browserhost.StateRunning {
		record.OwnerPID = os.Getpid()
		record.OwnerStartedAt, _ = procutil.StartTime(record.OwnerPID)
	} else {
		record.OwnerPID, record.OwnerStartedAt = 0, 0
	}
	return store.Save(*record)
}

// view reads the page the browser shows, with its outline when outlineChars
// is positive, masked for output, and the page's address as it is. A page
// that cannot be read is reported as a warning.
func (s *Sessions) view(ctx context.Context, eng engine, masker *masking.Masker, outlineChars int, warnings *[]string) (SessionPageView, string) {
	if outlineChars == 0 {
		pageURL, err := eng.CurrentURL(ctx)
		if err != nil {
			*warnings = append(*warnings, "read the page: "+masker.MaskString(err.Error()))
		}
		return SessionPageView{URL: masker.MaskString(pageURL)}, pageURL
	}
	snap, err := eng.Snapshot(ctx)
	if err != nil {
		*warnings = append(*warnings, "read the page: "+masker.MaskString(err.Error()))
		pageURL, _ := eng.CurrentURL(ctx)
		return SessionPageView{URL: masker.MaskString(pageURL)}, pageURL
	}
	text, truncated, _ := renderOutline(snap, outlineOptions{MaxChars: outlineChars})
	return SessionPageView{
		URL:              masker.MaskString(snap.URL),
		Title:            masker.MaskString(snap.Title),
		Outline:          masker.MaskString(text),
		OutlineTruncated: truncated,
	}, snap.URL
}

// pageRefusal returns the refusal the page at pageURL shows after a
// command, with the bot check an earlier command saw on that page: a
// browser reattached since cannot tell it, as when a person reloaded the
// page between commands.
func pageRefusal(ctx context.Context, op *operator, before sessionPage, pageURL string) *refusal {
	refused, ok := op.pageRefusal(ctx)
	if !ok {
		return nil
	}
	if seen := before.Refused; refused.BotCheck == "" && seen != nil && before.URL == pageURL && seen.Host == refused.Host && seen.Status == refused.Status {
		refused.BotCheck = seen.BotCheck
	}
	return &refused
}

// sessionRefusal reports refused in a command's result.
func sessionRefusal(refused *refusal, masker *masking.Masker) *SessionRefusal {
	if refused == nil {
		return nil
	}
	return &SessionRefusal{
		Host: masker.MaskString(refused.Host), Status: refused.Status, BotCheck: refused.BotCheck,
		Message: masker.MaskString(refused.String()),
	}
}

// closeSession closes a session whose opening failed.
func closeSession(ctx context.Context, eng engine, store *browserhost.Store, record browserhost.Record) error {
	closeErr := eng.Close(context.WithoutCancel(ctx))
	record.CDPURL = ""
	return errors.Join(closeErr, browserhost.Release(ctx, store, record), store.Purge(record.ID))
}

// reserveSession picks an unused session ID and creates its work directory.
func reserveSession(store *browserhost.Store) (string, string, error) {
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	for range 10 {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return "", "", err
		}
		id := strings.ToLower(encoding.EncodeToString(raw))[:sessionIDLength]
		dir := store.WorkDir(id)
		if err := os.MkdirAll(filepath.Dir(dir), sessionDirMode); err != nil {
			return "", "", err
		}
		if err := os.Mkdir(dir, sessionDirMode); err == nil {
			return id, dir, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
	}
	return "", "", errors.New("could not pick an unused session ID")
}

// sessionActions reports the actions an act performed, masking the secret
// values their arguments hold.
func sessionActions(actions []recordedAction, masker *masking.Masker) []SessionAction {
	out := make([]SessionAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, SessionAction{
			Selector:    action.Selector,
			Description: action.Description,
			Method:      action.Method,
			Arguments:   maskAll(masker, slices.Clone(action.Arguments)),
		})
	}
	return out
}

func sessionProfileError(err error) error {
	if held, ok := errors.AsType[*profileHeldError](err); ok {
		return &SessionError{Code: CodeProfileInUse, Message: held.Error()}
	}
	return err
}

func sessionNotFound(id string) error {
	return &SessionError{Code: CodeSessionNotFound, Message: fmt.Sprintf("no browser session %q on this host; list them with \"dagu browser session list\"", id)}
}

func sessionBusy(id string, cause error) error {
	if errors.Is(cause, dirlock.ErrLockConflict) {
		return &SessionError{Code: CodeSessionBusy, Message: fmt.Sprintf("browser session %s is running another command; wait for it to finish", id)}
	}
	return fmt.Errorf("lock browser session %s: %w", id, cause)
}

func sessionEnded(id, why string) error {
	return &SessionError{Code: CodeSessionEnded, Message: fmt.Sprintf("browser session %s has ended: %s; its history can still be exported", id, why)}
}

func maskOutputs(masker *masking.Masker, outputs map[string]any) map[string]any {
	if len(outputs) == 0 {
		return outputs
	}
	data, err := json.Marshal(outputs)
	if err != nil {
		return outputs
	}
	var masked map[string]any
	if err := json.Unmarshal([]byte(masker.MaskString(string(data))), &masked); err != nil {
		return outputs
	}
	return masked
}

func maskAll(masker *masking.Masker, texts []string) []string {
	for i, text := range texts {
		texts[i] = masker.MaskString(text)
	}
	return texts
}

// nonNil returns an empty slice for nil, so a list is never null in JSON.
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
