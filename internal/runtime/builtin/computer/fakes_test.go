// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/require"
)

// fakeBackend is a desktop that records input and shows a screen. Each
// left click moves on to the next of afterClicks, if any remain.
type fakeBackend struct {
	mu          sync.Mutex
	screen      *image.RGBA
	afterClicks []*image.RGBA
	events      []string
	// typeErr fails typing when set.
	typeErr error
	// lastEvent and personInputAt are when the desktop last received input
	// from the step and from a person.
	lastEvent     time.Time
	personInputAt time.Time
	// personChecks is how many more checks find a person using the desktop
	// at that moment.
	personChecks int
}

func newFakeBackend(width, height int) *fakeBackend {
	return &fakeBackend{screen: pattern(width, height, 0)}
}

// pattern draws a screen whose brightness ramps across; shift moves the
// ramp, which changes the screen's fingerprint.
func pattern(width, height, shift int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			v := uint8((x + shift) * 255 / (width + shift))
			if shift != 0 && x < width/2 {
				v = 255 - v
			}
			img.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

func (b *fakeBackend) show(screen *image.RGBA) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.screen = screen
}

func (b *fakeBackend) record(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	b.lastEvent = time.Now()
}

// personUses records input from a person now. The time is a moment ahead,
// so it follows every earlier reading of a clock as coarse as Windows'.
func (b *fakeBackend) personUses() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.personInputAt = time.Now().Add(time.Millisecond)
}

// personKeepsUsing makes the next checks find a person using the desktop,
// however long the step takes to make them.
func (b *fakeBackend) personKeepsUsing(checks int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.personChecks = checks
}

func (b *fakeBackend) LastInput() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.personChecks > 0 {
		b.personChecks--
		b.personInputAt = time.Now()
	}
	if b.personInputAt.After(b.lastEvent) {
		return b.personInputAt
	}
	return b.lastEvent
}

func (b *fakeBackend) inputs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.events...)
}

func (b *fakeBackend) Capture() (*image.RGBA, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.screen, nil
}

func (b *fakeBackend) MoveTo(x, y int) error {
	b.record(fmt.Sprintf("move %d,%d", x, y))
	return nil
}

func (b *fakeBackend) Position() (int, int, error) { return 0, 0, nil }

func (b *fakeBackend) Button(button desktop.Button, down bool, clicks int) error {
	if !down {
		return nil
	}
	b.record(fmt.Sprintf("%s down #%d", button, clicks))
	b.mu.Lock()
	defer b.mu.Unlock()
	if button == desktop.ButtonLeft && len(b.afterClicks) > 0 {
		b.screen, b.afterClicks = b.afterClicks[0], b.afterClicks[1:]
	}
	return nil
}

// script shows screen now and next after each later left click.
func (b *fakeBackend) script(screen *image.RGBA, next ...*image.RGBA) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.screen, b.afterClicks, b.events = screen, next, nil
}

// stripes draws n vertical black and white stripes, whose fingerprints
// differ clearly for different n.
func stripes(width, height, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			v := uint8(0)
			if (x*n/width)%2 == 1 {
				v = 255
			}
			img.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

func (b *fakeBackend) Wheel(dx, dy int) error {
	b.record(fmt.Sprintf("wheel %d,%d", dx, dy))
	return nil
}

func (b *fakeBackend) Key(key desktop.Key, down bool) error {
	if down {
		b.record("key " + string(key))
	}
	return nil
}

func (b *fakeBackend) Type(text string) error {
	b.mu.Lock()
	err := b.typeErr
	b.mu.Unlock()
	if err != nil {
		return err
	}
	b.record("type " + text)
	return nil
}

func (b *fakeBackend) Close() error { return nil }

// scriptedSession answers each observation with the next scripted turn and
// records the observations.
type scriptedSession struct {
	limit        computeruse.ImageLimit
	turns        []*computeruse.Turn
	err          error
	observations []computeruse.Observation
	// onNext runs at the start of every request.
	onNext func()
}

func (s *scriptedSession) ImageLimit() computeruse.ImageLimit { return s.limit }

func (s *scriptedSession) Next(_ context.Context, obs computeruse.Observation) (*computeruse.Turn, error) {
	if s.onNext != nil {
		s.onNext()
	}
	s.observations = append(s.observations, obs)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.turns) == 0 {
		return nil, errors.New("no scripted turn left")
	}
	turn := s.turns[0]
	s.turns = s.turns[1:]
	return turn, nil
}

func done(summary string) *computeruse.Turn {
	return &computeruse.Turn{Done: &computeruse.Done{Success: true, Summary: summary}, Usage: llmpkg.Usage{PromptTokens: 10, CompletionTokens: 2}}
}

func actions(list ...computeruse.Action) *computeruse.Turn {
	return &computeruse.Turn{Actions: list, Usage: llmpkg.Usage{PromptTokens: 10, CompletionTokens: 2}}
}

func clickAt(x, y int) computeruse.Action {
	return computeruse.Action{CallID: "c", Kind: computeruse.KindClick, Point: &computeruse.Point{X: x, Y: y}}
}

// visionModel answers extract and judge requests with a respond tool call.
// Requests are told apart by the schema: judge requests ask for answer.
type visionModel struct {
	mu       sync.Mutex
	extract  string
	truths   map[string]bool
	requests []*llmpkg.ChatRequest
}

func (m *visionModel) Chat(_ context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	arguments := m.extract
	properties, _ := req.Tools[0].Function.Parameters["properties"].(map[string]any)
	if _, judge := properties["answer"]; judge {
		holds := false
		for statement, truth := range m.truths {
			if bytes.Contains([]byte(req.Messages[1].Content), []byte(statement)) {
				holds = truth
			}
		}
		arguments = fmt.Sprintf(`{"answer":%t,"reason":"checked"}`, holds)
	}
	return &llmpkg.ChatResponse{
		ToolCalls: []llmpkg.ToolCall{{ID: "r", Type: "function", Function: llmpkg.ToolCallFunction{Name: agentstep.RespondToolName, Arguments: arguments}}},
		Usage:     llmpkg.Usage{PromptTokens: 5, CompletionTokens: 1},
	}, nil
}

func (m *visionModel) ChatStream(context.Context, *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
	return nil, errors.New("not used")
}

func (m *visionModel) Name() string { return "vision" }

// testRun is one DAG run whose computer step executions share a data
// directory and a desktop.
type testRun struct {
	t         *testing.T
	dataDir   string
	artifacts string
	workDir   string
	// desktopLock is the desktop lock the run's steps take; runs that share
	// it share a desktop.
	desktopLock string
	backend     *fakeBackend
	vision      *visionModel
	secrets     map[string]string
	llm         *ir.LLMConfig
	// sessions supplies the session for each act, in order.
	sessions []*scriptedSession
	// sessionErrors fails session creation for the named models.
	sessionErrors map[string]error
	launches      [][]string
}

func newTestRun(t *testing.T) *testRun {
	t.Helper()
	return &testRun{
		t:           t,
		dataDir:     t.TempDir(),
		artifacts:   t.TempDir(),
		workDir:     t.TempDir(),
		desktopLock: filepath.Join(t.TempDir(), desktopLockName),
		backend:     newFakeBackend(400, 200),
		vision:      &visionModel{},
		llm:         &ir.LLMConfig{Provider: "openai", Model: "test-model"},
	}
}

// stepExecution is one execution of the computer step.
type stepExecution struct {
	exec   *computerExecutor
	stdout bytes.Buffer
	stderr bytes.Buffer
	err    error
}

func (r *testRun) execute(withJSON string, session *ir.AgentSession) *stepExecution {
	r.t.Helper()
	var with map[string]any
	require.NoError(r.t, json.Unmarshal([]byte(withJSON), &with))
	step := ir.Step{
		ID:             "post",
		Name:           "post",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with},
		LLM:            r.llm,
	}
	created, err := newExecutor(r.t.Context(), step)
	require.NoError(r.t, err)
	execution := &stepExecution{exec: created.(*computerExecutor)}
	execution.exec.openDesktop = func() (*desktop.Driver, error) { return desktop.New(r.backend), nil }
	execution.exec.launch = func(dir, command string, args []string) error {
		r.launches = append(r.launches, append([]string{dir, command}, args...))
		return nil
	}
	execution.exec.newProvider = func(context.Context, *ir.LLMConfig) (llmpkg.Provider, error) {
		return r.vision, nil
	}
	execution.exec.newSession = func(_ llmpkg.ProviderType, _ llmpkg.Provider, _ computeruse.Mode, opts computeruse.Options) (computeruse.Session, error) {
		if err := r.sessionErrors[opts.Model]; err != nil {
			return nil, err
		}
		if len(r.sessions) == 0 {
			return nil, errors.New("no scripted session left")
		}
		session := r.sessions[0]
		r.sessions = r.sessions[1:]
		return session, nil
	}
	execution.exec.settle = settleTiming{timeout: time.Millisecond}
	execution.exec.desktopLock = r.desktopLock
	execution.exec.idlePoll = time.Millisecond
	execution.exec.SetStdout(&execution.stdout)
	execution.exec.SetStderr(&execution.stderr)
	execution.exec.SetAgentSession(session)
	execution.err = execution.exec.Run(r.context())
	return execution
}

func (r *testRun) context() context.Context {
	scope := value.NewEnvScope(nil, false).WithEntry(runenv.EnvKeyDAGRunArtifactsDir, r.artifacts, value.EnvSourceDAGEnv)
	for name, secret := range r.secrets {
		scope = scope.WithEntry(name, secret, value.EnvSourceSecret)
	}
	ctx := cmnconfig.WithConfig(r.t.Context(), &cmnconfig.Config{Paths: cmnconfig.PathsConfig{DataDir: r.dataDir}})
	return runtime.WithEnv(ctx, runtime.Env{
		Context: runtime.Context{
			DAG:      &ir.DAG{Name: "invoices"},
			DAGRunID: "run-1",
			WorkerID: "worker-a",
		},
		Scope:      scope,
		WorkingDir: r.workDir,
	})
}

// operationEvents lists the step's operation events, in order.
func operationEvents(session *ir.AgentSession) []ir.AgentSessionEvent {
	var events []ir.AgentSessionEvent
	for _, event := range session.Events {
		if event.Type == agentstep.EventOperation {
			events = append(events, event)
		}
	}
	return events
}

func eventNames(session *ir.AgentSession) []string {
	names := make([]string, 0, len(session.Events))
	for _, event := range session.Events {
		if event.Type == agentstep.EventOperation {
			names = append(names, event.Name+":"+event.Status)
		}
	}
	return names
}

// waitReasons lists the reasons of the step's waiting events, in order.
func waitReasons(session *ir.AgentSession) []string {
	var reasons []string
	for _, event := range session.Events {
		if event.Type == agentstep.EventLifecycle && event.Status == agentstep.StatusWaiting {
			reasons = append(reasons, event.Name)
		}
	}
	return reasons
}

func lifecycleMessages(session *ir.AgentSession) []string {
	var messages []string
	for _, event := range session.Events {
		if event.Type == agentstep.EventLifecycle {
			messages = append(messages, event.Content)
		}
	}
	return messages
}

// blockRead makes the only file in dir unreadable until the returned
// function restores it. A directory stands in for the file, which no
// process can read as a file whatever its privileges.
func blockRead(t *testing.T, dir string) (restore func()) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	path := filepath.Join(dir, entries[0].Name())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700))
	return func() {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.WriteFile(path, data, 0o600))
	}
}
