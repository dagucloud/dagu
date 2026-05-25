// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

const (
	executorType = "state"

	opGet    = "get"
	opSet    = "set"
	opDelete = "delete"
	opList   = "list"
	opDiff   = "diff"
)

var (
	errConfig      = errors.New("state: configuration error")
	errUnsupported = errors.New("state: unsupported operation")
)

var _ executor.Executor = (*executorImpl)(nil)

type executorImpl struct {
	mu      sync.Mutex
	stdout  io.Writer
	stderr  io.Writer
	cfg     config
	op      string
	store   dagstate.Store
	dagName string
	root    dagstate.Ref
	source  *dagstate.UpdateSource
}

type entryOutput struct {
	Scope     dagstate.Scope   `json:"scope"`
	Namespace string           `json:"namespace"`
	Key       string           `json:"key"`
	Version   int64            `json:"version"`
	Hash      string           `json:"hash"`
	CreatedAt string           `json:"createdAt"`
	UpdatedAt string           `json:"updatedAt"`
	Value     *json.RawMessage `json:"value,omitempty"`
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
}

func newExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	cfg := config{}
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}

	op := stepOperation(step)
	if err := validateConfig(op, cfg); err != nil {
		return nil, err
	}

	rCtx := runtime.GetDAGContext(ctx)
	if rCtx.StateStore == nil {
		return nil, fmt.Errorf("%w: state store is not configured", errConfig)
	}

	env := runtime.GetEnv(ctx)
	stepName := step.Name
	if env.Step.Name != "" {
		stepName = env.Step.Name
	}

	dagName := ""
	if rCtx.DAG != nil {
		dagName = rCtx.DAG.Name
	}

	return &executorImpl{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		cfg:     cfg,
		op:      op,
		store:   rCtx.StateStore,
		dagName: dagName,
		root: dagstate.Ref{
			Scope:     dagstate.ScopeRootDAG,
			Namespace: rCtx.RootDAGRun.Name,
			Key:       rCtx.RootDAGRun.ID,
		},
		source: &dagstate.UpdateSource{
			DAGName:  dagName,
			DAGRunID: rCtx.DAGRunID,
			StepName: stepName,
		},
	}, nil
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	cfg := config{}
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return err
	}
	return validateConfig(stepOperation(step), cfg)
}

func stepOperation(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

func (e *executorImpl) SetStdout(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stdout = out
}

func (e *executorImpl) SetStderr(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stderr = out
}

func (*executorImpl) Kill(os.Signal) error {
	return nil
}

func (e *executorImpl) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	switch e.op {
	case opGet:
		return e.runGet(ctx)
	case opSet:
		return e.runSet(ctx)
	case opDelete:
		return e.runDelete(ctx)
	case opList:
		return e.runList(ctx)
	case opDiff:
		return e.runDiff(ctx)
	default:
		return fmt.Errorf("%w: %q", errUnsupported, e.op)
	}
}

func (e *executorImpl) runGet(ctx context.Context) error {
	ref, err := e.resolveRef(e.cfg.Key)
	if err != nil {
		return err
	}
	entry, err := e.store.Get(ctx, ref)
	if err != nil {
		if !errors.Is(err, dagstate.ErrNotFound) {
			return err
		}
		if e.cfg.Required {
			return err
		}
		var value json.RawMessage
		if e.cfg.hasDefault {
			value, err = marshalStateValue(e.cfg.Default)
			if err != nil {
				return err
			}
		}
		return e.writeJSON(struct {
			Operation string          `json:"operation"`
			Scope     dagstate.Scope  `json:"scope"`
			Namespace string          `json:"namespace"`
			Key       string          `json:"key"`
			Found     bool            `json:"found"`
			Value     json.RawMessage `json:"value,omitempty"`
		}{
			Operation: opGet,
			Scope:     ref.Scope,
			Namespace: ref.Namespace,
			Key:       ref.Key,
			Found:     false,
			Value:     value,
		})
	}

	return e.writeJSON(struct {
		Operation string          `json:"operation"`
		Scope     dagstate.Scope  `json:"scope"`
		Namespace string          `json:"namespace"`
		Key       string          `json:"key"`
		Found     bool            `json:"found"`
		Version   int64           `json:"version"`
		Hash      string          `json:"hash"`
		Value     json.RawMessage `json:"value"`
	}{
		Operation: opGet,
		Scope:     entry.Scope,
		Namespace: entry.Namespace,
		Key:       entry.Key,
		Found:     true,
		Version:   entry.Version,
		Hash:      entry.Hash,
		Value:     entry.Value,
	})
}

func (e *executorImpl) runSet(ctx context.Context) error {
	ref, err := e.resolveRef(e.cfg.Key)
	if err != nil {
		return err
	}
	value, err := marshalStateValue(e.cfg.Value)
	if err != nil {
		return err
	}
	entry, err := e.store.Put(ctx, ref, value, dagstate.PutOptions{
		ExpectedVersion: e.cfg.ExpectedVersion,
		CreateOnly:      e.cfg.CreateOnly,
		UpdatedBy:       e.source,
	})
	if err != nil {
		return err
	}
	return e.writeJSON(struct {
		Operation string         `json:"operation"`
		Scope     dagstate.Scope `json:"scope"`
		Namespace string         `json:"namespace"`
		Key       string         `json:"key"`
		Version   int64          `json:"version"`
		Hash      string         `json:"hash"`
		Created   bool           `json:"created"`
	}{
		Operation: opSet,
		Scope:     entry.Scope,
		Namespace: entry.Namespace,
		Key:       entry.Key,
		Version:   entry.Version,
		Hash:      entry.Hash,
		Created:   entry.Version == 1,
	})
}

func (e *executorImpl) runDelete(ctx context.Context) error {
	ref, err := e.resolveRef(e.cfg.Key)
	if err != nil {
		return err
	}
	deleted, err := e.store.Delete(ctx, ref)
	if err != nil {
		return err
	}
	return e.writeJSON(struct {
		Operation string         `json:"operation"`
		Scope     dagstate.Scope `json:"scope"`
		Namespace string         `json:"namespace"`
		Key       string         `json:"key"`
		Deleted   bool           `json:"deleted"`
	}{
		Operation: opDelete,
		Scope:     ref.Scope,
		Namespace: ref.Namespace,
		Key:       ref.Key,
		Deleted:   deleted,
	})
}

func (e *executorImpl) runList(ctx context.Context) error {
	scope, namespace, err := e.resolveScope()
	if err != nil {
		return err
	}
	entries, err := e.store.List(ctx, dagstate.ListOptions{
		Scope:     scope,
		Namespace: namespace,
		KeyPrefix: strings.TrimSpace(e.cfg.Prefix),
		Limit:     e.cfg.Limit,
	})
	if err != nil {
		return err
	}

	out := make([]entryOutput, 0, len(entries))
	for _, entry := range entries {
		item := entryOutput{
			Scope:     entry.Scope,
			Namespace: entry.Namespace,
			Key:       entry.Key,
			Version:   entry.Version,
			Hash:      entry.Hash,
			CreatedAt: entry.CreatedAt.Format(timeFormatRFC3339Nano),
			UpdatedAt: entry.UpdatedAt.Format(timeFormatRFC3339Nano),
		}
		if e.cfg.IncludeValues {
			value := append(json.RawMessage(nil), entry.Value...)
			item.Value = &value
		}
		out = append(out, item)
	}

	return e.writeJSON(struct {
		Operation string         `json:"operation"`
		Scope     dagstate.Scope `json:"scope"`
		Namespace string         `json:"namespace"`
		Prefix    string         `json:"prefix,omitempty"`
		Entries   []entryOutput  `json:"entries"`
	}{
		Operation: opList,
		Scope:     scope,
		Namespace: namespace,
		Prefix:    strings.TrimSpace(e.cfg.Prefix),
		Entries:   out,
	})
}

func (e *executorImpl) runDiff(ctx context.Context) error {
	ref, err := e.resolveRef(e.cfg.Key)
	if err != nil {
		return err
	}
	current, err := marshalStateValue(e.cfg.Value)
	if err != nil {
		return err
	}
	currentHash := dagstate.HashValue(current)

	previous, err := e.store.Get(ctx, ref)
	if err != nil && !errors.Is(err, dagstate.ErrNotFound) {
		return err
	}
	if errors.Is(err, dagstate.ErrNotFound) {
		if e.cfg.ExpectedVersion != nil && *e.cfg.ExpectedVersion != 0 {
			return dagstate.ErrConflict
		}
		var entry *dagstate.Entry
		if e.shouldUpdateDiff() {
			expected := int64(0)
			entry, err = e.store.Put(ctx, ref, current, dagstate.PutOptions{
				ExpectedVersion: &expected,
				UpdatedBy:       e.source,
			})
			if err != nil {
				return err
			}
		}
		return e.writeDiffResult(ref, nil, entry, current, true)
	}

	changed := previous.Hash != currentHash
	if e.cfg.ExpectedVersion != nil && previous.Version != *e.cfg.ExpectedVersion {
		return dagstate.ErrConflict
	}
	if !changed {
		return e.writeDiffResult(ref, previous, previous, current, false)
	}

	entry := previous
	if e.shouldUpdateDiff() {
		expected := previous.Version
		entry, err = e.store.Put(ctx, ref, current, dagstate.PutOptions{
			ExpectedVersion: &expected,
			UpdatedBy:       e.source,
		})
		if err != nil {
			return err
		}
	}
	return e.writeDiffResult(ref, previous, entry, current, true)
}

func (e *executorImpl) writeDiffResult(ref dagstate.Ref, previous, entry *dagstate.Entry, current json.RawMessage, changed bool) error {
	var previousValue json.RawMessage
	var previousVersion int64
	var version int64
	var hash string
	foundPrevious := previous != nil
	if previous != nil {
		previousValue = previous.Value
		previousVersion = previous.Version
	}
	if entry != nil {
		version = entry.Version
		hash = entry.Hash
	}

	return e.writeJSON(struct {
		Operation       string          `json:"operation"`
		Scope           dagstate.Scope  `json:"scope"`
		Namespace       string          `json:"namespace"`
		Key             string          `json:"key"`
		Changed         bool            `json:"changed"`
		FoundPrevious   bool            `json:"foundPrevious"`
		PreviousVersion int64           `json:"previousVersion,omitempty"`
		Version         int64           `json:"version,omitempty"`
		Hash            string          `json:"hash,omitempty"`
		Previous        json.RawMessage `json:"previous,omitempty"`
		Current         json.RawMessage `json:"current"`
	}{
		Operation:       opDiff,
		Scope:           ref.Scope,
		Namespace:       ref.Namespace,
		Key:             ref.Key,
		Changed:         changed,
		FoundPrevious:   foundPrevious,
		PreviousVersion: previousVersion,
		Version:         version,
		Hash:            hash,
		Previous:        previousValue,
		Current:         current,
	})
}

func (e *executorImpl) shouldUpdateDiff() bool {
	return e.cfg.Update == nil || *e.cfg.Update
}

func (e *executorImpl) resolveRef(key string) (dagstate.Ref, error) {
	scope, namespace, err := e.resolveScope()
	if err != nil {
		return dagstate.Ref{}, err
	}
	ref := dagstate.Ref{
		Scope:     scope,
		Namespace: namespace,
		Key:       strings.TrimSpace(key),
	}
	if err := ref.Validate(); err != nil {
		return dagstate.Ref{}, err
	}
	return ref, nil
}

func (e *executorImpl) resolveScope() (dagstate.Scope, string, error) {
	scope := dagstate.Scope(strings.TrimSpace(e.cfg.Scope))
	if scope == "" {
		scope = dagstate.ScopeDAG
	}
	namespace := strings.TrimSpace(e.cfg.Namespace)
	switch scope {
	case dagstate.ScopeDAG:
		if namespace == "" {
			namespace = e.dagName
		}
	case dagstate.ScopeRootDAG:
		if namespace == "" {
			namespace = strings.TrimSpace(e.root.Namespace)
		}
		if namespace == "" {
			namespace = e.dagName
		}
	case dagstate.ScopeGlobal:
		if namespace == "" {
			namespace = dagstate.DefaultGlobalNamespace
		}
	case dagstate.ScopeCustom:
		if namespace == "" {
			return "", "", fmt.Errorf("%w: namespace is required for custom scope", errConfig)
		}
	default:
		return "", "", fmt.Errorf("%w: unsupported scope %q", errConfig, scope)
	}
	return scope, namespace, nil
}

func marshalStateValue(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal value: %v", errConfig, err)
	}
	return dagstate.NormalizeValue(data)
}

func (e *executorImpl) writeJSON(value any) error {
	e.mu.Lock()
	out := e.stdout
	e.mu.Unlock()

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}

const timeFormatRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
