// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// DAGRepository provides application-level access to DAG definitions.
type DAGRepository struct {
	store   DAGDefinitionStore
	options DAGRepositoryOptions
}

// NewDAGRepository creates a repository backed by the given definition store.
func NewDAGRepository(store DAGDefinitionStore, options DAGRepositoryOptions) *DAGRepository {
	return &DAGRepository{store: store, options: options}
}

func (r *DAGRepository) loadOptions(opts ...spec.LoadOption) []spec.LoadOption {
	loadOpts := make([]spec.LoadOption, 0, len(opts)+2)
	if r.options.BaseConfigPath != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(r.options.BaseConfigPath))
	}
	if r.options.WorkspaceBaseConfigDir != "" {
		loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(r.options.WorkspaceBaseConfigDir))
	}
	return append(loadOpts, opts...)
}

func repositoryLoadOptions(opts DAGLoadOptions) []spec.LoadOption {
	if opts.AllowBuildErrors {
		return []spec.LoadOption{spec.WithAllowBuildErrors()}
	}
	return nil
}

func (r *DAGRepository) Create(ctx context.Context, id string, source []byte) error {
	return r.store.Create(ctx, id, source)
}

func (r *DAGRepository) Delete(ctx context.Context, id string) error {
	return r.store.Delete(ctx, id)
}

func (r *DAGRepository) Rename(ctx context.Context, oldID, newID string) error {
	return r.store.Rename(ctx, oldID, newID)
}

func (r *DAGRepository) GetMetadata(ctx context.Context, id string) (*ir.DAG, error) {
	return r.store.GetMetadata(ctx, id)
}

func (r *DAGRepository) GetDetails(ctx context.Context, id string, opts DAGLoadOptions) (*ir.DAG, error) {
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to locate DAG %s: %w", id, err)
	}

	loadOpts := r.loadOptions(repositoryLoadOptions(opts)...)
	loadOpts = append(loadOpts, spec.WithoutEval())
	var dag *ir.DAG
	if definition.SourcePath != "" {
		loadOpts = append(loadOpts, spec.WithDefaultName(definition.ID))
		dag, err = spec.LoadYAMLAt(ctx, definition.Source, definition.SourcePath, loadOpts...)
	} else {
		loadOpts = append(loadOpts, spec.WithName(definition.ID))
		dag, err = spec.LoadYAML(ctx, definition.Source, loadOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load DAG %s: %w", id, err)
	}
	return dag, nil
}

func (r *DAGRepository) GetSpec(ctx context.Context, id string) (string, error) {
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return string(definition.Source), nil
}

func (r *DAGRepository) LoadSpec(ctx context.Context, source []byte, name string, opts DAGLoadOptions) (*ir.DAG, error) {
	loadOpts := r.loadOptions(repositoryLoadOptions(opts)...)
	loadOpts = append(loadOpts, spec.WithName(name), spec.WithoutEval())
	return spec.LoadYAML(ctx, source, loadOpts...)
}

func (r *DAGRepository) UpdateSpec(ctx context.Context, id string, source []byte) error {
	definition, err := r.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to locate DAG %s: %w", id, err)
	}

	loadOpts := r.loadOptions(spec.WithoutEval())
	var dag *ir.DAG
	if definition.SourcePath != "" {
		loadOpts = append(loadOpts, spec.WithDefaultName(definition.ID))
		dag, err = spec.LoadYAMLAt(ctx, source, definition.SourcePath, loadOpts...)
	} else {
		loadOpts = append(loadOpts, spec.WithName(id))
		dag, err = spec.LoadYAML(ctx, source, loadOpts...)
	}
	if err != nil {
		return err
	}
	if err := dag.Validate(); err != nil {
		return err
	}
	return r.store.Update(ctx, id, source)
}

func (r *DAGRepository) SetSuspended(ctx context.Context, id string, suspended bool) error {
	return r.store.SetSuspended(ctx, id, suspended)
}

func (r *DAGRepository) IsSuspended(ctx context.Context, id string) (bool, error) {
	return r.store.IsSuspended(ctx, id)
}
