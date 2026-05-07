// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/gitsync"
	"github.com/dagucloud/dagu/internal/service/audit"
)

// SyncService is the interface for Git sync operations.
type SyncService interface {
	Pull(ctx context.Context) (*gitsync.SyncResult, error)
	Publish(ctx context.Context, dagID, message string, force bool) (*gitsync.SyncResult, error)
	PublishAll(ctx context.Context, message string, dagIDs []string) (*gitsync.SyncResult, error)
	Discard(ctx context.Context, dagID string) error
	Forget(ctx context.Context, itemIDs []string) ([]string, error)
	Cleanup(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, itemID, message string, force bool) error
	DeleteBatch(ctx context.Context, itemIDs []string, message string, force bool) ([]string, error)
	DeleteAllMissing(ctx context.Context, message string) ([]string, error)
	Move(ctx context.Context, oldID, newID, message string, force bool) error
	GetStatus(ctx context.Context) (*gitsync.OverallStatus, error)
	GetDAGStatus(ctx context.Context, dagID string) (*gitsync.DAGState, error)
	GetDAGDiff(ctx context.Context, dagID string) (*gitsync.DAGDiff, error)
	GetConfig(ctx context.Context) (*gitsync.Config, error)
	UpdateConfig(ctx context.Context, cfg *gitsync.Config) error
	TestConnection(ctx context.Context) (*gitsync.ConnectionResult, error)
}

// errSyncNotConfigured returns an error for when sync service is not available.
var errSyncNotConfigured = &Error{
	Code:       api.ErrorCodeInternalError,
	Message:    "Git sync is not configured",
	HTTPStatus: http.StatusServiceUnavailable,
}

// requireSyncService checks if the sync service is available.
func (a *API) requireSyncService() error {
	if a.syncService == nil {
		return errSyncNotConfigured
	}
	return nil
}

// internalError creates an internal server error from an error.
func internalError(err error) *Error {
	return &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    err.Error(),
		HTTPStatus: http.StatusInternalServerError,
	}
}

// GetSyncStatus returns the overall Git sync status.
func (a *API) GetSyncStatus(ctx context.Context, _ api.GetSyncStatusRequestObject) (api.GetSyncStatusResponseObject, error) {
	if a.syncService == nil {
		return disabledSyncStatusResponse(), nil
	}

	status, err := a.syncService.GetStatus(ctx)
	if err != nil {
		return nil, internalError(err)
	}

	return api.GetSyncStatus200JSONResponse{
		Enabled:        status.Enabled,
		Repository:     ptrOf(status.Repository),
		Branch:         ptrOf(status.Branch),
		Summary:        toAPISyncSummary(status.Summary),
		LastSyncAt:     status.LastSyncAt,
		LastSyncCommit: ptrOf(status.LastSyncCommit),
		LastSyncStatus: ptrOf(status.LastSyncStatus),
		LastError:      status.LastError,
		Items:          toAPISyncItems(status.DAGs),
		Counts:         toAPISyncCounts(status.Counts),
	}, nil
}

// disabledSyncStatusResponse returns a response for when sync is disabled.
func disabledSyncStatusResponse() api.GetSyncStatus200JSONResponse {
	return api.GetSyncStatus200JSONResponse{
		Enabled: false,
		Summary: api.SyncSummarySynced,
		Items:   []api.SyncItem{},
		Counts: api.SyncStatusCounts{
			Synced:    0,
			Modified:  0,
			Untracked: 0,
			Conflict:  0,
			Missing:   0,
		},
	}
}

// SyncPull pulls changes from the remote repository.
func (a *API) SyncPull(ctx context.Context, _ api.SyncPullRequestObject) (api.SyncPullResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	result, err := a.syncService.Pull(ctx)
	if err != nil {
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_pull", map[string]any{
		"synced":    result.Synced,
		"modified":  result.Modified,
		"conflicts": result.Conflicts,
	})

	return api.SyncPull200JSONResponse(toAPISyncResult(result)), nil
}

// SyncPublishAll publishes selected DAGs.
func (a *API) SyncPublishAll(ctx context.Context, req api.SyncPublishAllRequestObject) (api.SyncPublishAllResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	message := valueOf(req.Body.Message)
	var itemIDs []string
	if req.Body.ItemIds == nil {
		status, err := a.syncService.GetStatus(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		itemIDs = collectPublishableItemIDs(status)
	} else {
		itemIDs = append(itemIDs, (*req.Body.ItemIds)...)
	}
	if len(itemIDs) == 0 {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "No modified or untracked items to publish",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	sort.Strings(itemIDs)

	result, err := a.syncService.PublishAll(ctx, message, itemIDs)
	if err != nil {
		if gitsync.IsNotEnabled(err) {
			return nil, errSyncNotConfigured
		}
		var validationErr *gitsync.ValidationError
		if errors.As(err, &validationErr) || gitsync.IsInvalidDAGID(err) || gitsync.IsDAGNotFound(err) {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
				HTTPStatus: http.StatusBadRequest,
			}
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_publish_all", map[string]any{
		"message":  message,
		"item_ids": itemIDs,
		"synced":   result.Synced,
		"modified": result.Modified,
	})

	return api.SyncPublishAll200JSONResponse(toAPISyncResult(result)), nil
}

func collectPublishableItemIDs(status *gitsync.OverallStatus) []string {
	if status == nil {
		return nil
	}
	itemIDs := make([]string, 0, len(status.DAGs))
	for id, item := range status.DAGs {
		if item == nil {
			continue
		}
		if item.Status == gitsync.StatusModified || item.Status == gitsync.StatusUntracked {
			itemIDs = append(itemIDs, id)
		}
	}
	return itemIDs
}

// SyncTestConnection tests the connection to the remote repository.
func (a *API) SyncTestConnection(ctx context.Context, _ api.SyncTestConnectionRequestObject) (api.SyncTestConnectionResponseObject, error) {
	if a.syncService == nil {
		return api.SyncTestConnection200JSONResponse{
			Success: false,
			Error:   ptrOf("Git sync is not configured"),
		}, nil
	}

	result, err := a.syncService.TestConnection(ctx)
	if err != nil {
		return api.SyncTestConnection200JSONResponse{
			Success: false,
			Error:   ptrOf(err.Error()),
		}, nil
	}

	resp := api.SyncTestConnection200JSONResponse{
		Success: result.Success,
	}
	if result.Message != "" {
		resp.Message = ptrOf(result.Message)
	}
	if result.Error != "" {
		resp.Error = ptrOf(result.Error)
	}
	return resp, nil
}

// GetSyncConfig returns the Git sync configuration.
func (a *API) GetSyncConfig(ctx context.Context, _ api.GetSyncConfigRequestObject) (api.GetSyncConfigResponseObject, error) {
	if a.syncService == nil {
		return api.GetSyncConfig200JSONResponse{Enabled: false}, nil
	}

	cfg, err := a.syncService.GetConfig(ctx)
	if err != nil {
		return nil, internalError(err)
	}

	return api.GetSyncConfig200JSONResponse(toAPISyncConfig(cfg)), nil
}

// GetSyncItemDiff returns the diff between local and remote versions of a sync item.
func (a *API) GetSyncItemDiff(ctx context.Context, req api.GetSyncItemDiffRequestObject) (api.GetSyncItemDiffResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}

	diff, err := a.syncService.GetDAGDiff(ctx, req.ItemId)
	if err != nil {
		if gitsync.IsDAGNotFound(err) {
			return api.GetSyncItemDiff404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	return api.GetSyncItemDiff200JSONResponse{
		ItemId:        diff.DAGID,
		FilePath:      syncItemFilePath(diff.DAGID, gitsync.KindForDAGID(diff.DAGID)),
		Status:        toAPISyncStatus(diff.Status),
		LocalContent:  diff.LocalContent,
		RemoteContent: ptrOf(diff.RemoteContent),
		RemoteCommit:  ptrOf(diff.RemoteCommit),
		RemoteAuthor:  ptrOf(diff.RemoteAuthor),
		RemoteMessage: ptrOf(diff.RemoteMessage),
	}, nil
}

// UpdateSyncConfig updates the Git sync configuration.
func (a *API) UpdateSyncConfig(ctx context.Context, req api.UpdateSyncConfigRequestObject) (api.UpdateSyncConfigResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	cfg, err := a.syncService.GetConfig(ctx)
	if err != nil {
		return nil, internalError(err)
	}

	applyConfigUpdates(cfg, req.Body)

	if cfg.Enabled && !cfg.IsValid() {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "Git sync configuration is incomplete - repository and branch are required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if err := a.syncService.UpdateConfig(ctx, cfg); err != nil {
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_config_update", map[string]any{
		"enabled":      cfg.Enabled,
		"repository":   cfg.Repository,
		"branch":       cfg.Branch,
		"push_enabled": cfg.PushEnabled,
	})

	return api.UpdateSyncConfig200JSONResponse(toAPISyncConfig(cfg)), nil
}

// PublishSyncItem publishes a single sync item.
func (a *API) PublishSyncItem(ctx context.Context, req api.PublishSyncItemRequestObject) (api.PublishSyncItemResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	message, force := extractPublishOptions(req.Body)

	result, err := a.syncService.Publish(ctx, req.ItemId, message, force)
	if err != nil {
		return handlePublishError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_publish", map[string]any{
		"item_id": req.ItemId,
		"message": message,
		"force":   force,
	})

	return api.PublishSyncItem200JSONResponse(toAPISyncResult(result)), nil
}

// DiscardSyncItemChanges discards local changes for a sync item.
func (a *API) DiscardSyncItemChanges(ctx context.Context, req api.DiscardSyncItemChangesRequestObject) (api.DiscardSyncItemChangesResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	if err := a.syncService.Discard(ctx, req.ItemId); err != nil {
		if gitsync.IsDAGNotFound(err) {
			return api.DiscardSyncItemChanges404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_discard", map[string]any{"item_id": req.ItemId})

	return api.DiscardSyncItemChanges200JSONResponse{
		Message: "Changes discarded successfully",
	}, nil
}

// ForgetSyncItem removes a sync item's state entry.
func (a *API) ForgetSyncItem(ctx context.Context, req api.ForgetSyncItemRequestObject) (api.ForgetSyncItemResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	forgotten, err := a.syncService.Forget(ctx, []string{req.ItemId})
	if err != nil {
		if gitsync.IsDAGNotFound(err) {
			return api.ForgetSyncItem404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: err.Error(),
			}, nil
		}
		if errors.Is(err, gitsync.ErrCannotForget) {
			return api.ForgetSyncItem400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_forget", map[string]any{
		"item_id":   req.ItemId,
		"forgotten": forgotten,
	})

	return api.ForgetSyncItem200JSONResponse{
		Message: fmt.Sprintf("Forgotten item: %s", req.ItemId),
	}, nil
}

// SyncCleanup removes all missing entries from sync state.
func (a *API) SyncCleanup(ctx context.Context, _ api.SyncCleanupRequestObject) (api.SyncCleanupResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	forgotten, err := a.syncService.Cleanup(ctx)
	if err != nil {
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_cleanup", map[string]any{
		"forgotten": forgotten,
	})

	message := fmt.Sprintf("Cleaned up %d missing item(s)", len(forgotten))
	return api.SyncCleanup200JSONResponse{
		Forgotten: forgotten,
		Message:   message,
	}, nil
}

// DeleteSyncItem deletes a sync item from remote, local, and state.
func (a *API) DeleteSyncItem(ctx context.Context, req api.DeleteSyncItemRequestObject) (api.DeleteSyncItemResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	var message string
	var force bool
	if req.Body != nil {
		if req.Body.Message != nil {
			message = *req.Body.Message
		}
		if req.Body.Force != nil {
			force = *req.Body.Force
		}
	}

	if err := a.syncService.Delete(ctx, req.ItemId, message, force); err != nil {
		if gitsync.IsDAGNotFound(err) {
			return api.DeleteSyncItem404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: err.Error(),
			}, nil
		}
		if errors.Is(err, gitsync.ErrCannotDeleteUntracked) || errors.Is(err, gitsync.ErrPushDisabled) {
			return api.DeleteSyncItem400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		var validationErr *gitsync.ValidationError
		if errors.As(err, &validationErr) {
			return api.DeleteSyncItem400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_delete", map[string]any{
		"item_id": req.ItemId,
		"force":   force,
		"message": message,
	})

	return api.DeleteSyncItem200JSONResponse{
		Message: fmt.Sprintf("Deleted item: %s", req.ItemId),
	}, nil
}

// SyncDeleteMissing deletes all missing items from remote, local, and state.
func (a *API) SyncDeleteMissing(ctx context.Context, req api.SyncDeleteMissingRequestObject) (api.SyncDeleteMissingResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	var message string
	if req.Body != nil && req.Body.Message != nil {
		message = *req.Body.Message
	}

	deleted, err := a.syncService.DeleteAllMissing(ctx, message)
	if err != nil {
		if errors.Is(err, gitsync.ErrPushDisabled) {
			return api.SyncDeleteMissing400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_delete_missing", map[string]any{
		"deleted": deleted,
	})

	if deleted == nil {
		deleted = []string{}
	}

	return api.SyncDeleteMissing200JSONResponse{
		Deleted: deleted,
		Message: fmt.Sprintf("Deleted %d missing item(s)", len(deleted)),
	}, nil
}

// SyncDeleteBatch deletes multiple items from remote, local, and state in a single commit.
func (a *API) SyncDeleteBatch(ctx context.Context, req api.SyncDeleteBatchRequestObject) (api.SyncDeleteBatchResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}

	if req.Body == nil || len(req.Body.ItemIds) == 0 {
		return api.SyncDeleteBatch400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "itemIds is required and must not be empty",
		}, nil
	}

	var message string
	var force bool
	if req.Body.Message != nil {
		message = *req.Body.Message
	}
	if req.Body.Force != nil {
		force = *req.Body.Force
	}

	deleted, err := a.syncService.DeleteBatch(ctx, req.Body.ItemIds, message, force)
	if err != nil {
		if gitsync.IsDAGNotFound(err) {
			return api.SyncDeleteBatch404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: err.Error(),
			}, nil
		}
		if gitsync.IsInvalidDAGID(err) {
			return api.SyncDeleteBatch400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		if errors.Is(err, gitsync.ErrCannotDeleteUntracked) || errors.Is(err, gitsync.ErrPushDisabled) {
			return api.SyncDeleteBatch400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		var validationErr *gitsync.ValidationError
		if errors.As(err, &validationErr) {
			return api.SyncDeleteBatch400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_delete_batch", map[string]any{
		"item_ids": req.Body.ItemIds,
		"deleted":  deleted,
		"force":    force,
		"message":  message,
	})

	if deleted == nil {
		deleted = []string{}
	}

	return api.SyncDeleteBatch200JSONResponse{
		Deleted: deleted,
		Message: fmt.Sprintf("Deleted %d item(s)", len(deleted)),
	}, nil
}

// MoveSyncItem atomically renames an item across local, remote, and state.
func (a *API) MoveSyncItem(ctx context.Context, req api.MoveSyncItemRequestObject) (api.MoveSyncItemResponseObject, error) {
	if err := a.requireSyncService(); err != nil {
		return nil, err
	}
	if err := a.requireDAGWrite(ctx); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, ErrInvalidRequestBody
	}

	var message string
	var force bool
	if req.Body.Message != nil {
		message = *req.Body.Message
	}
	if req.Body.Force != nil {
		force = *req.Body.Force
	}

	if err := a.syncService.Move(ctx, req.ItemId, req.Body.NewItemId, message, force); err != nil {
		if gitsync.IsDAGNotFound(err) {
			return api.MoveSyncItem404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: err.Error(),
			}, nil
		}
		if errors.Is(err, gitsync.ErrPushDisabled) {
			return api.MoveSyncItem400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		var validationErr *gitsync.ValidationError
		if errors.As(err, &validationErr) {
			return api.MoveSyncItem400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		var conflictErr *gitsync.ConflictError
		if errors.As(err, &conflictErr) {
			return api.MoveSyncItem409JSONResponse{
				ItemId:        conflictErr.DAGID,
				RemoteCommit:  ptrOf(conflictErr.RemoteCommit),
				RemoteAuthor:  ptrOf(conflictErr.RemoteAuthor),
				RemoteMessage: ptrOf(conflictErr.RemoteMessage),
				Message:       conflictErr.Error(),
			}, nil
		}
		if gitsync.IsInvalidDAGID(err) {
			return api.MoveSyncItem400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: err.Error(),
			}, nil
		}
		return nil, internalError(err)
	}

	a.logAudit(ctx, audit.CategoryGitSync, "sync_move", map[string]any{
		"old_item_id": req.ItemId,
		"new_item_id": req.Body.NewItemId,
		"force":       force,
		"message":     message,
	})

	return api.MoveSyncItem200JSONResponse{
		Message: fmt.Sprintf("Moved %s to %s", req.ItemId, req.Body.NewItemId),
	}, nil
}

// Helper functions for type conversion

func toAPISyncSummary(s gitsync.SummaryStatus) api.SyncSummary {
	switch s {
	case gitsync.SummarySynced:
		return api.SyncSummarySynced
	case gitsync.SummaryPending:
		return api.SyncSummaryPending
	case gitsync.SummaryConflict:
		return api.SyncSummaryConflict
	case gitsync.SummaryMissing:
		return api.SyncSummaryMissing
	case gitsync.SummaryError:
		return api.SyncSummaryError
	default:
		return api.SyncSummarySynced
	}
}

func toAPISyncStatus(s gitsync.SyncStatus) api.SyncStatus {
	switch s {
	case gitsync.StatusSynced:
		return api.SyncStatusSynced
	case gitsync.StatusModified:
		return api.SyncStatusModified
	case gitsync.StatusUntracked:
		return api.SyncStatusUntracked
	case gitsync.StatusConflict:
		return api.SyncStatusConflict
	case gitsync.StatusMissing:
		return api.SyncStatusMissing
	default:
		return api.SyncStatusSynced
	}
}

func resolveKind(itemID string, kind gitsync.DAGKind) gitsync.DAGKind {
	if kind == "" {
		return gitsync.KindForDAGID(itemID)
	}
	return kind
}

func toAPISyncItemKind(itemID string, kind gitsync.DAGKind) api.SyncItemKind {
	kind = resolveKind(itemID, kind)

	switch kind {
	case gitsync.DAGKindMemory:
		return api.SyncItemKindMemory
	case gitsync.DAGKindSkill:
		return api.SyncItemKindSkill
	case gitsync.DAGKindSoul:
		return api.SyncItemKindSoul
	case gitsync.DAGKindDoc:
		return api.SyncItemKindDoc
	case gitsync.DAGKindConfig:
		return api.SyncItemKindConfig
	case gitsync.DAGKindDAG:
		return api.SyncItemKindDag
	default:
		return api.SyncItemKindDag
	}
}

func syncItemFilePath(itemID string, kind gitsync.DAGKind) string {
	kind = resolveKind(itemID, kind)
	ext := ".yaml"
	switch kind {
	case gitsync.DAGKindMemory, gitsync.DAGKindSkill, gitsync.DAGKindSoul, gitsync.DAGKindDoc:
		ext = ".md"
	case gitsync.DAGKindDAG, gitsync.DAGKindConfig:
		// default .yaml extension
	}
	return itemID + ext
}

func toAPISyncItems(states map[string]*gitsync.DAGState) []api.SyncItem {
	if states == nil {
		return []api.SyncItem{}
	}

	result := make([]api.SyncItem, 0, len(states))
	for itemID, state := range states {
		if state == nil {
			continue
		}
		filePath := syncItemFilePath(itemID, state.Kind)
		item := api.SyncItem{
			ItemId:             itemID,
			FilePath:           filePath,
			DisplayName:        filePath,
			Status:             toAPISyncStatus(state.Status),
			Kind:               toAPISyncItemKind(itemID, state.Kind),
			BaseCommit:         ptrOf(state.BaseCommit),
			LastSyncedHash:     ptrOf(state.LastSyncedHash),
			LastSyncedAt:       state.LastSyncedAt,
			ModifiedAt:         state.ModifiedAt,
			LocalHash:          ptrOf(state.LocalHash),
			RemoteCommit:       ptrOf(state.RemoteCommit),
			RemoteAuthor:       ptrOf(state.RemoteAuthor),
			RemoteMessage:      ptrOf(state.RemoteMessage),
			ConflictDetectedAt: state.ConflictDetectedAt,
			MissingAt:          state.MissingAt,
		}
		if state.PreviousStatus != "" {
			item.PreviousStatus = ptrOf(api.SyncStatus(state.PreviousStatus))
		}
		result = append(result, item)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].FilePath < result[j].FilePath
	})

	return result
}

func toAPISyncCounts(counts gitsync.StatusCounts) api.SyncStatusCounts {
	return api.SyncStatusCounts{
		Synced:    counts.Synced,
		Modified:  counts.Modified,
		Untracked: counts.Untracked,
		Conflict:  counts.Conflict,
		Missing:   counts.Missing,
	}
}

func toAPISyncResult(result *gitsync.SyncResult) api.SyncResultResponse {
	return api.SyncResultResponse{
		Success:   result.Success,
		Message:   ptrOf(result.Message),
		Synced:    ptrOf(result.Synced),
		Modified:  ptrOf(result.Modified),
		Conflicts: ptrOf(result.Conflicts),
		Errors:    toAPISyncErrors(result.Errors),
		Timestamp: result.Timestamp,
	}
}

func toAPISyncErrors(errors []gitsync.SyncError) *[]api.SyncError {
	if len(errors) == 0 {
		return nil
	}
	result := make([]api.SyncError, len(errors))
	for i, e := range errors {
		result[i] = api.SyncError{
			ItemId:  ptrOf(e.DAGID),
			Message: e.Message,
		}
	}
	return &result
}

// toAPISyncConfig converts a gitsync.Config to the API response format.
func toAPISyncConfig(cfg *gitsync.Config) api.SyncConfigResponse {
	// Redact SSH key path - only indicate if it's configured
	var sshKeyPath *string
	if cfg.Auth.SSHKeyPath != "" {
		sshKeyPath = ptrOf("[configured]")
	}

	return api.SyncConfigResponse{
		Enabled:     cfg.Enabled,
		Repository:  ptrOf(cfg.Repository),
		Branch:      ptrOf(cfg.Branch),
		Path:        ptrOf(cfg.Path),
		PushEnabled: ptrOf(cfg.PushEnabled),
		Auth: &api.SyncAuthConfig{
			Type:       api.SyncAuthConfigType(cfg.Auth.Type),
			SshKeyPath: sshKeyPath,
		},
		AutoSync: &api.SyncAutoSyncConfig{
			Enabled:   cfg.AutoSync.Enabled,
			OnStartup: cfg.AutoSync.OnStartup,
			Interval:  cfg.AutoSync.Interval,
		},
		Commit: &api.SyncCommitConfig{
			AuthorName:  ptrOf(cfg.Commit.AuthorName),
			AuthorEmail: ptrOf(cfg.Commit.AuthorEmail),
		},
	}
}

// applyConfigUpdates applies request body updates to the config.
func applyConfigUpdates(cfg *gitsync.Config, body *api.UpdateSyncConfigJSONRequestBody) {
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	if body.Repository != nil {
		cfg.Repository = *body.Repository
	}
	if body.Branch != nil {
		cfg.Branch = *body.Branch
	}
	if body.Path != nil {
		cfg.Path = *body.Path
	}
	if body.PushEnabled != nil {
		cfg.PushEnabled = *body.PushEnabled
	}
	if body.Auth != nil {
		cfg.Auth.Type = string(body.Auth.Type)
		if body.Auth.Token != nil {
			cfg.Auth.Token = *body.Auth.Token
		}
		if body.Auth.SshKeyPath != nil {
			cfg.Auth.SSHKeyPath = *body.Auth.SshKeyPath
		}
	}
	if body.AutoSync != nil {
		cfg.AutoSync.Enabled = body.AutoSync.Enabled
		cfg.AutoSync.OnStartup = body.AutoSync.OnStartup
		cfg.AutoSync.Interval = body.AutoSync.Interval
	}
	if body.Commit != nil {
		if body.Commit.AuthorName != nil {
			cfg.Commit.AuthorName = *body.Commit.AuthorName
		}
		if body.Commit.AuthorEmail != nil {
			cfg.Commit.AuthorEmail = *body.Commit.AuthorEmail
		}
	}
}

// extractPublishOptions extracts message and force options from the request body.
func extractPublishOptions(body *api.PublishSyncItemJSONRequestBody) (message string, force bool) {
	if body == nil {
		return "", false
	}
	if body.Message != nil {
		message = *body.Message
	}
	if body.Force != nil {
		force = *body.Force
	}
	return message, force
}

// handlePublishError handles errors from the publish operation.
func handlePublishError(err error) (api.PublishSyncItemResponseObject, error) {
	var conflictErr *gitsync.ConflictError
	if errors.As(err, &conflictErr) {
		return api.PublishSyncItem409JSONResponse{
			ItemId:        conflictErr.DAGID,
			RemoteCommit:  ptrOf(conflictErr.RemoteCommit),
			RemoteAuthor:  ptrOf(conflictErr.RemoteAuthor),
			RemoteMessage: ptrOf(conflictErr.RemoteMessage),
			Message:       conflictErr.Error(),
		}, nil
	}
	if gitsync.IsDAGNotFound(err) {
		return api.PublishSyncItem404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: err.Error(),
		}, nil
	}
	return nil, internalError(err)
}
