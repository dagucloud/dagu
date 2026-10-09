// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// PinDAG pins a DAG to the top of the DAG list for all users.
func (a *API) PinDAG(ctx context.Context, request api.PinDAGRequestObject) (api.PinDAGResponseObject, error) {
	if err := a.setDAGPinned(ctx, request.FileName, true); err != nil {
		return nil, err
	}
	return api.PinDAG204Response{}, nil
}

// UnpinDAG removes a DAG's pin for all users.
func (a *API) UnpinDAG(ctx context.Context, request api.UnpinDAGRequestObject) (api.UnpinDAGResponseObject, error) {
	if err := a.setDAGPinned(ctx, request.FileName, false); err != nil {
		return nil, err
	}
	return api.UnpinDAG204Response{}, nil
}

func (a *API) setDAGPinned(ctx context.Context, fileName string, pinned bool) error {
	if a.dagPinStore == nil {
		return &Error{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       api.ErrorCodeInternalError,
			Message:    "DAG pin store not configured",
		}
	}
	dag, err := a.dagRepository.GetDetails(ctx, fileName, persis.DAGLoadOptions{AllowBuildErrors: true})
	if err != nil {
		if errors.Is(err, persis.ErrDAGNotFound) || errors.Is(err, os.ErrNotExist) {
			return &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG %s not found", fileName),
			}
		}
		return err
	}
	workspaceName := dagWorkspaceName(dag)
	// Visibility comes first so that a hidden DAG answers 404 rather than 403.
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return err
	}
	// Pins never change DAG files, so Git sync read-only mode allows them.
	if err := a.requireWorkspaceWrite(ctx, workspaceName); err != nil {
		return err
	}

	action := "dag_unpin"
	if pinned {
		action = "dag_pin"
		err = a.dagPinStore.Pin(ctx, dagPinID(fileName))
	} else {
		err = a.dagPinStore.Unpin(ctx, dagPinID(fileName))
	}
	if err != nil {
		return fmt.Errorf("failed to update DAG pin: %w", err)
	}
	a.logAudit(ctx, audit.CategoryDAG, action, map[string]any{"dag_name": fileName})
	a.notifyDAGMutation(fileName)
	return nil
}

// dagPinID returns the pin key for a DAG file name. Listed DAGs are keyed by
// the file name without its YAML extension.
func dagPinID(fileName string) string {
	return fileutil.TrimYAMLFileExtension(fileName)
}

// pinnedDAGIDs returns the pinned DAG IDs. A store failure is logged and
// treated as no pins, so that listing DAGs never fails because of pins.
func (a *API) pinnedDAGIDs(ctx context.Context) map[string]struct{} {
	if a.dagPinStore == nil {
		return nil
	}
	pins, err := a.dagPinStore.List(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to load pinned DAGs", tag.Error(err))
		return nil
	}
	return pins
}

func (a *API) isDAGPinned(ctx context.Context, fileName string) bool {
	_, pinned := a.pinnedDAGIDs(ctx)[dagPinID(fileName)]
	return pinned
}

func (a *API) migrateDAGPinAfterRename(ctx context.Context, oldName, newName string) {
	if a.dagPinStore == nil {
		return
	}
	if err := a.dagPinStore.Rename(ctx, dagPinID(oldName), dagPinID(newName)); err != nil {
		logger.Warn(ctx, "Failed to move DAG pin after rename",
			tag.Error(err),
			slog.String("old_name", oldName),
			slog.String("new_name", newName),
		)
	}
}

func (a *API) removeDAGPinAfterDelete(ctx context.Context, fileName string) {
	if a.dagPinStore == nil {
		return
	}
	if err := a.dagPinStore.Unpin(ctx, dagPinID(fileName)); err != nil {
		logger.Warn(ctx, "Failed to remove DAG pin after delete",
			tag.Error(err),
			tag.Name(fileName),
		)
	}
}
