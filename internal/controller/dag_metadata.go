// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
)

// DAGMetadata contains the current DAG definition facts used by Controllers.
type DAGMetadata struct {
	FileName    string
	Name        string
	Workspace   string
	Description string
	ParamDefs   []core.ParamDef
	ParamSchema json.RawMessage
}

// DAGResolver resolves current metadata for a Controller DAG fileName.
type DAGResolver func(ctx context.Context, fileName string) (DAGMetadata, error)

// NewDAGStoreResolver adapts the existing DAG store to current Controller metadata.
func NewDAGStoreResolver(store exec.DAGStore) DAGResolver {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, fileName string) (DAGMetadata, error) {
		dag, err := store.GetDetails(ctx, fileName, spec.WithoutEval())
		if err != nil {
			return DAGMetadata{}, err
		}
		if dag == nil {
			return DAGMetadata{}, fmt.Errorf("DAG %q resolved to nil", fileName)
		}
		workspaceName, state := exec.WorkspaceLabelFromLabels(dag.Labels)
		if state == exec.WorkspaceLabelInvalid {
			return DAGMetadata{}, fmt.Errorf("DAG %q has invalid workspace labels", fileName)
		}
		return DAGMetadata{
			FileName:    dag.FileName(),
			Name:        dag.Name,
			Workspace:   workspaceName,
			Description: dag.Description,
			ParamDefs:   append([]core.ParamDef(nil), dag.ParamDefs...),
			ParamSchema: append(json.RawMessage(nil), dag.ParamSchema...),
		}, nil
	}
}
