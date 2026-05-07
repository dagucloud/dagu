// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/gitsync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSyncService struct {
	publishAllFn     func(ctx context.Context, message string, itemIDs []string) (*gitsync.SyncResult, error)
	getStatusFn      func(context.Context) (*gitsync.OverallStatus, error)
	forgetFn         func(ctx context.Context, itemIDs []string) ([]string, error)
	cleanupFn        func(ctx context.Context) ([]string, error)
	deleteFn         func(ctx context.Context, itemID, message string, force bool) error
	deleteBatchFn    func(ctx context.Context, itemIDs []string, message string, force bool) ([]string, error)
	deleteAllMissing func(ctx context.Context, message string) ([]string, error)
	moveFn           func(ctx context.Context, oldID, newID, message string, force bool) error
}

func (m *mockSyncService) Pull(_ context.Context) (*gitsync.SyncResult, error) { return nil, nil }

func (m *mockSyncService) Publish(_ context.Context, _ string, _ string, _ bool) (*gitsync.SyncResult, error) {
	return nil, nil
}

func (m *mockSyncService) PublishAll(ctx context.Context, message string, itemIDs []string) (*gitsync.SyncResult, error) {
	if m.publishAllFn == nil {
		return nil, nil
	}
	return m.publishAllFn(ctx, message, itemIDs)
}

func (m *mockSyncService) Discard(_ context.Context, _ string) error { return nil }

func (m *mockSyncService) Forget(ctx context.Context, itemIDs []string) ([]string, error) {
	if m.forgetFn != nil {
		return m.forgetFn(ctx, itemIDs)
	}
	return nil, nil
}

func (m *mockSyncService) Cleanup(ctx context.Context) ([]string, error) {
	if m.cleanupFn != nil {
		return m.cleanupFn(ctx)
	}
	return nil, nil
}

func (m *mockSyncService) Delete(ctx context.Context, itemID, message string, force bool) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, itemID, message, force)
	}
	return nil
}

func (m *mockSyncService) DeleteBatch(ctx context.Context, itemIDs []string, message string, force bool) ([]string, error) {
	if m.deleteBatchFn != nil {
		return m.deleteBatchFn(ctx, itemIDs, message, force)
	}
	return nil, nil
}

func (m *mockSyncService) DeleteAllMissing(ctx context.Context, message string) ([]string, error) {
	if m.deleteAllMissing != nil {
		return m.deleteAllMissing(ctx, message)
	}
	return nil, nil
}

func (m *mockSyncService) Move(ctx context.Context, oldID, newID, message string, force bool) error {
	if m.moveFn != nil {
		return m.moveFn(ctx, oldID, newID, message, force)
	}
	return nil
}

func (m *mockSyncService) GetStatus(ctx context.Context) (*gitsync.OverallStatus, error) {
	if m.getStatusFn != nil {
		return m.getStatusFn(ctx)
	}
	return nil, nil
}

func (m *mockSyncService) GetDAGStatus(_ context.Context, _ string) (*gitsync.DAGState, error) {
	return nil, nil
}

func (m *mockSyncService) GetDAGDiff(_ context.Context, _ string) (*gitsync.DAGDiff, error) {
	return nil, nil
}

func (m *mockSyncService) GetConfig(_ context.Context) (*gitsync.Config, error) { return nil, nil }

func (m *mockSyncService) UpdateConfig(_ context.Context, _ *gitsync.Config) error { return nil }

func (m *mockSyncService) TestConnection(_ context.Context) (*gitsync.ConnectionResult, error) {
	return nil, nil
}

func newSyncAPIForTest(syncSvc SyncService) *API {
	return &API{
		config: &config.Config{
			Server: config.Server{
				Permissions: map[config.Permission]bool{
					config.PermissionWriteDAGs: true,
				},
			},
		},
		syncService: syncSvc,
	}
}

func TestSyncPublishAll_Validation(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 for nil request body", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{})
		_, err := a.SyncPublishAll(context.Background(), apigen.SyncPublishAllRequestObject{})
		require.Error(t, err)

		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	})

	t.Run("returns 400 for empty itemIds", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{})
		_, err := a.SyncPublishAll(context.Background(), apigen.SyncPublishAllRequestObject{
			Body: &apigen.SyncPublishAllRequest{
				ItemIds: ptrOf([]string{}),
			},
		})
		require.Error(t, err)

		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		assert.Contains(t, apiErr.Message, "No modified or untracked")
	})

	t.Run("defaults missing dagIds to publishable DAGs from status", func(t *testing.T) {
		t.Parallel()

		var gotIDs []string
		a := newSyncAPIForTest(&mockSyncService{
			getStatusFn: func(_ context.Context) (*gitsync.OverallStatus, error) {
				now := time.Now()
				return &gitsync.OverallStatus{
					DAGs: map[string]*gitsync.DAGState{
						"zeta":    {Status: gitsync.StatusModified, ModifiedAt: &now},
						"alpha":   {Status: gitsync.StatusUntracked, ModifiedAt: &now},
						"ignored": {Status: gitsync.StatusSynced, LastSyncedAt: &now},
					},
				}, nil
			},
			publishAllFn: func(_ context.Context, _ string, itemIDs []string) (*gitsync.SyncResult, error) {
				gotIDs = itemIDs
				return &gitsync.SyncResult{
					Success:   true,
					Synced:    itemIDs,
					Timestamp: time.Now(),
				}, nil
			},
		})

		resp, err := a.SyncPublishAll(context.Background(), apigen.SyncPublishAllRequestObject{
			Body: &apigen.SyncPublishAllRequest{
				Message: ptrOf("publish all publishable"),
			},
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.SyncPublishAll200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, []string{"alpha", "zeta"}, gotIDs)
	})

	t.Run("maps validation error from service to 400", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			publishAllFn: func(_ context.Context, _ string, _ []string) (*gitsync.SyncResult, error) {
				return nil, &gitsync.ValidationError{
					Field:   "itemIds",
					Message: "DAG \"missing\" is not tracked by git sync",
				}
			},
		})

		_, err := a.SyncPublishAll(context.Background(), apigen.SyncPublishAllRequestObject{
			Body: &apigen.SyncPublishAllRequest{
				ItemIds: ptrOf([]string{"missing"}),
			},
		})
		require.Error(t, err)

		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		assert.Contains(t, apiErr.Message, "not tracked")
	})

	t.Run("maps invalid DAG ID error from service to 400", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			publishAllFn: func(_ context.Context, _ string, _ []string) (*gitsync.SyncResult, error) {
				return nil, &gitsync.InvalidDAGIDError{
					DAGID:  "../etc/passwd",
					Reason: "path traversal is not allowed",
				}
			},
		})

		_, err := a.SyncPublishAll(context.Background(), apigen.SyncPublishAllRequestObject{
			Body: &apigen.SyncPublishAllRequest{
				ItemIds: ptrOf([]string{"../etc/passwd"}),
			},
		})
		require.Error(t, err)

		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		assert.Contains(t, apiErr.Message, "invalid DAG ID")
	})

	t.Run("passes dag IDs to service and returns 200", func(t *testing.T) {
		t.Parallel()

		var gotMessage string
		var gotIDs []string
		a := newSyncAPIForTest(&mockSyncService{
			publishAllFn: func(_ context.Context, message string, itemIDs []string) (*gitsync.SyncResult, error) {
				gotMessage = message
				gotIDs = itemIDs
				return &gitsync.SyncResult{
					Success:   true,
					Synced:    []string{"a"},
					Timestamp: time.Now(),
				}, nil
			},
		})

		resp, err := a.SyncPublishAll(context.Background(), apigen.SyncPublishAllRequestObject{
			Body: &apigen.SyncPublishAllRequest{
				Message: ptrOf("publish selected"),
				ItemIds: ptrOf([]string{"b", "a"}),
			},
		})
		require.NoError(t, err)

		_, ok := resp.(apigen.SyncPublishAll200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, "publish selected", gotMessage)
		assert.Equal(t, []string{"a", "b"}, gotIDs)
	})
}

func TestForgetSyncItem(t *testing.T) {
	t.Parallel()

	t.Run("returns 404 when item not found", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			forgetFn: func(_ context.Context, _ []string) ([]string, error) {
				return nil, &gitsync.DAGNotFoundError{DAGID: "missing-dag"}
			},
		})

		resp, err := a.ForgetSyncItem(context.Background(), apigen.ForgetSyncItemRequestObject{
			ItemId: "missing-dag",
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.ForgetSyncItem404JSONResponse)
		assert.True(t, ok)
	})

	t.Run("returns 400 when item cannot be forgotten", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			forgetFn: func(_ context.Context, _ []string) ([]string, error) {
				return nil, gitsync.ErrCannotForget
			},
		})

		resp, err := a.ForgetSyncItem(context.Background(), apigen.ForgetSyncItemRequestObject{
			ItemId: "synced-dag",
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.ForgetSyncItem400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "cannot be forgotten")
	})

	t.Run("returns 200 on success", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			forgetFn: func(_ context.Context, itemIDs []string) ([]string, error) {
				return itemIDs, nil
			},
		})

		resp, err := a.ForgetSyncItem(context.Background(), apigen.ForgetSyncItemRequestObject{
			ItemId: "my-dag",
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.ForgetSyncItem200JSONResponse)
		assert.True(t, ok)
	})
}

func TestSyncCleanup(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 with forgotten list", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			cleanupFn: func(_ context.Context) ([]string, error) {
				return []string{"dag-a", "dag-b"}, nil
			},
		})

		resp, err := a.SyncCleanup(context.Background(), apigen.SyncCleanupRequestObject{})
		require.NoError(t, err)
		r, ok := resp.(apigen.SyncCleanup200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, []string{"dag-a", "dag-b"}, r.Forgotten)
		assert.Contains(t, r.Message, "2")
	})
}

func TestDeleteSyncItem(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 when push disabled", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteFn: func(_ context.Context, _, _ string, _ bool) error {
				return gitsync.ErrPushDisabled
			},
		})

		resp, err := a.DeleteSyncItem(context.Background(), apigen.DeleteSyncItemRequestObject{
			ItemId: "my-dag",
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.DeleteSyncItem400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "push")
	})

	t.Run("returns 400 when item is untracked", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteFn: func(_ context.Context, _, _ string, _ bool) error {
				return gitsync.ErrCannotDeleteUntracked
			},
		})

		resp, err := a.DeleteSyncItem(context.Background(), apigen.DeleteSyncItemRequestObject{
			ItemId: "my-dag",
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.DeleteSyncItem400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "untracked")
	})

	t.Run("returns 404 when item not found", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteFn: func(_ context.Context, _, _ string, _ bool) error {
				return &gitsync.DAGNotFoundError{DAGID: "missing"}
			},
		})

		resp, err := a.DeleteSyncItem(context.Background(), apigen.DeleteSyncItemRequestObject{
			ItemId: "missing",
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.DeleteSyncItem404JSONResponse)
		assert.True(t, ok)
	})

	t.Run("returns 200 on success", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteFn: func(_ context.Context, _, _ string, _ bool) error {
				return nil
			},
		})

		resp, err := a.DeleteSyncItem(context.Background(), apigen.DeleteSyncItemRequestObject{
			ItemId: "my-dag",
			Body: &apigen.DeleteSyncItemJSONRequestBody{
				Message: ptrOf("remove old dag"),
				Force:   ptrOf(true),
			},
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.DeleteSyncItem200JSONResponse)
		assert.True(t, ok)
	})
}

func TestSyncDeleteMissing(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 when push disabled", func(t *testing.T) {
		t.Parallel()

		var called bool
		a := newSyncAPIForTest(&mockSyncService{
			deleteAllMissing: func(_ context.Context, _ string) ([]string, error) {
				called = true
				return nil, gitsync.ErrPushDisabled
			},
		})

		resp, err := a.SyncDeleteMissing(context.Background(), apigen.SyncDeleteMissingRequestObject{})
		require.NoError(t, err)
		assert.True(t, called, "deleteAllMissing should have been invoked")
		errResp, ok := resp.(apigen.SyncDeleteMissing400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "push")
	})

	t.Run("returns 200 with deleted list", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteAllMissing: func(_ context.Context, _ string) ([]string, error) {
				return []string{"dag-x", "dag-y"}, nil
			},
		})

		resp, err := a.SyncDeleteMissing(context.Background(), apigen.SyncDeleteMissingRequestObject{
			Body: &apigen.SyncDeleteMissingJSONRequestBody{
				Message: ptrOf("clean up"),
			},
		})
		require.NoError(t, err)
		r, ok := resp.(apigen.SyncDeleteMissing200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, []string{"dag-x", "dag-y"}, r.Deleted)
		assert.Contains(t, r.Message, "2")
	})
}

func TestSyncDeleteBatch(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 for nil body", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{})
		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.SyncDeleteBatch400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "itemIds")
	})

	t.Run("returns 400 for empty itemIds", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{})
		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{
			Body: &apigen.SyncDeleteBatchJSONRequestBody{
				ItemIds: []string{},
			},
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.SyncDeleteBatch400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "itemIds")
	})

	t.Run("returns 400 when push disabled", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteBatchFn: func(_ context.Context, _ []string, _ string, _ bool) ([]string, error) {
				return nil, gitsync.ErrPushDisabled
			},
		})

		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{
			Body: &apigen.SyncDeleteBatchJSONRequestBody{
				ItemIds: []string{"dag-a"},
			},
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.SyncDeleteBatch400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "push")
	})

	t.Run("returns 400 when item is untracked", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteBatchFn: func(_ context.Context, _ []string, _ string, _ bool) ([]string, error) {
				return nil, gitsync.ErrCannotDeleteUntracked
			},
		})

		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{
			Body: &apigen.SyncDeleteBatchJSONRequestBody{
				ItemIds: []string{"untracked-dag"},
			},
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.SyncDeleteBatch400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "untracked")
	})

	t.Run("returns 404 when item not found", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteBatchFn: func(_ context.Context, _ []string, _ string, _ bool) ([]string, error) {
				return nil, &gitsync.DAGNotFoundError{DAGID: "missing"}
			},
		})

		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{
			Body: &apigen.SyncDeleteBatchJSONRequestBody{
				ItemIds: []string{"missing"},
			},
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.SyncDeleteBatch404JSONResponse)
		assert.True(t, ok)
	})

	t.Run("returns 400 for validation error", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			deleteBatchFn: func(_ context.Context, _ []string, _ string, _ bool) ([]string, error) {
				return nil, &gitsync.ValidationError{
					Field:   "modified-dag",
					Message: "item has local modifications — use force to delete anyway",
				}
			},
		})

		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{
			Body: &apigen.SyncDeleteBatchJSONRequestBody{
				ItemIds: []string{"modified-dag"},
			},
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.SyncDeleteBatch400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "local modifications")
	})

	t.Run("returns 200 on success", func(t *testing.T) {
		t.Parallel()

		var gotIDs []string
		var gotMsg string
		var gotForce bool
		a := newSyncAPIForTest(&mockSyncService{
			deleteBatchFn: func(_ context.Context, itemIDs []string, message string, force bool) ([]string, error) {
				gotIDs = itemIDs
				gotMsg = message
				gotForce = force
				return []string{"dag-a", "dag-b"}, nil
			},
		})

		resp, err := a.SyncDeleteBatch(context.Background(), apigen.SyncDeleteBatchRequestObject{
			Body: &apigen.SyncDeleteBatchJSONRequestBody{
				ItemIds: []string{"dag-b", "dag-a"},
				Message: ptrOf("bulk delete"),
				Force:   ptrOf(true),
			},
		})
		require.NoError(t, err)
		r, ok := resp.(apigen.SyncDeleteBatch200JSONResponse)
		require.True(t, ok)
		assert.Equal(t, []string{"dag-a", "dag-b"}, r.Deleted)
		assert.Contains(t, r.Message, "2")
		assert.Equal(t, []string{"dag-b", "dag-a"}, gotIDs)
		assert.Equal(t, "bulk delete", gotMsg)
		assert.True(t, gotForce)
	})
}

func TestMoveSyncItem(t *testing.T) {
	t.Parallel()

	t.Run("returns 400 for nil request body", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{})
		_, err := a.MoveSyncItem(context.Background(), apigen.MoveSyncItemRequestObject{
			ItemId: "old-dag",
		})
		require.Error(t, err)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	})

	t.Run("returns 400 when push disabled", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			moveFn: func(_ context.Context, _, _, _ string, _ bool) error {
				return gitsync.ErrPushDisabled
			},
		})

		resp, err := a.MoveSyncItem(context.Background(), apigen.MoveSyncItemRequestObject{
			ItemId: "old-dag",
			Body: &apigen.MoveSyncItemJSONRequestBody{
				NewItemId: "new-dag",
			},
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.MoveSyncItem400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "push")
	})

	t.Run("returns 400 for validation error", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			moveFn: func(_ context.Context, _, _, _ string, _ bool) error {
				return &gitsync.ValidationError{
					Field:   "newItemId",
					Message: "cannot move across kinds",
				}
			},
		})

		resp, err := a.MoveSyncItem(context.Background(), apigen.MoveSyncItemRequestObject{
			ItemId: "old-dag",
			Body: &apigen.MoveSyncItemJSONRequestBody{
				NewItemId: "memory/new",
			},
		})
		require.NoError(t, err)
		errResp, ok := resp.(apigen.MoveSyncItem400JSONResponse)
		assert.True(t, ok)
		assert.Contains(t, errResp.Message, "cannot move across kinds")
	})

	t.Run("returns 404 when source not found", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			moveFn: func(_ context.Context, _, _, _ string, _ bool) error {
				return &gitsync.DAGNotFoundError{DAGID: "missing"}
			},
		})

		resp, err := a.MoveSyncItem(context.Background(), apigen.MoveSyncItemRequestObject{
			ItemId: "missing",
			Body: &apigen.MoveSyncItemJSONRequestBody{
				NewItemId: "new-dag",
			},
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.MoveSyncItem404JSONResponse)
		assert.True(t, ok)
	})

	t.Run("returns 409 for conflict error", func(t *testing.T) {
		t.Parallel()

		a := newSyncAPIForTest(&mockSyncService{
			moveFn: func(_ context.Context, _, _, _ string, _ bool) error {
				return &gitsync.ConflictError{
					DAGID:         "old-dag",
					RemoteCommit:  "abc123",
					RemoteAuthor:  "alice",
					RemoteMessage: "conflicting change",
				}
			},
		})

		resp, err := a.MoveSyncItem(context.Background(), apigen.MoveSyncItemRequestObject{
			ItemId: "old-dag",
			Body: &apigen.MoveSyncItemJSONRequestBody{
				NewItemId: "new-dag",
			},
		})
		require.NoError(t, err)
		conflictResp, ok := resp.(apigen.MoveSyncItem409JSONResponse)
		assert.True(t, ok)
		assert.Equal(t, "old-dag", conflictResp.ItemId)
	})

	t.Run("returns 200 on success", func(t *testing.T) {
		t.Parallel()

		var gotOld, gotNew, gotMsg string
		var gotForce bool
		a := newSyncAPIForTest(&mockSyncService{
			moveFn: func(_ context.Context, oldID, newID, message string, force bool) error {
				gotOld = oldID
				gotNew = newID
				gotMsg = message
				gotForce = force
				return nil
			},
		})

		resp, err := a.MoveSyncItem(context.Background(), apigen.MoveSyncItemRequestObject{
			ItemId: "old-dag",
			Body: &apigen.MoveSyncItemJSONRequestBody{
				NewItemId: "new-dag",
				Message:   ptrOf("rename workflow"),
				Force:     ptrOf(true),
			},
		})
		require.NoError(t, err)
		_, ok := resp.(apigen.MoveSyncItem200JSONResponse)
		assert.True(t, ok)
		assert.Equal(t, "old-dag", gotOld)
		assert.Equal(t, "new-dag", gotNew)
		assert.Equal(t, "rename workflow", gotMsg)
		assert.True(t, gotForce)
	})
}

func TestToAPISyncItems_IncludesKindAndPath(t *testing.T) {
	t.Parallel()

	now := time.Now()
	states := map[string]*gitsync.DAGState{
		"alpha": {
			Status:     gitsync.StatusModified,
			Kind:       gitsync.DAGKindDAG,
			ModifiedAt: &now,
		},
		"memory/MEMORY": {
			Status:     gitsync.StatusUntracked,
			ModifiedAt: &now, // No Kind set: should fallback by DAG ID
		},
		"base": {
			Status:     gitsync.StatusModified,
			Kind:       gitsync.DAGKindConfig,
			ModifiedAt: &now,
		},
	}

	apiItems := toAPISyncItems(states)
	require.Len(t, apiItems, 3)

	assert.Equal(t, "alpha", apiItems[0].ItemId)
	assert.Equal(t, apigen.SyncItemKindDag, apiItems[0].Kind)
	assert.Equal(t, "alpha.yaml", apiItems[0].FilePath)
	assert.Equal(t, "alpha.yaml", apiItems[0].DisplayName)

	assert.Equal(t, "base", apiItems[1].ItemId)
	assert.Equal(t, apigen.SyncItemKindConfig, apiItems[1].Kind)
	assert.Equal(t, "base.yaml", apiItems[1].FilePath)
	assert.Equal(t, "base.yaml", apiItems[1].DisplayName)

	assert.Equal(t, "memory/MEMORY", apiItems[2].ItemId)
	assert.Equal(t, apigen.SyncItemKindMemory, apiItems[2].Kind)
	assert.Equal(t, "memory/MEMORY.md", apiItems[2].FilePath)
	assert.Equal(t, "memory/MEMORY.md", apiItems[2].DisplayName)
}
