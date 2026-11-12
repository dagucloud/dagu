// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const relayURL = "https://hooks.example/server-object"

// publicWebhookFixture is a server connected to Dagu Cloud, an admin token,
// and the store the scheduler that holds the relay connection writes.
type publicWebhookFixture struct {
	server test.Server
	token  string
	relay  *store.PublicWebhookStore
}

func setupPublicWebhooks(t *testing.T) publicWebhookFixture {
	t.Helper()
	server := setupWebhookTestServerWithLicense(t, license.NewConnectedTestManager(license.FeatureRBAC, license.FeatureAudit))
	backend := persisfile.NewBackend(server.Config.Paths)
	return publicWebhookFixture{
		server: server,
		token:  getWebhookAdminToken(t, server),
		relay:  store.NewPublicWebhookStore(backend.Collection(persis.CollectionPublicWebhooks)),
	}
}

// webhook creates a DAG with a webhook.
func (f publicWebhookFixture) webhook(t *testing.T, dagName string) {
	t.Helper()
	createTestDAG(t, f.server, f.token, dagName)
	f.server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(f.token).ExpectStatus(http.StatusCreated).Send(t)
}

func (f publicWebhookFixture) enable(t *testing.T, dagName string, status int) api.WebhookDetails {
	t.Helper()
	var details api.WebhookDetails
	f.server.Client().Post("/api/v1/dags/"+dagName+"/webhook/public", nil).
		WithBearerToken(f.token).ExpectStatus(status).Send(t).Unmarshal(t, &details)
	return details
}

func (f publicWebhookFixture) public(t *testing.T, dagName string) api.WebhookPublicDetails {
	t.Helper()
	var details api.WebhookPublicDetails
	f.server.Client().Get("/api/v1/dags/"+dagName+"/webhook/public").
		WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	return details
}

func (f publicWebhookFixture) webhookID(t *testing.T, dagName string) string {
	t.Helper()
	var details api.WebhookDetails
	f.server.Client().Get("/api/v1/dags/"+dagName+"/webhook").
		WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	return details.Id.String()
}

func TestWebhooksPublicURL(t *testing.T) {
	webhookParallel(t)
	f := setupPublicWebhooks(t)
	ctx := context.Background()
	dagName := "public_url_test"
	f.webhook(t, dagName)
	f.server.Client().Get("/api/v1/dags/" + dagName + "/webhook/public").
		WithBearerToken(f.token).ExpectStatus(http.StatusNotFound).Send(t)

	enabled := f.enable(t, dagName, http.StatusOK)
	require.NotNil(t, enabled.Public)
	assert.Nil(t, enabled.Public.Sender, "anyone with the URL may send")
	assert.False(t, enabled.Public.SecretConfigured)
	assert.Empty(t, enabled.Public.Events)

	connecting := f.public(t, dagName)
	assert.Equal(t, api.WebhookPublicStateConnecting, connecting.State)
	assert.Nil(t, connecting.Url, "no URL before Dagu Cloud gives the server its address")

	require.NoError(t, f.relay.SetStatus(ctx, publicwebhook.Status{Connected: true, URL: relayURL, MaxHooks: 1000}))
	webhookID := f.webhookID(t, dagName)
	received := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)
	require.NoError(t, f.relay.AddDelivery(ctx, webhookID, publicwebhook.Delivery{
		ID: "delivery-1", Received: received, Size: 42, Event: "push", Outcome: publicwebhook.OutcomeStarted, RunID: "delivery-1",
	}))
	require.NoError(t, f.relay.AddMissed(ctx, webhookID, 2, 1, received))

	active := f.public(t, dagName)
	assert.Equal(t, api.WebhookPublicStateActive, active.State)
	require.NotNil(t, active.Url)
	token, found := strings.CutPrefix(*active.Url, relayURL+"/")
	require.True(t, found, *active.Url)
	assert.NotEmpty(t, token)
	assert.Equal(t, 1000, *active.MaxHooks)
	assert.Equal(t, []api.WebhookPublicDelivery{{
		Id: "delivery-1", ReceivedAt: received, Size: 42, Event: new("push"),
		Outcome: api.WebhookPublicDeliveryOutcomeStarted, DagRunId: new("delivery-1"),
	}}, active.Deliveries)
	assert.Equal(t, 2, active.Missed.Expired)
	assert.Equal(t, 1, active.Missed.Refused)

	f.server.Client().Delete("/api/v1/dags/" + dagName + "/webhook/public/missed").
		WithBearerToken(f.token).ExpectStatus(http.StatusNoContent).Send(t)
	assert.Equal(t, api.WebhookPublicMissed{}, f.public(t, dagName).Missed)

	f.server.Client().Post("/api/v1/dags/"+dagName+"/webhook/public/regenerate", nil).
		WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t)
	renewed := f.public(t, dagName)
	assert.NotEqual(t, *active.Url, *renewed.Url)
	assert.True(t, strings.HasPrefix(*renewed.Url, relayURL+"/"))

	var disabled api.WebhookDetails
	f.server.Client().Delete("/api/v1/dags/"+dagName+"/webhook/public").
		WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &disabled)
	assert.Nil(t, disabled.Public)
	f.server.Client().Get("/api/v1/dags/" + dagName + "/webhook/public").
		WithBearerToken(f.token).ExpectStatus(http.StatusNotFound).Send(t)

	f.enable(t, dagName, http.StatusOK)
	assert.Empty(t, f.public(t, dagName).Deliveries, "turning the URL off removed its deliveries")
}

func TestWebhooksPublicURLNeedsDaguCloud(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	f := publicWebhookFixture{server: server, token: getWebhookAdminToken(t, server)}
	f.webhook(t, "public_offline_test")

	f.enable(t, "public_offline_test", http.StatusBadRequest)
}

// A plan holds only so many public URLs: past its limit a new one is
// refused, and one Dagu Cloud leaves out shows as over the limit.
func TestWebhooksPublicURLPlanLimit(t *testing.T) {
	webhookParallel(t)
	f := setupPublicWebhooks(t)
	ctx := context.Background()
	f.webhook(t, "public_first")
	f.webhook(t, "public_second")
	f.webhook(t, "public_third")
	f.enable(t, "public_first", http.StatusOK)
	f.enable(t, "public_second", http.StatusOK)

	require.NoError(t, f.relay.SetStatus(ctx, publicwebhook.Status{
		Connected: true, URL: relayURL, MaxHooks: 1, LeftOut: []string{"public_second"},
	}))
	assert.Equal(t, api.WebhookPublicStateActive, f.public(t, "public_first").State)
	assert.Equal(t, api.WebhookPublicStateOverLimit, f.public(t, "public_second").State)

	f.enable(t, "public_third", http.StatusBadRequest)
	f.enable(t, "public_first", http.StatusOK)
}

func TestWebhooksPublicSender(t *testing.T) {
	webhookParallel(t)
	f := setupPublicWebhooks(t)
	dagName := "public_sender_test"
	f.webhook(t, dagName)
	f.enable(t, dagName, http.StatusOK)
	path := "/api/v1/dags/" + dagName + "/webhook/public/sender"

	var github api.WebhookPublicSenderResponse
	f.server.Client().Put(path, api.WebhookPublicSenderRequest{
		Sender: new(api.WebhookPublicSenderGithub),
		Events: &[]string{"push", "pull_request.opened"},
	}).WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &github)
	require.NotNil(t, github.Secret, "Dagu makes GitHub's secret")
	assert.Len(t, *github.Secret, 64)
	assert.Equal(t, &api.WebhookPublicSettings{
		Sender:           new(api.WebhookPublicSenderGithub),
		SecretConfigured: true,
		Events:           []string{"push", "pull_request.opened"},
		CreatedAt:        github.Webhook.Public.CreatedAt,
	}, github.Webhook.Public)

	f.server.Client().Put(path, api.WebhookPublicSenderRequest{Sender: new(api.WebhookPublicSenderStripe)}).
		WithBearerToken(f.token).ExpectStatus(http.StatusBadRequest).Send(t)
}

// Developers see a public URL; only admins change it.
func TestWebhooksPublicURLAdminOnly(t *testing.T) {
	webhookParallel(t)
	f := setupPublicWebhooks(t)
	dagName := "public_admin_test"
	f.webhook(t, dagName)
	f.enable(t, dagName, http.StatusOK)

	f.server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "developer-user",
		Password: "developer1",
		Role:     api.UserRoleDeveloper,
	}).WithBearerToken(f.token).ExpectStatus(http.StatusCreated).Send(t)
	var login api.LoginResponse
	f.server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "developer-user",
		Password: "developer1",
	}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &login)

	base := "/api/v1/dags/" + dagName + "/webhook/public"
	f.server.Client().Get(base).WithBearerToken(login.Token).ExpectStatus(http.StatusOK).Send(t)
	f.server.Client().Post(base+"/regenerate", nil).WithBearerToken(login.Token).ExpectStatus(http.StatusForbidden).Send(t)
	f.server.Client().Delete(base).WithBearerToken(login.Token).ExpectStatus(http.StatusForbidden).Send(t)
}

// Renaming a DAG keeps its webhook's public URL and deliveries; deleting the
// DAG removes them.
func TestWebhooksPublicURLFollowsTheDAG(t *testing.T) {
	webhookParallel(t)
	f := setupPublicWebhooks(t)
	ctx := context.Background()
	f.webhook(t, "public_rename_old")
	f.enable(t, "public_rename_old", http.StatusOK)
	require.NoError(t, f.relay.SetStatus(ctx, publicwebhook.Status{Connected: true, URL: relayURL}))
	webhookID := f.webhookID(t, "public_rename_old")
	require.NoError(t, f.relay.AddDelivery(ctx, webhookID, publicwebhook.Delivery{
		ID: "delivery-1", Received: time.Now().UTC(), Outcome: publicwebhook.OutcomeStarted,
	}))
	before := f.public(t, "public_rename_old")

	f.server.Client().Post("/api/v1/dags/public_rename_old/rename", api.RenameDAGJSONRequestBody{
		NewFileName: "public_rename_new",
	}).WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t)

	after := f.public(t, "public_rename_new")
	assert.Equal(t, before.Url, after.Url, "the URL keeps its token")
	assert.Len(t, after.Deliveries, 1)
	f.server.Client().Get("/api/v1/dags/public_rename_old/webhook").
		WithBearerToken(f.token).ExpectStatus(http.StatusNotFound).Send(t)

	f.server.Client().Delete("/api/v1/dags/public_rename_new").
		WithBearerToken(f.token).ExpectStatus(http.StatusNoContent).Send(t)
	log, err := f.relay.Log(ctx, webhookID)
	require.NoError(t, err)
	assert.Empty(t, log.Deliveries, "deleting the DAG removes its deliveries")
}

// A rename that cannot finish leaves the webhook with the DAG, and one onto a
// name whose webhook was left behind is refused before anything moves.
func TestWebhooksRenameKeepsWebhookWithTheDAG(t *testing.T) {
	webhookParallel(t)
	f := setupPublicWebhooks(t)
	f.webhook(t, "rename_source")
	createTestDAG(t, f.server, f.token, "rename_taken")

	f.server.Client().Post("/api/v1/dags/rename_source/rename", api.RenameDAGJSONRequestBody{
		NewFileName: "rename_taken",
	}).WithBearerToken(f.token).ExpectStatus(http.StatusConflict).Send(t)
	f.server.Client().Get("/api/v1/dags/rename_source/webhook").
		WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t)
	f.server.Client().Get("/api/v1/dags/rename_taken/webhook").
		WithBearerToken(f.token).ExpectStatus(http.StatusNotFound).Send(t)

	// A DAG file removed outside Dagu leaves its webhook behind.
	f.webhook(t, "rename_orphan")
	require.NoError(t, os.Remove(filepath.Join(f.server.Config.Paths.DAGsDir, "rename_orphan.yaml")))
	f.server.Client().Post("/api/v1/dags/rename_source/rename", api.RenameDAGJSONRequestBody{
		NewFileName: "rename_orphan",
	}).WithBearerToken(f.token).ExpectStatus(http.StatusConflict).Send(t)
	f.server.Client().Get("/api/v1/dags/rename_source/webhook").
		WithBearerToken(f.token).ExpectStatus(http.StatusOK).Send(t)

	// A DAG without a webhook does not take over the one left behind.
	createTestDAG(t, f.server, f.token, "rename_plain")
	f.server.Client().Post("/api/v1/dags/rename_plain/rename", api.RenameDAGJSONRequestBody{
		NewFileName: "rename_orphan",
	}).WithBearerToken(f.token).ExpectStatus(http.StatusConflict).Send(t)
}
