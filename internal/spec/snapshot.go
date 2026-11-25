// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"maps"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
)

// PrepareDAGSnapshot returns a copy without SMTP inherited from base configuration.
// Authored YAML and all other base settings are preserved, including local DAGs.
func PrepareDAGSnapshot(dag *ir.DAG) (*ir.DAG, error) {
	copy := *dag
	if len(dag.BaseConfigData) > 0 {
		base := parseBaseConfig(dag.BaseConfigData)
		if base.err != nil {
			return nil, fmt.Errorf("decode snapshot base config: %w", base.err)
		}
		if _, ok := base.values["smtp"]; ok {
			delete(base.values, "smtp")
			data, err := yaml.Marshal(base.values)
			if err != nil {
				return nil, fmt.Errorf("encode snapshot base config: %w", err)
			}
			copy.BaseConfigData = data
		}
	}
	copy.LocalDAGs = maps.Clone(dag.LocalDAGs)
	for name, child := range dag.LocalDAGs {
		filtered, err := PrepareDAGSnapshot(child)
		if err != nil {
			return nil, err
		}
		copy.LocalDAGs[name] = filtered
	}
	return &copy, nil
}

// BaseConfigValues returns the values of a base config other than SMTP, which
// PrepareDAGSnapshot and RefreshBaseSMTP take out or read anew. A base config
// and the copy that a run's snapshot keeps of it give equal values.
func BaseConfigValues(data []byte) (map[string]any, error) {
	base := parseBaseConfig(data)
	if base.err != nil {
		return nil, base.err
	}
	delete(base.values, "smtp")
	return base.values, nil
}

// RefreshBaseSMTP returns a copy with inherited SMTP from the current base
// configuration. Other saved settings and authored YAML remain unchanged.
// If no base configuration was captured, the current base settings are used.
// Rebuilding the returned DAG applies its original SMTP overrides.
func RefreshBaseSMTP(dag *ir.DAG, opts ...LoadOption) (*ir.DAG, error) {
	options := newBuildOpts(opts...)
	base, _, err := readBaseDefinitionData(options)
	if err != nil {
		return nil, err
	}
	return refreshBaseSMTP(dag, options, base, "")
}

func refreshBaseSMTP(dag *ir.DAG, opts buildOpts, base *baseConfigSource, inheritedWorkspace string) (*ir.DAG, error) {
	workspaceName := inheritedWorkspace
	if dag.BaseConfigWorkspace != nil {
		workspaceName = *dag.BaseConfigWorkspace
	} else if len(dag.YamlData) > 0 {
		docs, err := decodeDocuments(dag.YamlData)
		if err != nil {
			return nil, fmt.Errorf("decode snapshot workspace: %w", err)
		}
		if len(docs) > 0 {
			if name := workspaceNameFromDocument(docs[0].data); name != "" {
				workspaceName = name
			}
		}
	}
	workspaceBase, err := readWorkspaceBaseDefinitionData(opts, map[string]any{
		"labels": map[string]any{"workspace": workspaceName},
	})
	if err != nil {
		return nil, err
	}
	current := make(map[string]any)
	for _, source := range []*baseConfigSource{base, workspaceBase} {
		if source == nil {
			continue
		}
		if source.err != nil {
			return nil, fmt.Errorf("decode current base config: %w", source.err)
		}
		values := source.values
		if len(dag.BaseConfigData) > 0 {
			smtp, ok := values["smtp"]
			if !ok {
				continue
			}
			values = map[string]any{"smtp": smtp}
		}
		current, err = mergeDefinitionMaps(current, values)
		if err != nil {
			return nil, err
		}
	}
	saved := parseBaseConfig(dag.BaseConfigData)
	if saved.err != nil {
		return nil, fmt.Errorf("decode snapshot base config: %w", saved.err)
	}
	values := saved.values
	if values == nil {
		values = make(map[string]any)
	}
	delete(values, "smtp")
	maps.Copy(values, current)
	copy := *dag
	copy.BaseConfigWorkspace = &workspaceName
	if len(dag.BaseConfigData) > 0 || base != nil || workspaceBase != nil {
		copy.BaseConfigData, err = yaml.Marshal(values)
		if err != nil {
			return nil, fmt.Errorf("encode runtime base config: %w", err)
		}
	}
	copy.LocalDAGs = maps.Clone(dag.LocalDAGs)
	for name, child := range dag.LocalDAGs {
		refreshed, err := refreshBaseSMTP(child, opts, base, workspaceName)
		if err != nil {
			return nil, err
		}
		copy.LocalDAGs[name] = refreshed
	}
	return &copy, nil
}
