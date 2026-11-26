// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// unknownOutcome documents what a failed a2a.send means.
const unknownOutcome = " Outcome unknown: the agent may have accepted the message and started a task. " +
	"a2a.send never retries; retrying can duplicate remote work"

// defaultDeadline bounds discovery and the call together when the step sets
// no timeout_sec.
const defaultDeadline = 60 * time.Second

// outputLimitHint tells how to fit a result into captured step output.
const outputLimitHint = "raise max_output_size, drop output:, or send history_length to shorten the task history"

var _ executor.Executor = (*caller)(nil)

type caller struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
	cfg    config
	// capture is true when the step stores its stdout in a variable.
	capture bool
	base    http.RoundTripper
	// deadline bounds the call when the step has no timeout of its own.
	deadline time.Duration
	cancel   context.CancelFunc
	killed   bool
}

func newCaller(_ context.Context, step ir.Step) (executor.Executor, error) {
	cfg, err := decodeConfig(step, false)
	if err != nil {
		return nil, err
	}
	return &caller{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		cfg:     cfg,
		capture: step.Output != "",
		base:    newBaseTransport(),

		deadline: defaultDeadline,
	}, nil
}

// newBaseTransport clones the default transport, or builds a fresh one when
// the default has been replaced by another RoundTripper type.
func newBaseTransport() http.RoundTripper {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{Proxy: http.ProxyFromEnvironment}
}

func (c *caller) SetStdout(out io.Writer) { c.stdout = out }
func (c *caller) SetStderr(out io.Writer) { c.stderr = out }

func (c *caller) Kill(_ os.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killed = true
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

// Run performs one discovery and one JSON-RPC call. It never retries; retry
// policy belongs to the step.
func (c *caller) Run(ctx context.Context) error {
	ctx, cancel := withDefaultDeadline(ctx, c.deadline)
	defer cancel()
	defer func() {
		if idle, ok := c.base.(interface{ CloseIdleConnections() }); ok {
			idle.CloseIdleConnections()
		}
	}()
	c.mu.Lock()
	c.cancel = cancel
	killed := c.killed
	c.mu.Unlock()
	if killed {
		cancel()
	}

	b := c.cfg.base()
	redact := newRedactor(b.Headers)
	out, cerr := c.call(ctx, redact)
	if cerr == nil {
		body, err := redact.marshal(out)
		if err != nil {
			return fmt.Errorf("a2a.%s: encoding output: %w", c.cfg.op, err)
		}
		if limit := outputLimit(ctx); c.capture && int64(len(body)) > limit {
			cerr = &callError{kind: kindOutputLimit, message: fmt.Sprintf(
				"result is %d bytes, over the %d byte limit for captured step output; %s", len(body), limit, outputLimitHint)}
			if id, note := acceptedNote(out); id != "" {
				cerr.messageID = id
				cerr.message += note
			}
		} else if err := writeAll(c.stdout, body); err != nil {
			_, note := acceptedNote(out)
			return fmt.Errorf("a2a.%s: writing output: %s%s", c.cfg.op, redact.apply(err.Error()), redact.apply(note))
		} else {
			return nil
		}
	}
	cerr.action = "a2a." + c.cfg.op
	// Every message may hold text from the agent or its card, so all of them are masked here.
	cerr.message = redact.apply(cerr.message)
	body, err := redact.marshal(errorOutput{Error: errorBody{Kind: cerr.kind, Message: cerr.message, MessageID: cerr.messageID}})
	if err == nil {
		_ = writeAll(c.stdout, body)
	}
	return cerr
}

// withDefaultDeadline bounds ctx by d unless it already has a deadline, such
// as the step's timeout_sec.
func withDefaultDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

// acceptedNote returns the message ID and a recovery note for a result of
// a2a.send, whose message the agent has already accepted; a retry would submit
// it again. It is empty for other results.
func acceptedNote(out map[string]any) (string, string) {
	id, ok := out["sent_message_id"].(string)
	if !ok {
		return "", ""
	}
	note := fmt.Sprintf(". The message was accepted (message_id %s)", id)
	if tid, _ := out["task_id"].(string); tid != "" {
		note += fmt.Sprintf(" and started task_id %s; read the task with a2a.get_task", tid)
	} else {
		note += " and answered with a direct Message, which a2a.get_task cannot recover"
	}
	return id, note + ". a2a.send never retries; retrying can duplicate remote work"
}

func outputLimit(ctx context.Context) int64 {
	limit := int64(ir.DefaultMaxOutputSize)
	if rc, ok := runtime.LookupDAGContext(ctx); ok && rc.DAG != nil && rc.DAG.MaxOutputSize > 0 {
		limit = int64(rc.DAG.MaxOutputSize)
	}
	return limit
}

// responseLimit caps the JSON-RPC response body. A captured a2a.get_task
// result over max_output_size fails anyway, so its body is refused at twice
// that size, leaving room for whitespace and fields the output drops.
// a2a.send keeps the ceiling so an oversized result still names its task.
func (c *caller) responseLimit(ctx context.Context) int64 {
	if !c.capture || c.cfg.op != opGetTask {
		return maxResponseBytes
	}
	return min(2*outputLimit(ctx), maxResponseBytes)
}

func (c *caller) call(ctx context.Context, redact redactor) (map[string]any, *callError) {
	cardObs := &observer{}
	limit := c.responseLimit(ctx)
	sess, err := connect(ctx, c.base, c.cfg.base(), cardObs, limit)
	if err != nil {
		cerr := classify(ctx, err, cardObs, redact, false)
		var refused *statusError
		if cerr.kind == kindProtocol && !errors.As(err, &refused) {
			cerr.kind = kindDiscovery
		}
		return nil, cerr
	}
	if c.cfg.op == opGetTask {
		out, err := c.getTask(ctx, sess)
		if _, netErr := sess.obs.snapshot(); err != nil && c.capture && errors.Is(netErr, errBodyTooLarge) {
			return nil, &callError{kind: kindOutputLimit, message: fmt.Sprintf(
				"response is over %d bytes, too large for the %d byte limit for captured step output; %s", limit, outputLimit(ctx), outputLimitHint)}
		}
		return out, classify(ctx, err, sess.obs, redact, false)
	}
	return c.send(ctx, sess, redact)
}

func (c *caller) send(ctx context.Context, sess *session, redact redactor) (map[string]any, *callError) {
	cfg := c.cfg.send
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(cfg.Message))
	msg.ContextID = cfg.ContextID
	msg.ID = messageID(ctx, cfg)
	immediately := cfg.ReturnImmediately == nil || *cfg.ReturnImmediately
	res, err := sess.transport.SendMessage(ctx, sess.params(), &a2a.SendMessageRequest{
		Tenant:  sess.route.tenant,
		Config:  &a2a.SendMessageConfig{ReturnImmediately: immediately},
		Message: msg,
	})
	if err != nil {
		cerr := classify(ctx, err, sess.obs, redact, true)
		cerr.messageID = msg.ID
		return nil, cerr
	}
	var out map[string]any
	switch r := res.(type) {
	case *a2a.Task:
		var tree any
		if tree, err = resultTree(sess.obs, "task", r); err == nil {
			out, err = taskOutput(sess.route, r, tree)
		}
	case *a2a.Message:
		var tree any
		if tree, err = resultTree(sess.obs, "message", r); err == nil {
			out, err = messageOutput(sess.route, r, tree)
		}
	default:
		err = fmt.Errorf("unexpected result %T", res)
	}
	if err != nil {
		return nil, &callError{kind: kindProtocol, message: redact.apply(err.Error()) + "." + unknownOutcome, messageID: msg.ID}
	}
	out["sent_message_id"] = msg.ID
	return out, nil
}

func (c *caller) getTask(ctx context.Context, sess *session) (map[string]any, error) {
	cfg := c.cfg.get
	task, err := sess.transport.GetTask(ctx, sess.params(), &a2a.GetTaskRequest{
		Tenant:        sess.route.tenant,
		ID:            a2a.TaskID(cfg.TaskID),
		HistoryLength: cfg.HistoryLength,
	})
	if err != nil {
		return nil, err
	}
	tree, err := resultTree(sess.obs, "task", task)
	if err != nil {
		return nil, err
	}
	return taskOutput(sess.route, task, tree)
}

// classify maps a failure to a stable kind. Failures of a2a.send that leave
// open whether the agent received the message say so.
func classify(ctx context.Context, err error, obs *observer, redact redactor, send bool) *callError {
	if err == nil {
		return nil
	}
	var cerr *callError
	if errors.As(err, &cerr) {
		cerr.message = clip(redact.apply(cerr.message))
		return cerr
	}
	status, netErr := obs.snapshot()
	msg := clip(redact.apply(err.Error()))
	var statusErr *statusError
	var remote *a2a.Error
	var notOK *agentcard.ErrStatusNotOK
	ambiguous := true
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		cerr = &callError{kind: kindTimeout, message: "call timed out"}
	case errors.Is(ctx.Err(), context.Canceled):
		cerr = &callError{kind: kindCanceled, message: "call canceled"}
	case errors.As(err, &statusErr) && statusErr.code >= 300 && statusErr.code < 400:
		ambiguous = false
		cerr = &callError{kind: kindProtocol, message: "agent responded with HTTP " + strconv.Itoa(statusErr.code) + "; redirects are not followed, configure the final URL"}
	case errors.As(err, &statusErr):
		ambiguous = false
		cerr = &callError{kind: kindAuth, message: "agent rejected credentials (HTTP " + strconv.Itoa(statusErr.code) + ")"}
	case errors.Is(err, a2a.ErrUnauthenticated), errors.Is(err, a2a.ErrUnauthorized):
		ambiguous = false
		cerr = &callError{kind: kindAuth, message: "agent rejected credentials: " + msg}
	case errors.Is(err, a2a.ErrTaskNotFound):
		ambiguous = false
		cerr = &callError{kind: kindNotFound, message: "task not found on the agent (wrong task_id, agent, or tenant?): " + msg}
	case errors.As(err, &remote):
		ambiguous = false
		cerr = &callError{kind: kindRemote, message: msg}
	case errors.As(err, &notOK):
		ambiguous = false
		cerr = &callError{kind: kindDiscovery, message: "agent card request failed with HTTP " + strconv.Itoa(notOK.StatusCode) + "; check agent_url"}
	case netErr != nil:
		cerr = &callError{kind: kindTransport, message: "connection failed or dropped: " + msg}
	case status >= 400:
		ambiguous = status >= 500
		cerr = &callError{kind: kindTransport, message: "agent responded with HTTP " + strconv.Itoa(status) + " " + http.StatusText(status)}
	default:
		cerr = &callError{kind: kindProtocol, message: "malformed response: " + msg}
	}
	if send && ambiguous {
		cerr.message += "." + unknownOutcome
	}
	return cerr
}

// maxDetailBytes bounds the agent text an error carries. The SDK can copy a
// whole response into its error, and the error is stored in the run status.
const maxDetailBytes = 1 << 10

// clip cuts s to maxDetailBytes on a rune boundary. Secrets are masked
// before the cut, so the cut never leaves part of one visible.
func clip(s string) string {
	if len(s) <= maxDetailBytes {
		return s
	}
	cut := maxDetailBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "... (truncated)"
}

func init() {
	executor.RegisterExecutor(executorType, newCaller, validateStep, registry.ExecutorCapabilities{Command: true})
}
