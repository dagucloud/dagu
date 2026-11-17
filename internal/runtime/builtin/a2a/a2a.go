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
	redact := newRedactor(b.Headers, b.AgentURL)
	out, cerr := c.call(ctx, redact)
	if cerr == nil {
		body, err := redact.marshal(out)
		if err != nil {
			return fmt.Errorf("a2a.%s: encoding output: %w", c.cfg.op, err)
		}
		if limit := outputLimit(ctx); c.capture && int64(len(body)) > limit {
			cerr = &callError{kind: kindOutputLimit, message: fmt.Sprintf(
				"result is %d bytes, over the %d byte limit for captured step output; raise max_output_size, drop output:, or send history_length to shorten the task history", len(body), limit)}
			if id, ok := out["sent_message_id"].(string); ok {
				// The agent already accepted the message; a retry would submit it again.
				cerr.messageID = id
				cerr.message += ". The message was accepted"
				if tid, _ := out["task_id"].(string); tid != "" {
					cerr.message += fmt.Sprintf(" (task_id %s); read the task with a2a.get_task", tid)
				} else {
					cerr.message += " and answered with a direct Message, which a2a.get_task cannot recover"
				}
				cerr.message += ". a2a.send never retries; retrying can duplicate remote work"
			}
		} else {
			return wrapWrite(writeAll(c.stdout, body), c.cfg.op)
		}
	}
	cerr.action = "a2a." + c.cfg.op
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

func wrapWrite(err error, op string) error {
	if err != nil {
		return fmt.Errorf("a2a.%s: writing output: %w", op, err)
	}
	return nil
}

func outputLimit(ctx context.Context) int64 {
	limit := int64(ir.DefaultMaxOutputSize)
	if rc, ok := runtime.LookupDAGContext(ctx); ok && rc.DAG != nil && rc.DAG.MaxOutputSize > 0 {
		limit = int64(rc.DAG.MaxOutputSize)
	}
	return limit
}

func (c *caller) call(ctx context.Context, redact redactor) (map[string]any, *callError) {
	cardObs := &observer{}
	sess, err := connect(ctx, c.base, c.cfg.base(), cardObs)
	if err != nil {
		cerr := classify(ctx, err, cardObs, redact, false)
		if cerr.kind == kindProtocol {
			cerr.kind = kindDiscovery
		}
		return nil, cerr
	}
	if c.cfg.op == opGetTask {
		out, err := c.getTask(ctx, sess)
		return out, classify(ctx, err, sess.obs, redact, false)
	}
	return c.send(ctx, sess, redact)
}

func (c *caller) send(ctx context.Context, sess *session, redact redactor) (map[string]any, *callError) {
	cfg := c.cfg.send
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(cfg.Message))
	msg.ContextID = cfg.ContextID
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
		return nil, &callError{kind: kindProtocol, message: redact.apply(err.Error()), messageID: msg.ID}
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
		return cerr
	}
	status, netErr := obs.snapshot()
	msg := redact.apply(err.Error())
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

func init() {
	executor.RegisterExecutor(executorType, newCaller, validateStep, registry.ExecutorCapabilities{Command: true})
}
