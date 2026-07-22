// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/workspace"
)

const (
	definitionDirectoryName = "controllers"
	runtimeDirectoryName    = "controller-runtime"
	runtimeTerminalReserve  = 2 << 10
)

// DefinitionStore persists exact Controller YAML documents.
type DefinitionStore interface {
	Create(ctx context.Context, id string, data []byte) error
	Get(ctx context.Context, id string) ([]byte, error)
	Update(ctx context.Context, id string, data []byte) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]string, error)
}

// DefinitionMetadataStore optionally exposes filesystem definition timestamps.
type DefinitionMetadataStore interface {
	ModifiedAt(ctx context.Context, id string) (time.Time, error)
}

// RuntimeStore persists the current-or-last runtime snapshot.
type RuntimeStore interface {
	Get(ctx context.Context, id string) (*Runtime, error)
	Put(ctx context.Context, runtime *Runtime) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]string, error)
}

// ResourceLocker serializes all mutations for one Controller across processes.
type ResourceLocker interface {
	WithLock(ctx context.Context, id string, fn func() error) error
}

// FileStores contains Controller stores rooted below one DataDir.
type FileStores struct {
	Definitions DefinitionStore
	Runtimes    RuntimeStore
	Locker      ResourceLocker
}

// NewFileStores creates stores at DataDir/controllers and DataDir/controller-runtime.
func NewFileStores(dataDir string) FileStores {
	runtimeDir := filepath.Join(dataDir, runtimeDirectoryName)
	return FileStores{
		Definitions: &FileDefinitionStore{dir: filepath.Join(dataDir, definitionDirectoryName)},
		Runtimes:    &FileRuntimeStore{dir: runtimeDir},
		Locker:      &FileResourceLocker{dir: runtimeDir},
	}
}

// FileDefinitionStore stores Controller definitions as owner-only YAML files.
type FileDefinitionStore struct {
	dir string
}

// NewFileDefinitionStore creates a definition store below the supplied DataDir.
func NewFileDefinitionStore(dataDir string) *FileDefinitionStore {
	return &FileDefinitionStore{dir: filepath.Join(dataDir, definitionDirectoryName)}
}

func (s *FileDefinitionStore) Create(_ context.Context, id string, data []byte) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("create controller definition directory: %w", err)
	}
	if err := fileutil.WriteFileAtomicExclusive(path, data, 0o600); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, id)
		}
		return fmt.Errorf("create controller definition %s: %w", id, err)
	}
	return nil
}

func (s *FileDefinitionStore) Get(_ context.Context, id string) ([]byte, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is derived from a validated Controller ID under the fixed store root.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("read controller definition %s: %w", id, err)
	}
	return data, nil
}

func (s *FileDefinitionStore) Update(_ context.Context, id string, data []byte) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return fmt.Errorf("stat controller definition %s: %w", id, err)
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("update controller definition %s: %w", id, err)
	}
	return nil
}

func (s *FileDefinitionStore) Delete(_ context.Context, id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := fileutil.RemoveFileDurable(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete controller definition %s: %w", id, err)
	}
	return nil
}

func (s *FileDefinitionStore) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("list controller definitions: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".yaml")
		if !controllerIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%w: invalid definition filename %q", ErrDefinitionCorrupt, entry.Name())
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// ModifiedAt returns the persisted definition file timestamp.
func (s *FileDefinitionStore) ModifiedAt(_ context.Context, id string) (time.Time, error) {
	path, err := s.path(id)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return time.Time{}, fmt.Errorf("stat controller definition %s: %w", id, err)
	}
	return info.ModTime().UTC(), nil
}

func (s *FileDefinitionStore) path(id string) (string, error) {
	if !controllerIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: invalid Controller ID %q", ErrInvalidDefinition, id)
	}
	return filepath.Join(s.dir, id+".yaml"), nil
}

// FileRuntimeStore stores compact runtime JSON records.
type FileRuntimeStore struct {
	dir string
}

// NewFileRuntimeStore creates a runtime store below the supplied DataDir.
func NewFileRuntimeStore(dataDir string) *FileRuntimeStore {
	return &FileRuntimeStore{dir: filepath.Join(dataDir, runtimeDirectoryName)}
}

func (s *FileRuntimeStore) Get(_ context.Context, id string) (*Runtime, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	data, err := fileutil.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("read controller runtime %s: %w", id, err)
	}
	if len(data) > MaxRuntimeBytes {
		return nil, fmt.Errorf("%w: %s: snapshot exceeds %d bytes", ErrRuntimeCorrupt, id, MaxRuntimeBytes)
	}
	runtime, err := decodeRuntime(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrRuntimeCorrupt, id, err)
	}
	if runtime.ID != id {
		return nil, fmt.Errorf("%w: runtime ID %q does not match record %q", ErrRuntimeCorrupt, runtime.ID, id)
	}
	return runtime, nil
}

func (s *FileRuntimeStore) Put(_ context.Context, runtime *Runtime) error {
	if err := ValidateRuntime(runtime); err != nil {
		return err
	}
	data, err := json.Marshal(runtime)
	if err != nil {
		return fmt.Errorf("marshal controller runtime %s: %w", runtime.ID, err)
	}
	limit := MaxRuntimeBytes
	if runtime.FinishedAt == nil {
		limit -= runtimeTerminalReserve
	}
	if len(data) > limit {
		return fmt.Errorf("%w: got %d bytes", ErrSnapshotTooLarge, len(data))
	}
	path, err := s.path(runtime.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("create controller runtime directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("write controller runtime %s: %w", runtime.ID, err)
	}
	return nil
}

func (s *FileRuntimeStore) Delete(_ context.Context, id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := fileutil.RemoveFileDurable(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete controller runtime %s: %w", id, err)
	}
	return nil
}

func (s *FileRuntimeStore) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("list controller runtimes: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !controllerIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%w: invalid runtime filename %q", ErrRuntimeCorrupt, entry.Name())
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *FileRuntimeStore) path(id string) (string, error) {
	if !controllerIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: invalid Controller ID %q", ErrNotFound, id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// FileResourceLocker uses the runtime namespace as the cross-process resource lock.
type FileResourceLocker struct {
	dir string
}

// NewFileResourceLocker creates the shared Controller lock below the supplied DataDir.
func NewFileResourceLocker(dataDir string) *FileResourceLocker {
	return &FileResourceLocker{dir: filepath.Join(dataDir, runtimeDirectoryName)}
}

func (l *FileResourceLocker) WithLock(ctx context.Context, id string, fn func() error) error {
	if !controllerIDPattern.MatchString(id) {
		return fmt.Errorf("%w: invalid Controller ID %q", ErrInvalidDefinition, id)
	}
	if fn == nil {
		return fmt.Errorf("controller lock callback is required")
	}
	lock := dirlock.New(filepath.Join(l.dir, id), &dirlock.LockOptions{
		StaleThreshold: 30 * time.Second,
		RetryInterval:  50 * time.Millisecond,
	})
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()
	return fn()
}

// ValidateRuntime verifies the persisted snapshot contract without mutating it.
func ValidateRuntime(runtime *Runtime) error {
	if runtime == nil {
		return fmt.Errorf("%w: runtime is nil", ErrRuntimeCorrupt)
	}
	if runtime.RuntimeVersion != RuntimeVersion {
		return fmt.Errorf("%w: unsupported runtimeVersion %d", ErrRuntimeCorrupt, runtime.RuntimeVersion)
	}
	if !controllerIDPattern.MatchString(runtime.ID) {
		return fmt.Errorf("%w: invalid runtime ID %q", ErrRuntimeCorrupt, runtime.ID)
	}
	if runtime.Workspace != "" {
		if err := workspace.ValidateName(runtime.Workspace); err != nil {
			return fmt.Errorf("%w: invalid workspace %q", ErrRuntimeCorrupt, runtime.Workspace)
		}
	}
	if !stateNamePattern.MatchString(runtime.CurrentState) {
		return fmt.Errorf("%w: invalid currentState %q", ErrRuntimeCorrupt, runtime.CurrentState)
	}
	if runtime.TurnCount < 0 {
		return fmt.Errorf("%w: turnCount must not be negative", ErrRuntimeCorrupt)
	}
	if runtime.StartedAt.IsZero() || runtime.UpdatedAt.IsZero() || runtime.UpdatedAt.Before(runtime.StartedAt) {
		return fmt.Errorf("%w: invalid runtime timestamps", ErrRuntimeCorrupt)
	}
	if runtime.FinishedAt != nil && (runtime.FinishedAt.IsZero() || runtime.FinishedAt.Before(runtime.StartedAt) || runtime.FinishedAt.After(runtime.UpdatedAt)) {
		return fmt.Errorf("%w: invalid finishedAt timestamp", ErrRuntimeCorrupt)
	}
	if runtime.LastError != nil && len(*runtime.LastError) > 1<<10 {
		return fmt.Errorf("%w: lastError exceeds 1024 bytes", ErrRuntimeCorrupt)
	}
	if len(runtime.DAGRunRefs) > 20 {
		return fmt.Errorf("%w: dagRunRefs exceeds 20 entries", ErrRuntimeCorrupt)
	}
	seenRunIDs := make(map[string]struct{}, len(runtime.DAGRunRefs))
	for _, ref := range runtime.DAGRunRefs {
		if !stateNamePattern.MatchString(ref.State) || !validDAGFileName(ref.DAG) || exec.ValidateDAGRunID(ref.DAGRunID) != nil {
			return fmt.Errorf("%w: incomplete dagRunRef", ErrRuntimeCorrupt)
		}
		if _, duplicate := seenRunIDs[ref.DAGRunID]; duplicate {
			return fmt.Errorf("%w: duplicate DAG run ID %q", ErrRuntimeCorrupt, ref.DAGRunID)
		}
		seenRunIDs[ref.DAGRunID] = struct{}{}
	}
	if runtime.ActiveDAGRun != nil {
		active := runtime.ActiveDAGRun
		if active.ToolCallID == "" || !validDAGFileName(active.DAG) || exec.ValidateDAGRunID(active.DAGRunID) != nil || !validJSONObject(active.Params) {
			return fmt.Errorf("%w: invalid activeDAGRun", ErrRuntimeCorrupt)
		}
	}
	pendingToolCallID, err := validateRuntimeContext(runtime.Context)
	if err != nil {
		return err
	}
	if runtime.ActiveDAGRun != nil {
		if pendingToolCallID == "" || pendingToolCallID != runtime.ActiveDAGRun.ToolCallID {
			return fmt.Errorf("%w: activeDAGRun does not match the pending route tool call", ErrRuntimeCorrupt)
		}
	} else if pendingToolCallID != "" && runtime.Status != core.Failed && runtime.Status != core.Aborted {
		return fmt.Errorf("%w: unresolved route tool call without an active child", ErrRuntimeCorrupt)
	}
	return validateRuntimeLifecycle(runtime)
}

func validateRuntimeContext(messages []exec.LLMMessage) (string, error) {
	if len(messages) == 0 || messages[0].Role != core.LLMRoleUser {
		return "", fmt.Errorf("%w: context must begin with a user message", ErrRuntimeCorrupt)
	}
	pendingToolCallID := ""
	seenToolCallIDs := make(map[string]struct{})
	for _, message := range messages {
		switch message.Role {
		case core.LLMRoleUser:
			if pendingToolCallID != "" || message.ToolCallID != "" || len(message.ToolCalls) != 0 || message.Metadata != nil {
				return "", fmt.Errorf("%w: invalid user context message", ErrRuntimeCorrupt)
			}
		case core.LLMRoleAssistant:
			if pendingToolCallID != "" || message.ToolCallID != "" || message.Content != "" || len(message.ToolCalls) != 1 {
				return "", fmt.Errorf("%w: invalid assistant context message", ErrRuntimeCorrupt)
			}
			call := message.ToolCalls[0]
			if call.ID == "" || call.Type != "function" || call.Function.Name != routeToolName || !validJSONObject(json.RawMessage(call.Function.Arguments)) {
				return "", fmt.Errorf("%w: invalid route tool call", ErrRuntimeCorrupt)
			}
			if _, duplicate := seenToolCallIDs[call.ID]; duplicate {
				return "", fmt.Errorf("%w: duplicate route tool call ID %q", ErrRuntimeCorrupt, call.ID)
			}
			seenToolCallIDs[call.ID] = struct{}{}
			pendingToolCallID = call.ID
		case core.LLMRoleTool:
			if pendingToolCallID == "" || message.ToolCallID != pendingToolCallID || len(message.ToolCalls) != 0 || message.Metadata != nil {
				return "", fmt.Errorf("%w: unmatched route tool result", ErrRuntimeCorrupt)
			}
			pendingToolCallID = ""
		case core.LLMRoleSystem:
			return "", fmt.Errorf("%w: system messages must not be persisted in context", ErrRuntimeCorrupt)
		default:
			return "", fmt.Errorf("%w: invalid persisted context role %q", ErrRuntimeCorrupt, message.Role)
		}
	}
	return pendingToolCallID, nil
}

func validateRuntimeLifecycle(runtime *Runtime) error {
	switch runtime.Status {
	case core.Running:
		if runtime.FinishedAt != nil || runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: running runtime has terminal or waiting fields", ErrRuntimeCorrupt)
		}
	case core.Waiting:
		if runtime.FinishedAt != nil {
			return fmt.Errorf("%w: waiting runtime has finishedAt", ErrRuntimeCorrupt)
		}
		if runtime.ActiveDAGRun == nil {
			if runtime.WaitingQuestion == nil || strings.TrimSpace(*runtime.WaitingQuestion) == "" {
				return fmt.Errorf("%w: prompt wait requires waitingQuestion", ErrRuntimeCorrupt)
			}
		} else if runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: child wait must not have waitingQuestion", ErrRuntimeCorrupt)
		}
	case core.Succeeded, core.Failed:
		if runtime.FinishedAt == nil || runtime.ActiveDAGRun != nil || runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: terminal runtime has inconsistent fields", ErrRuntimeCorrupt)
		}
	case core.Aborted:
		if runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: aborted runtime has waitingQuestion", ErrRuntimeCorrupt)
		}
		if runtime.FinishedAt != nil && runtime.ActiveDAGRun != nil {
			return fmt.Errorf("%w: settled aborted runtime has an active child", ErrRuntimeCorrupt)
		}
	case core.NotStarted, core.Queued, core.PartiallySucceeded, core.Rejected:
		return fmt.Errorf("%w: unsupported Controller status %s", ErrRuntimeCorrupt, runtime.Status)
	default:
		return fmt.Errorf("%w: unknown Controller status %d", ErrRuntimeCorrupt, runtime.Status)
	}
	return nil
}

func decodeRuntime(data []byte) (*Runtime, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var runtime Runtime
	if err := decoder.Decode(&runtime); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := ValidateRuntime(&runtime); err != nil {
		return nil, err
	}
	return &runtime, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("multiple JSON values are not allowed")
	}
	return nil
}

func validJSONObject(data json.RawMessage) bool {
	if !json.Valid(data) {
		return false
	}
	var object map[string]any
	return json.Unmarshal(data, &object) == nil && object != nil
}
