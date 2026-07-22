// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const maxIDGenerationAttempts = 32

// ServiceOption configures Controller lifecycle behavior.
type ServiceOption func(*Service)

// WithClock overrides lifecycle timestamps.
func WithClock(now func() time.Time) ServiceOption {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

// WithIDGenerator overrides Controller identity generation.
func WithIDGenerator(generator func() (string, error)) ServiceOption {
	return func(service *Service) {
		if generator != nil {
			service.generateID = generator
		}
	}
}

// Service owns locked Controller definition and lifecycle mutations.
type Service struct {
	definitions DefinitionStore
	runtimes    RuntimeStore
	locker      ResourceLocker
	validator   *Validator
	now         func() time.Time
	generateID  func() (string, error)
}

// NewService creates a Controller lifecycle service.
func NewService(
	definitions DefinitionStore,
	runtimes RuntimeStore,
	locker ResourceLocker,
	validator *Validator,
	opts ...ServiceOption,
) *Service {
	if validator == nil {
		validator = NewValidator(nil)
	}
	service := &Service{
		definitions: definitions,
		runtimes:    runtimes,
		locker:      locker,
		validator:   validator,
		now:         time.Now,
		generateID:  generateControllerID,
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// Create validates an ID-less YAML document, generates its identity, and persists it.
func (s *Service) Create(ctx context.Context, data []byte) (*Detail, error) {
	draft, err := ParseCreateDefinition(data)
	if err != nil {
		return nil, err
	}
	for range maxIDGenerationAttempts {
		id, err := s.generateID()
		if err != nil {
			return nil, fmt.Errorf("generate Controller ID: %w", err)
		}
		definition := *draft
		definition.ID = id
		if err := s.validator.Validate(ctx, &definition); err != nil {
			return nil, err
		}
		persisted, err := MarshalDefinition(&definition)
		if err != nil {
			return nil, err
		}
		err = s.locker.WithLock(ctx, id, func() error {
			return s.definitions.Create(ctx, id, persisted)
		})
		if errors.Is(err, ErrAlreadyExists) {
			continue
		}
		if err != nil {
			return nil, err
		}
		detail := &Detail{RawYAML: string(persisted), Definition: definition}
		s.setResourceUpdatedAt(ctx, detail)
		return detail, nil
	}
	return nil, fmt.Errorf("generate unused Controller ID after %d attempts", maxIDGenerationAttempts)
}

// List returns all Controller definitions with compact lifecycle projections.
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	ids, err := s.definitions.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(ids))
	for _, id := range ids {
		detail, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, s.summary(detail))
	}
	return items, nil
}

// ListVisible returns compact views only for definitions accepted by include.
// Invalid definitions are omitted because their workspace cannot be trusted.
func (s *Service) ListVisible(ctx context.Context, include func(Definition) bool) ([]Summary, error) {
	ids, err := s.definitions.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(ids))
	for _, id := range ids {
		definition, err := s.GetDefinition(ctx, id)
		if err != nil {
			if errors.Is(err, ErrDefinitionCorrupt) {
				continue
			}
			return nil, err
		}
		if include != nil && !include(*definition) {
			continue
		}
		detail, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, s.summary(detail))
	}
	return items, nil
}

// GetDefinition returns a definition without reading its runtime snapshot.
func (s *Service) GetDefinition(ctx context.Context, id string) (*Definition, error) {
	return s.loadDefinition(ctx, id)
}

// Get returns one persisted definition and its API-safe runtime snapshot.
func (s *Service) Get(ctx context.Context, id string) (*Detail, error) {
	data, err := s.definitions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	definition, err := ParseDefinition(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDefinitionCorrupt, id, err)
	}
	if definition.ID != id {
		return nil, fmt.Errorf("%w: definition ID %q does not match resource %q", ErrDefinitionCorrupt, definition.ID, id)
	}
	runtime, err := s.runtimes.Get(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	detail, err := detailFromRecords(data, definition, runtime)
	if err != nil {
		return nil, err
	}
	s.setResourceUpdatedAt(ctx, detail)
	return detail, nil
}

// Update replaces an inactive Controller definition with a validated persisted document.
func (s *Service) Update(ctx context.Context, id string, data []byte) (*Detail, error) {
	next, err := ParseDefinition(data)
	if err != nil {
		return nil, err
	}
	if next.ID != id {
		return nil, fmt.Errorf("%w: definition ID %q does not match resource %q", ErrInvalidDefinition, next.ID, id)
	}
	var detail *Detail
	err = s.locker.WithLock(ctx, id, func() error {
		currentData, err := s.definitions.Get(ctx, id)
		if err != nil {
			return err
		}
		current, err := ParseDefinition(currentData)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrDefinitionCorrupt, id, err)
		}
		runtime, err := s.runtimeOrNil(ctx, id)
		if err != nil {
			return err
		}
		if err := validateDefinitionRuntimeIdentity(current, runtime); err != nil {
			return err
		}
		if !runtimeAllowsDefinitionMutation(runtime) {
			return fmt.Errorf("%w: %s", ErrActiveController, id)
		}
		if current.Workspace() != next.Workspace() {
			return fmt.Errorf("%w: Controller workspace is immutable", ErrInvalidLifecycle)
		}
		if err := s.validator.Validate(ctx, next); err != nil {
			return err
		}
		if err := s.definitions.Update(ctx, id, data); err != nil {
			return err
		}
		detail, err = detailFromRecords(data, next, runtime)
		return err
	})
	if detail != nil {
		s.setResourceUpdatedAt(ctx, detail)
	}
	return detail, err
}

// Delete removes an inactive definition and runtime snapshot. Child DAG history is untouched.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.locker.WithLock(ctx, id, func() error {
		data, definitionErr := s.definitions.Get(ctx, id)
		runtime, runtimeErr := s.runtimes.Get(ctx, id)
		if definitionErr != nil && !errors.Is(definitionErr, ErrNotFound) {
			return definitionErr
		}
		if errors.Is(definitionErr, ErrNotFound) && errors.Is(runtimeErr, ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if runtimeErr != nil {
			if !errors.Is(runtimeErr, ErrNotFound) {
				return runtimeErr
			}
			runtime = nil
		}
		if definitionErr == nil {
			definition, err := ParseDefinition(data)
			if err != nil {
				return fmt.Errorf("%w: %s: %v", ErrDefinitionCorrupt, id, err)
			}
			if err := validateDefinitionRuntimeIdentity(definition, runtime); err != nil {
				return err
			}
		}
		if !runtimeAllowsDefinitionMutation(runtime) {
			return fmt.Errorf("%w: %s", ErrActiveController, id)
		}
		if runtime != nil {
			if err := s.runtimes.Delete(ctx, id); err != nil {
				return err
			}
		}
		if definitionErr == nil {
			if err := s.definitions.Delete(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Start replaces any settled snapshot with a new execution beginning at default.
func (s *Service) Start(ctx context.Context, id, prompt string) (*RuntimeView, error) {
	if err := ValidatePrompt(prompt); err != nil {
		return nil, err
	}
	var view *RuntimeView
	err := s.locker.WithLock(ctx, id, func() error {
		definition, err := s.loadDefinition(ctx, id)
		if err != nil {
			return err
		}
		if err := s.validator.Validate(ctx, definition); err != nil {
			return err
		}
		current, err := s.runtimeOrNil(ctx, id)
		if err != nil {
			return err
		}
		if err := validateDefinitionRuntimeIdentity(definition, current); err != nil {
			return err
		}
		if !runtimeAllowsStart(current) {
			return fmt.Errorf("%w: Controller cannot start from its current lifecycle", ErrInvalidLifecycle)
		}
		now := s.now().UTC()
		next := &Runtime{
			RuntimeVersion: RuntimeVersion,
			ID:             id,
			Workspace:      definition.Workspace(),
			Status:         core.Running,
			CurrentState:   DefaultStateName,
			DAGRunRefs:     []DAGRunRef{},
			Context: []exec.LLMMessage{{
				Role:    exec.RoleUser,
				Content: prompt,
			}},
			StartedAt: now,
			UpdatedAt: now,
		}
		if err := s.runtimes.Put(ctx, next); err != nil {
			return err
		}
		public := next.Public()
		view = &public
		return nil
	})
	return view, err
}

// Prompt resumes a prompt-waiting Controller without changing its State.
func (s *Service) Prompt(ctx context.Context, id, prompt string) (*RuntimeView, error) {
	if err := ValidatePrompt(prompt); err != nil {
		return nil, err
	}
	var view *RuntimeView
	err := s.locker.WithLock(ctx, id, func() error {
		definition, err := s.loadDefinition(ctx, id)
		if err != nil {
			return err
		}
		runtime, err := s.runtimes.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: Controller has not been started", ErrInvalidLifecycle)
			}
			return err
		}
		if err := validateDefinitionRuntimeIdentity(definition, runtime); err != nil {
			return err
		}
		if runtime.Status != core.Waiting || runtime.ActiveDAGRun != nil || runtime.WaitingQuestion == nil || strings.TrimSpace(*runtime.WaitingQuestion) == "" {
			return fmt.Errorf("%w: Controller is not waiting for a prompt", ErrInvalidLifecycle)
		}
		base := cloneRuntime(runtime)
		runtime.Context = append(runtime.Context, exec.LLMMessage{Role: exec.RoleUser, Content: prompt})
		runtime.Status = core.Running
		runtime.WaitingQuestion = nil
		runtime.FinishedAt = nil
		now := s.now().UTC()
		runtime.UpdatedAt = now
		persisted, _, err := persistRuntimeCandidate(ctx, s.runtimes, base, runtime, now)
		if err != nil {
			return err
		}
		public := persisted.Public()
		view = &public
		return nil
	})
	return view, err
}

// Stop records an idempotent abort request. The runner settles active work and finishedAt.
func (s *Service) Stop(ctx context.Context, id string) (*RuntimeView, error) {
	var view *RuntimeView
	err := s.locker.WithLock(ctx, id, func() error {
		definition, err := s.loadDefinition(ctx, id)
		if err != nil {
			return err
		}
		runtime, err := s.runtimes.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: Controller has not been started", ErrInvalidLifecycle)
			}
			return err
		}
		if err := validateDefinitionRuntimeIdentity(definition, runtime); err != nil {
			return err
		}
		if runtime.Status == core.Aborted {
			public := runtime.Public()
			view = &public
			return nil
		}
		if runtime.Status != core.Running && runtime.Status != core.Waiting {
			return fmt.Errorf("%w: only running or waiting Controllers can be stopped", ErrInvalidLifecycle)
		}
		runtime.Status = core.Aborted
		runtime.WaitingQuestion = nil
		runtime.FinishedAt = nil
		runtime.UpdatedAt = s.now().UTC()
		if err := s.runtimes.Put(ctx, runtime); err != nil {
			return err
		}
		public := runtime.Public()
		view = &public
		return nil
	})
	return view, err
}

func (s *Service) loadDefinition(ctx context.Context, id string) (*Definition, error) {
	data, err := s.definitions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	definition, err := ParseDefinition(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDefinitionCorrupt, id, err)
	}
	if definition.ID != id {
		return nil, fmt.Errorf("%w: definition ID %q does not match resource %q", ErrDefinitionCorrupt, definition.ID, id)
	}
	return definition, nil
}

func (s *Service) runtimeOrNil(ctx context.Context, id string) (*Runtime, error) {
	runtime, err := s.runtimes.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return runtime, err
}

func (*Service) summary(detail *Detail) Summary {
	definition := detail.Definition
	item := Summary{
		ID:          definition.ID,
		Name:        definition.Name,
		Description: definition.Description,
		Labels:      append([]string(nil), definition.Labels...),
		Workspace:   definition.Workspace(),
		Status:      core.NotStarted,
		StatusLabel: core.NotStarted.String(),
		MaxTurns:    definition.EffectiveMaxTurns(),
	}
	if !detail.ResourceUpdatedAt.IsZero() {
		updatedAt := detail.ResourceUpdatedAt
		item.ResourceUpdatedAt = &updatedAt
	}
	if detail.Runtime == nil {
		return item
	}
	runtime := detail.Runtime
	item.Status = runtime.Status
	item.StatusLabel = runtime.StatusLabel
	item.CurrentState = runtime.CurrentState
	item.TurnCount = runtime.TurnCount
	item.WaitingQuestion = cloneStringPointer(runtime.WaitingQuestion)
	item.ActiveDAGRun = runtime.ActiveDAGRun
	if runtime.ActiveDAGRun != nil {
		item.LatestDAGRun = &DAGRunRef{
			State:    runtime.ActiveDAGRun.State,
			DAG:      runtime.ActiveDAGRun.DAG,
			DAGRunID: runtime.ActiveDAGRun.DAGRunID,
		}
	} else if len(runtime.DAGRunRefs) > 0 {
		latest := runtime.DAGRunRefs[len(runtime.DAGRunRefs)-1]
		item.LatestDAGRun = &latest
	}
	item.LastError = cloneStringPointer(runtime.LastError)
	item.FinishedAt = cloneTimePointer(runtime.FinishedAt)
	if item.ResourceUpdatedAt == nil || runtime.UpdatedAt.After(*item.ResourceUpdatedAt) {
		updatedAt := runtime.UpdatedAt
		item.ResourceUpdatedAt = &updatedAt
	}
	return item
}

func (s *Service) setResourceUpdatedAt(ctx context.Context, detail *Detail) {
	if detail == nil {
		return
	}
	if metadataStore, ok := s.definitions.(DefinitionMetadataStore); ok {
		if modifiedAt, err := metadataStore.ModifiedAt(ctx, detail.Definition.ID); err == nil {
			detail.ResourceUpdatedAt = modifiedAt
		}
	}
	if detail.Runtime != nil && detail.Runtime.UpdatedAt.After(detail.ResourceUpdatedAt) {
		detail.ResourceUpdatedAt = detail.Runtime.UpdatedAt
	}
}

func detailFromRecords(data []byte, definition *Definition, runtime *Runtime) (*Detail, error) {
	if err := validateDefinitionRuntimeIdentity(definition, runtime); err != nil {
		return nil, err
	}
	detail := &Detail{RawYAML: string(data), Definition: *definition}
	if runtime != nil {
		public := runtime.Public()
		detail.Runtime = &public
	}
	return detail, nil
}

func validateDefinitionRuntimeIdentity(definition *Definition, runtime *Runtime) error {
	if definition == nil || runtime == nil {
		return nil
	}
	if runtime.ID != definition.ID || runtime.Workspace != definition.Workspace() {
		return fmt.Errorf("%w: definition and runtime identity do not match", ErrRuntimeCorrupt)
	}
	if runtimeAllowsDefinitionMutation(runtime) {
		return nil
	}
	state, ok := definition.States[runtime.CurrentState]
	if !ok {
		return fmt.Errorf("%w: current State %q does not exist in the active definition", ErrRuntimeCorrupt, runtime.CurrentState)
	}
	if runtime.ActiveDAGRun != nil && (!slices.Contains(definition.DAGs, runtime.ActiveDAGRun.DAG) || !slices.Contains(state.DAGs, runtime.ActiveDAGRun.DAG)) {
		return fmt.Errorf("%w: active DAG %q is not allowed in current State %q", ErrRuntimeCorrupt, runtime.ActiveDAGRun.DAG, runtime.CurrentState)
	}
	return nil
}

func runtimeAllowsDefinitionMutation(runtime *Runtime) bool {
	if runtime == nil {
		return true
	}
	if runtime.ActiveDAGRun != nil {
		return false
	}
	switch runtime.Status {
	case core.Succeeded, core.Failed:
		return runtime.FinishedAt != nil
	case core.Aborted:
		return runtime.FinishedAt != nil
	case core.NotStarted, core.Running, core.Queued, core.PartiallySucceeded, core.Waiting, core.Rejected:
		return false
	}
	return false
}

func runtimeAllowsStart(runtime *Runtime) bool {
	return runtimeAllowsDefinitionMutation(runtime)
}

func generateControllerID() (string, error) {
	random := make([]byte, 10)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random)
	return "ctrl_" + strings.ToLower(encoded), nil
}
