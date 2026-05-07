// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build postgres_integration

package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

func TestPostgresControlPlaneStoreIntegration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	container, err := tcpostgres.Run(
		ctx,
		"postgres:18",
		tcpostgres.WithDatabase("dagu"),
		tcpostgres.WithUsername("dagu"),
		tcpostgres.WithPassword("dagu"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testcontainers.TerminateContainer(container))
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	encryptor, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)

	store, err := New(ctx, Config{
		DSN:              dsn,
		LocalWorkDirBase: t.TempDir(),
		AutoMigrate:      true,
		WebhookEncryptor: encryptor,
		Pool: PoolConfig{
			MaxOpenConns: 4,
			MaxIdleConns: 1,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	t.Run("DAGRuns", func(t *testing.T) {
		dag := &core.DAG{
			Name:   "example",
			Labels: core.NewLabels([]string{"workspace=ops"}),
		}
		attempt, err := store.DAGRuns().CreateAttempt(ctx, dag, time.Now().UTC(), "run-1", exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))

		status := exec.InitialStatus(dag)
		status.DAGRunID = "run-1"
		status.Status = core.Queued
		status.AttemptID = attempt.ID()
		require.NoError(t, attempt.Write(ctx, status))
		require.NoError(t, attempt.WriteOutputs(ctx, &exec.DAGRunOutputs{
			Metadata: exec.OutputsMetadata{
				DAGName:   "example",
				DAGRunID:  "run-1",
				AttemptID: attempt.ID(),
				Status:    status.Status.String(),
			},
			Outputs: map[string]string{"result": "ok"},
		}))
		require.NoError(t, attempt.WriteStepMessages(ctx, "step-1", []exec.LLMMessage{
			{Role: exec.RoleUser, Content: "hello"},
		}))

		found, err := store.DAGRuns().FindAttempt(ctx, exec.NewDAGRunRef("example", "run-1"))
		require.NoError(t, err)
		readStatus, err := found.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Queued, readStatus.Status)
		assert.Contains(t, readStatus.Labels, "workspace=ops")

		var (
			runVersion     int
			runData        []byte
			projectedName  string
			projectedRunID string
			projectedState string
		)
		require.NoError(t, store.pool.QueryRow(ctx, `
SELECT data_version, data, data #>> '{status,name}', data #>> '{status,dagRunId}', data #>> '{status,status}'
FROM dagu_dag_runs
WHERE is_root AND dag_name = 'example' AND dag_run_id = 'run-1'
`).Scan(&runVersion, &runData, &projectedName, &projectedRunID, &projectedState))
		assert.Equal(t, 1, runVersion)
		assert.Equal(t, "example", projectedName)
		assert.Equal(t, "run-1", projectedRunID)
		assert.Equal(t, strconv.Itoa(int(core.Queued)), projectedState)
		var runDoc struct {
			Status exec.DAGRunStatus `json:"status"`
		}
		require.NoError(t, json.Unmarshal(runData, &runDoc))
		assert.Equal(t, readStatus.Status, runDoc.Status.Status)
		assert.Equal(t, readStatus.DAGRunID, runDoc.Status.DAGRunID)

		var (
			attemptVersion int
			attemptData    []byte
		)
		require.NoError(t, store.pool.QueryRow(ctx, `
SELECT data_version, data
FROM dagu_dag_run_attempts
WHERE dag_name = 'example' AND dag_run_id = 'run-1' AND attempt_id = $1
`, attempt.ID()).Scan(&attemptVersion, &attemptData))
		assert.Equal(t, 1, attemptVersion)
		var attemptDoc struct {
			Status   exec.DAGRunStatus            `json:"status"`
			DAG      core.DAG                     `json:"dag"`
			Outputs  exec.DAGRunOutputs           `json:"outputs"`
			Messages map[string][]exec.LLMMessage `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(attemptData, &attemptDoc))
		assert.Equal(t, "example", attemptDoc.DAG.Name)
		assert.Equal(t, "ok", attemptDoc.Outputs.Outputs["result"])
		require.Len(t, attemptDoc.Messages["step-1"], 1)
		assert.Equal(t, "hello", attemptDoc.Messages["step-1"][0].Content)
	})

	t.Run("QueueLease", func(t *testing.T) {
		runRef := exec.NewDAGRunRef("example", "run-1")
		require.NoError(t, store.Queue().Enqueue(ctx, "default", exec.QueuePriorityHigh, runRef))

		items, err := store.Queue().List(ctx, "default")
		require.NoError(t, err)
		require.Len(t, items, 1)
		var queueData []byte
		var queueDataVersion int
		require.NoError(t, store.pool.QueryRow(ctx, `
SELECT data_version, data
FROM dagu_queue_items
WHERE queue_name = 'default' AND dag_name = 'example' AND dag_run_id = 'run-1'
`).Scan(&queueDataVersion, &queueData))
		assert.Equal(t, 1, queueDataVersion)
		var queued struct {
			DAGRun exec.DAGRunRef `json:"dagRun"`
		}
		require.NoError(t, json.Unmarshal(queueData, &queued))
		assert.Equal(t, runRef, queued.DAGRun)

		leased, err := store.ClaimByItemID(ctx, "default", items[0].ID(), "integration-test", time.Minute)
		require.NoError(t, err)
		assert.NotEmpty(t, leased.LeaseToken())
		require.NoError(t, store.AckLease(ctx, "default", leased.LeaseToken()))

		count, err := store.Queue().Len(ctx, "default")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("AuthWorkspaceAuditEventsSessions", func(t *testing.T) {
		ws := workspace.NewWorkspace("ops", "Operations")
		require.NoError(t, store.Workspaces().Create(ctx, ws))
		gotWorkspace, err := store.Workspaces().GetByName(ctx, "ops")
		require.NoError(t, err)
		assert.Equal(t, ws.ID, gotWorkspace.ID)
		ws.Description = "Ops Updated"
		require.NoError(t, store.Workspaces().Update(ctx, ws))
		updatedWorkspace, err := store.Workspaces().GetByID(ctx, ws.ID)
		require.NoError(t, err)
		assert.Equal(t, "Ops Updated", updatedWorkspace.Description)

		user := auth.NewUser("admin", "hash", auth.RoleAdmin)
		require.NoError(t, store.Users().Create(ctx, user))
		gotUser, err := store.Users().GetByUsername(ctx, "admin")
		require.NoError(t, err)
		assert.Equal(t, user.ID, gotUser.ID)
		user.IsDisabled = true
		require.NoError(t, store.Users().Update(ctx, user))
		updatedUser, err := store.Users().GetByID(ctx, user.ID)
		require.NoError(t, err)
		assert.True(t, updatedUser.IsDisabled)

		apiKey, err := auth.NewAPIKey("ci", "integration", auth.RoleAdmin, "hash", "dagu_123", user.ID)
		require.NoError(t, err)
		require.NoError(t, store.APIKeys().Create(ctx, apiKey))
		require.NoError(t, store.APIKeys().UpdateLastUsed(ctx, apiKey.ID))
		apiKey.Name = "ci-updated"
		require.NoError(t, store.APIKeys().Update(ctx, apiKey))
		updatedAPIKey, err := store.APIKeys().GetByID(ctx, apiKey.ID)
		require.NoError(t, err)
		assert.Equal(t, "ci-updated", updatedAPIKey.Name)

		webhook, err := auth.NewWebhook("example", "hash", "dagu_wh_123", user.ID)
		require.NoError(t, err)
		webhook.HMACSecret = "secret"
		now := time.Now().UTC()
		webhook.HMACSecretGeneratedAt = &now
		require.NoError(t, store.Webhooks().Create(ctx, webhook))
		gotWebhook, err := store.Webhooks().GetByDAGName(ctx, "example")
		require.NoError(t, err)
		assert.Equal(t, "secret", gotWebhook.HMACSecret)
		gotWebhook.Enabled = false
		require.NoError(t, store.Webhooks().Update(ctx, gotWebhook))
		updatedWebhook, err := store.Webhooks().GetByID(ctx, gotWebhook.ID)
		require.NoError(t, err)
		assert.False(t, updatedWebhook.Enabled)

		entry := audit.NewEntry(audit.CategorySystem, "integration_test", user.ID, user.Username)
		require.NoError(t, store.Audit().Append(ctx, entry))
		auditResult, err := store.Audit().Query(ctx, audit.QueryFilter{Category: audit.CategorySystem, Limit: 10})
		require.NoError(t, err)
		require.Len(t, auditResult.Entries, 1)
		assert.Equal(t, entry.ID, auditResult.Entries[0].ID)

		eventSvc := eventstore.New(store.Events())
		event := &eventstore.Event{
			ID:            "evt_" + uuid.Must(uuid.NewV7()).String(),
			OccurredAt:    time.Now().UTC(),
			Kind:          eventstore.KindLLMUsage,
			Type:          eventstore.TypeLLMUsageRecorded,
			SourceService: eventstore.SourceServiceServer,
			UserID:        user.ID,
			Model:         "test-model",
		}
		require.NoError(t, eventSvc.Emit(ctx, event))
		eventResult, err := eventSvc.Query(ctx, eventstore.QueryFilter{Kind: eventstore.KindLLMUsage, Limit: 10})
		require.NoError(t, err)
		require.Len(t, eventResult.Entries, 1)
		assert.Equal(t, event.ID, eventResult.Entries[0].ID)
		for i := 0; i < 3; i++ {
			cursorEvent := &eventstore.Event{
				ID:            "evt_" + uuid.Must(uuid.NewV7()).String(),
				OccurredAt:    time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
				Kind:          eventstore.KindDAGRun,
				Type:          eventstore.TypeDAGRunUpdated,
				SourceService: eventstore.SourceServiceServer,
				UserID:        user.ID,
				Status:        "blocked",
			}
			require.NoError(t, eventSvc.Emit(ctx, cursorEvent))
		}
		firstPage, err := eventSvc.Query(ctx, eventstore.QueryFilter{
			Kind:           eventstore.KindDAGRun,
			PaginationMode: eventstore.QueryPaginationModeCursor,
			Limit:          2,
		})
		require.NoError(t, err)
		require.Len(t, firstPage.Entries, 2)
		require.NotEmpty(t, firstPage.NextCursor)
		secondPage, err := eventSvc.Query(ctx, eventstore.QueryFilter{
			Kind:           eventstore.KindDAGRun,
			PaginationMode: eventstore.QueryPaginationModeCursor,
			Cursor:         firstPage.NextCursor,
			Limit:          2,
		})
		require.NoError(t, err)
		require.Len(t, secondPage.Entries, 1)

		sess := &agent.Session{
			ID:        uuid.Must(uuid.NewV7()).String(),
			UserID:    user.ID,
			Title:     "Integration",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, store.Sessions().CreateSession(ctx, sess))
		msg := &agent.Message{
			ID:         uuid.Must(uuid.NewV7()).String(),
			SessionID:  sess.ID,
			Type:       agent.MessageTypeUser,
			SequenceID: 1,
			Content:    "hello",
			CreatedAt:  time.Now().UTC(),
		}
		require.NoError(t, store.Sessions().AddMessage(ctx, sess.ID, msg))
		messages, err := store.Sessions().GetMessages(ctx, sess.ID)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		assert.Equal(t, "hello", messages[0].Content)
		require.NoError(t, store.Sessions().DeleteSession(ctx, sess.ID))
		require.NoError(t, store.Webhooks().Delete(ctx, gotWebhook.ID))
		require.NoError(t, store.APIKeys().Delete(ctx, apiKey.ID))
		require.NoError(t, store.Users().Delete(ctx, user.ID))
		require.NoError(t, store.Workspaces().Delete(ctx, ws.ID))
	})

	t.Run("DistributedStores", func(t *testing.T) {
		attemptKey := exec.GenerateAttemptKey("example", "run-1", "example", "run-1", "attempt-1")
		owner := exec.CoordinatorEndpoint{ID: "coord-1", Host: "127.0.0.1", Port: 50055}
		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_START,
			RootDagRunName: "example",
			RootDagRunId:   "run-1",
			DagRunId:       "run-1",
			Target:         "example",
			QueueName:      "critical",
			AttemptId:      "attempt-1",
			AttemptKey:     attemptKey,
			WorkerSelector: map[string]string{"pool": "blue"},
		}
		dispatchStore := store.DispatchTasks()
		require.NoError(t, dispatchStore.Enqueue(ctx, task))
		outstanding, err := dispatchStore.CountOutstandingByQueue(ctx, "critical", time.Minute)
		require.NoError(t, err)
		assert.Equal(t, 1, outstanding)
		hasAttempt, err := dispatchStore.HasOutstandingAttempt(ctx, attemptKey, time.Minute)
		require.NoError(t, err)
		assert.True(t, hasAttempt)

		claimed, err := dispatchStore.ClaimNext(ctx, exec.DispatchTaskClaim{
			WorkerID:     "worker-1",
			PollerID:     "poller-1",
			Labels:       map[string]string{"pool": "blue"},
			Owner:        owner,
			ClaimTimeout: time.Minute,
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)
		assert.Equal(t, "worker-1", claimed.WorkerID)
		assert.Equal(t, owner.ID, claimed.Owner.ID)
		assert.Equal(t, owner.ID, claimed.Task.OwnerCoordinatorId)
		assert.NotEmpty(t, claimed.ClaimToken)

		gotClaim, err := dispatchStore.GetClaim(ctx, claimed.ClaimToken)
		require.NoError(t, err)
		assert.Equal(t, claimed.ClaimToken, gotClaim.ClaimToken)
		require.NoError(t, dispatchStore.DeleteClaim(ctx, claimed.ClaimToken))
		_, err = dispatchStore.GetClaim(ctx, claimed.ClaimToken)
		assert.ErrorIs(t, err, exec.ErrDispatchTaskNotFound)

		heartbeatStore := store.WorkerHeartbeats()
		require.NoError(t, heartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			Labels:          map[string]string{"pool": "blue"},
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))
		heartbeat, err := heartbeatStore.Get(ctx, "worker-1")
		require.NoError(t, err)
		assert.Equal(t, "blue", heartbeat.Labels["pool"])
		heartbeats, err := heartbeatStore.List(ctx)
		require.NoError(t, err)
		require.Len(t, heartbeats, 1)
		deletedStale, err := heartbeatStore.DeleteStale(ctx, time.Now().UTC().Add(time.Hour))
		require.NoError(t, err)
		assert.Equal(t, 1, deletedStale)

		leaseStore := store.DAGRunLeases()
		lease := exec.DAGRunLease{
			AttemptKey: attemptKey,
			DAGRun:     exec.NewDAGRunRef("example", "run-1"),
			Root:       exec.NewDAGRunRef("example", "run-1"),
			AttemptID:  "attempt-1",
			QueueName:  "critical",
			WorkerID:   "worker-1",
			Owner:      owner,
		}
		require.NoError(t, leaseStore.Upsert(ctx, lease))
		require.NoError(t, leaseStore.Touch(ctx, attemptKey, time.Now().UTC().Add(time.Second)))
		gotLease, err := leaseStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, "worker-1", gotLease.WorkerID)
		leasesByQueue, err := leaseStore.ListByQueue(ctx, "critical")
		require.NoError(t, err)
		require.Len(t, leasesByQueue, 1)
		allLeases, err := leaseStore.ListAll(ctx)
		require.NoError(t, err)
		require.Len(t, allLeases, 1)
		require.NoError(t, leaseStore.Delete(ctx, attemptKey))

		activeStore := store.ActiveDistributedRuns()
		active := exec.ActiveDistributedRun{
			AttemptKey: attemptKey,
			DAGRun:     exec.NewDAGRunRef("example", "run-1"),
			Root:       exec.NewDAGRunRef("example", "run-1"),
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     core.Running,
		}
		require.NoError(t, activeStore.Upsert(ctx, active))
		gotActive, err := activeStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, core.Running, gotActive.Status)
		allActive, err := activeStore.ListAll(ctx)
		require.NoError(t, err)
		require.Len(t, allActive, 1)
		require.NoError(t, activeStore.Delete(ctx, attemptKey))
	})

	t.Run("ServiceRegistry", func(t *testing.T) {
		registry := store.Services()
		require.NoError(t, registry.Register(ctx, exec.ServiceNameCoordinator, exec.HostInfo{
			ID:     "coord-integration",
			Host:   "127.0.0.1",
			Port:   50055,
			Status: exec.ServiceStatusActive,
		}))
		members, err := registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
		require.NoError(t, err)
		require.Len(t, members, 1)
		assert.Equal(t, "coord-integration", members[0].ID)
		require.NoError(t, registry.UpdateStatus(ctx, exec.ServiceNameCoordinator, exec.ServiceStatusInactive))
		registry.Unregister(ctx)
		members, err = registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
		require.NoError(t, err)
		assert.Empty(t, members)
	})
}
