// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build postgres_integration

package postgres

import (
	"context"
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

		found, err := store.DAGRuns().FindAttempt(ctx, exec.NewDAGRunRef("example", "run-1"))
		require.NoError(t, err)
		readStatus, err := found.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Queued, readStatus.Status)
		assert.Contains(t, readStatus.Labels, "workspace=ops")
	})

	t.Run("QueueLease", func(t *testing.T) {
		runRef := exec.NewDAGRunRef("example", "run-1")
		require.NoError(t, store.Queue().Enqueue(ctx, "default", exec.QueuePriorityHigh, runRef))

		items, err := store.Queue().List(ctx, "default")
		require.NoError(t, err)
		require.Len(t, items, 1)

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

		user := auth.NewUser("admin", "hash", auth.RoleAdmin)
		require.NoError(t, store.Users().Create(ctx, user))
		gotUser, err := store.Users().GetByUsername(ctx, "admin")
		require.NoError(t, err)
		assert.Equal(t, user.ID, gotUser.ID)

		apiKey, err := auth.NewAPIKey("ci", "integration", auth.RoleAdmin, "hash", "dagu_123", user.ID)
		require.NoError(t, err)
		require.NoError(t, store.APIKeys().Create(ctx, apiKey))
		require.NoError(t, store.APIKeys().UpdateLastUsed(ctx, apiKey.ID))

		webhook, err := auth.NewWebhook("example", "hash", "dagu_wh_123", user.ID)
		require.NoError(t, err)
		webhook.HMACSecret = "secret"
		now := time.Now().UTC()
		webhook.HMACSecretGeneratedAt = &now
		require.NoError(t, store.Webhooks().Create(ctx, webhook))
		gotWebhook, err := store.Webhooks().GetByDAGName(ctx, "example")
		require.NoError(t, err)
		assert.Equal(t, "secret", gotWebhook.HMACSecret)

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
	})
}
