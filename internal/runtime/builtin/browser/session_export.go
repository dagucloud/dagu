// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/goccy/go-yaml"
)

// ExportRequest turns a session's history into a browser step.
type ExportRequest struct {
	ID string
	// DAG and Step name the step that will run the exported operations; its
	// replay cache receives the acts' recordings.
	DAG  string
	Step string
	// Skip lists the history indices of operations to leave out.
	Skip []int
	// DryRun builds the step without writing recordings.
	DryRun bool
}

// ExportResult is a browser step built from a session.
type ExportResult struct {
	ID  string `json:"id"`
	DAG string `json:"dag"`
	// Step is the step to put in the DAG's steps.
	Step ExportedStep `json:"step"`
	// CacheFile is where the recordings were written; empty on a dry run.
	CacheFile string `json:"cache_file,omitempty"`
	// Recordings counts the acts and uploads a first run replays.
	Recordings int `json:"recordings"`
	// CommitPoints counts the writes of the step's acts and uploads kept as
	// commit points to approve.
	CommitPoints int `json:"commit_points"`
	// Ops maps each operation of the step to the session's history.
	Ops []ExportedOperation `json:"ops"`
	// Variables says how to give the step each variable's value.
	Variables []ExportedVariable `json:"variables"`
	// LLM is the model the session used, for the DAG's llm block.
	LLM      *ir.LLMConfig `json:"llm,omitempty"`
	Warnings []string      `json:"warnings"`
}

// ExportedStep is a browser.run step, in the order a person writes one.
type ExportedStep struct {
	ID     string       `json:"id"`
	Action string       `json:"action"`
	With   ExportedWith `json:"with"`
}

// ExportedWith is the with block of an exported step.
type ExportedWith struct {
	URL       string            `json:"url,omitempty"`
	Browser   *exportedBrowser  `json:"browser,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
	Do        []json.RawMessage `json:"do"`
}

type exportedBrowser struct {
	Executable     string    `json:"executable,omitempty"`
	Viewport       *viewport `json:"viewport,omitempty"`
	Proxy          string    `json:"proxy,omitempty"`
	AllowedDomains []string  `json:"allowed_domains,omitempty"`
	Profile        string    `json:"profile,omitempty"`
}

// ExportedOperation is one operation of an exported step.
type ExportedOperation struct {
	// SessionIndex is the operation's place in the session's history, and
	// Index its place in the step's with.do.
	SessionIndex int    `json:"session_index"`
	Index        int    `json:"index"`
	Kind         string `json:"kind"`
	// Recorded reports that the step's first run replays the act or upload.
	Recorded bool `json:"recorded"`
}

// ExportedVariable is a variable the exported step uses.
type ExportedVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Note  string `json:"note"`
}

// YAML returns the step as an item of a DAG's steps list.
func (s ExportedStep) YAML() ([]byte, error) {
	data, err := json.Marshal([]ExportedStep{s})
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(data)
}

// Export builds a browser step from the operations a session ran: those
// that succeeded, and those skipped because their when did not hold, in
// the order they ran. Unless DryRun is set, it writes the acts' recordings
// to the replay cache of the named DAG and step, so the step's first run
// replays them when it meets the pages the session met. A session whose
// browser closed can still be exported.
func (s *Sessions) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
	if !sessionIDPattern.MatchString(req.ID) {
		return ExportResult{}, sessionNotFound(req.ID)
	}
	if req.DAG == "" {
		return ExportResult{}, &SessionError{Code: CodeInvalidInput, Message: "name the DAG the step goes in"}
	}
	if err := ir.ValidateDAGName(req.DAG); err != nil {
		return ExportResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("DAG name %q: %v", req.DAG, err)}
	}
	if err := spec.ValidateStepID(req.Step); err != nil {
		return ExportResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("step %q: %v", req.Step, err)}
	}
	browserDir, store, err := sessionDirs(ctx)
	if err != nil {
		return ExportResult{}, err
	}
	if _, err := store.Load(req.ID); errors.Is(err, os.ErrNotExist) {
		return ExportResult{}, sessionNotFound(req.ID)
	}
	lock := store.SessionLock(req.ID)
	if err := lock.TryLock(); err != nil {
		return ExportResult{}, sessionBusy(req.ID, err)
	}
	defer func() { _ = lock.Unlock() }()
	record, err := store.Load(req.ID)
	if err != nil {
		return ExportResult{}, err
	}
	state, err := decodeSessionState(record.Interactive)
	if err != nil {
		return ExportResult{}, err
	}
	for _, index := range req.Skip {
		if index < 0 || index >= len(state.Ops) {
			return ExportResult{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("the session has no operation %d to skip", index)}
		}
	}

	exported, err := exportStep(state, req)
	if err != nil {
		return ExportResult{}, err
	}
	if err := exported.check(); err != nil {
		return ExportResult{}, err
	}
	result := ExportResult{
		ID:        req.ID,
		DAG:       req.DAG,
		Step:      exported.step,
		Ops:       exported.ops,
		Variables: exported.variables,
		LLM:       state.LLM,
		Warnings:  exported.warnings,
	}
	result.Recordings = len(exported.recordings)
	result.CommitPoints = len(exported.commits)
	if !req.DryRun {
		if len(exported.commits) > 0 {
			if err := writegate.NewCommitStore(browserDir).Note(ctx, req.DAG, req.Step, exported.commits...); err != nil {
				return ExportResult{}, fmt.Errorf("keep the commit points: %w", err)
			}
		}
		cache := openReplayCache(browserDir, req.DAG, req.Step)
		for _, key := range slices.Sorted(maps.Keys(exported.recordings)) {
			cache.Stage(key, exported.recordings[key])
		}
		if err := cache.Commit(ctx); err != nil {
			return ExportResult{}, fmt.Errorf("write the recordings: %w", err)
		}
		result.CacheFile = replaycache.New(browserDir).Path(req.DAG, req.Step)
	}
	result.Variables = nonNil(result.Variables)
	result.Warnings = nonNil(result.Warnings)
	return result, nil
}

// exportedSession is a step built from a session, before it is written.
type exportedSession struct {
	step       ExportedStep
	ops        []ExportedOperation
	variables  []ExportedVariable
	recordings map[string][]recordedAction
	// commits are the writes the step's operations sent or tried to send.
	commits  []writegate.CommitPoint
	warnings []string
	// sessionIndex maps each operation of the step to the history.
	sessionIndex []int
}

func exportStep(state sessionState, req ExportRequest) (*exportedSession, error) {
	exported := &exportedSession{
		step:       ExportedStep{ID: req.Step, Action: "browser.run", With: ExportedWith{URL: state.StartURL}},
		recordings: map[string][]recordedAction{},
	}
	if browser := exportBrowser(state.Browser); browser != nil {
		exported.step.With.Browser = browser
	}
	used := map[string]bool{}
	// previous is the page the last exported operation left the browser on.
	previous := state.StartURL
	for sessionIndex, entry := range state.Ops {
		var op operation
		if err := json.Unmarshal(entry.Op, &op); err != nil {
			return nil, fmt.Errorf("read operation %d: %w", sessionIndex, err)
		}
		if !exportable(entry, op) || slices.Contains(req.Skip, sessionIndex) {
			continue
		}
		index := len(exported.step.With.Do)
		exported.step.With.Do = append(exported.step.With.Do, entry.Op)
		exported.sessionIndex = append(exported.sessionIndex, sessionIndex)
		item := ExportedOperation{SessionIndex: sessionIndex, Index: index, Kind: entry.Kind}
		// key is where the step's replay cache keeps what the operation did,
		// and what names it in a warning.
		key, what := "", ""
		if op.Act != nil {
			for _, name := range entry.Variables {
				used[name] = true
			}
			if strings.Contains(op.Act.Instruction, "$") {
				exported.warnf("operation %d's instruction holds $, which a step may fill in from its parameters before acting, so the first run may ask the model for it", sessionIndex)
			}
			key, what = replayKey(index, op.Act.Instruction, entry.URLBefore), fmt.Sprintf("act %q", op.Act.Instruction)
		}
		if op.Upload != nil {
			exported.warnf("operation %d uploads %s, the files the session was given; give the step the files each run uploads, such as an earlier step's output",
				sessionIndex, strings.Join(op.Upload.Files, ", "))
			if op.Upload.Into != "" {
				key, what = uploadReplayKey(index, op.Upload.Into, entry.URLBefore), fmt.Sprintf("upload into %q", op.Upload.Into)
			}
		}
		switch {
		case key == "", entry.Status != OperationDone:
			// A skipped operation has nothing to replay.
		case !entry.Recordable:
			exported.warnf("operation %d (%s) has no recording, so every run asks the model for it", sessionIndex, what)
		default:
			exported.recordings[key] = entry.Actions
			item.Recorded = true
			if previous != "" && pagePath(previous) != pagePath(entry.URLBefore) {
				exported.warnf("the page changed from %s to %s before operation %d outside the exported operations, so the first run may ask the model for it; an expect: {url: ...} before it makes the step wait for that page",
					previous, entry.URLBefore, sessionIndex)
			}
		}
		if key != "" && entry.Status == OperationDone {
			exported.noteCommits(key, index, op, entry)
		}
		if entry.URLAfter != "" {
			previous = entry.URLAfter
		}
		exported.ops = append(exported.ops, item)
	}
	if len(exported.step.With.Do) == 0 {
		return nil, &SessionError{Code: CodeExportInvalid, Message: "the session ran no operation that can be exported"}
	}
	exported.exportVariables(state, used)
	return exported, nil
}

// exportable reports whether an operation goes into the exported step: it
// succeeded, or its when did not hold, and it acted on the page rather than
// only looking at it.
func exportable(entry sessionOp, op operation) bool {
	if op.Screenshot != "" {
		return false
	}
	return entry.Status == OperationDone || (entry.Status == OperationSkipped && op.When != nil)
}

func exportBrowser(options browserOptions) *exportedBrowser {
	if options.Executable == "" && options.Viewport == nil && options.Proxy == "" && len(options.AllowedDomains) == 0 && options.Profile == "" {
		return nil
	}
	return &exportedBrowser{
		Executable:     options.Executable,
		Viewport:       options.Viewport,
		Proxy:          options.Proxy,
		AllowedDomains: options.AllowedDomains,
		Profile:        options.Profile,
	}
}

// exportVariables writes each variable the step's acts use as a reference
// the DAG fills in: an environment variable the session read becomes a
// secret of the DAG, and a literal value a parameter the DAG sets.
func (e *exportedSession) exportVariables(state sessionState, used map[string]bool) {
	if len(used) == 0 {
		return
	}
	e.step.With.Variables = map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(used)) {
		variable := ExportedVariable{Name: name}
		if env, ok := state.EnvVariables[name]; ok {
			variable.Value = "${" + env + "}"
			variable.Note = fmt.Sprintf("declare %s under the DAG's secrets, such as {name: %s, provider: env, key: %s}", env, env, env)
		} else {
			// A parameter of the same name, which no system variable such
			// as USER shadows.
			variable.Value = "${" + name + "}"
			variable.Note = fmt.Sprintf("give the DAG a parameter %s holding the value the session was given", name)
		}
		e.step.With.Variables[name] = variable.Value
		e.variables = append(e.variables, variable)
	}
}

// check runs the checks a step's with block gets when its DAG loads, naming
// the session's operations in what it reports.
func (e *exportedSession) check() error {
	data, err := json.Marshal(e.step.With)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg, err := parseConfig(raw)
	if err == nil {
		err = cfg.validate()
	}
	if err == nil {
		return nil
	}
	message := doReference.ReplaceAllStringFunc(err.Error(), func(ref string) string {
		index, _ := strconv.Atoi(doReference.FindStringSubmatch(ref)[1])
		if index >= len(e.sessionIndex) {
			return ref
		}
		return fmt.Sprintf("operation %d", e.sessionIndex[index])
	})
	return &SessionError{Code: CodeExportInvalid, Message: message + "; leave an operation out with skip"}
}

// doReference is how a step's checks name one of its operations.
var doReference = regexp.MustCompile(`do\[(\d+)\]`)

// noteCommits keeps the writes the operation at index of the step sent or
// tried to send as commit points to approve, with its recording.
func (e *exportedSession) noteCommits(key string, index int, op operation, entry sessionOp) {
	var actions json.RawMessage
	if entry.Recordable {
		actions, _ = json.Marshal(entry.Actions)
	}
	for _, signature := range entry.Writes {
		e.commits = append(e.commits, writegate.CommitPoint{
			Key: key, Op: index, Operation: opWords(op), Page: pagePath(entry.URLBefore),
			Signature: signature, Actions: actions, SeenAt: entry.At,
		})
	}
}

func (e *exportedSession) warnf(format string, args ...any) {
	e.warnings = append(e.warnings, fmt.Sprintf(format, args...))
}

// pagePath is a page's address without its query and fragment, the part a
// recording is keyed by.
func pagePath(pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return pageURL
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	return parsed.String()
}
