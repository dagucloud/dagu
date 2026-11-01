// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPublicWebhook(t *testing.T) *Service {
	t.Helper()
	service, _ := setupWebhookTestServiceWithEncryptedStore(t)
	_, err := service.CreateWebhook(context.Background(), "ci", "admin")
	require.NoError(t, err)
	_, err = service.EnablePublicWebhook(context.Background(), "ci")
	require.NoError(t, err)
	return service
}

func TestService_PublicWebhookURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := setupPublicWebhook(t)

	first, err := service.GetWebhookByDAGName(ctx, "ci")
	require.NoError(t, err)
	require.NotNil(t, first.Public)
	assert.Len(t, first.Public.Token, 43, "32 random bytes, base64url")

	again, err := service.EnablePublicWebhook(ctx, "ci")
	require.NoError(t, err)
	assert.Equal(t, first.Public.Token, again.Public.Token, "turning it on again keeps the URL")

	renewed, err := service.RegeneratePublicWebhookToken(ctx, "ci")
	require.NoError(t, err)
	assert.NotEqual(t, first.Public.Token, renewed.Public.Token)
	assert.Equal(t, first.Public.CreatedAt, renewed.Public.CreatedAt, "a new URL keeps its place under the plan's limit")

	off, err := service.DisablePublicWebhook(ctx, "ci")
	require.NoError(t, err)
	assert.Nil(t, off.Public)
	_, err = service.RegeneratePublicWebhookToken(ctx, "ci")
	require.ErrorIs(t, err, ErrWebhookPublicNotEnabled)
}

func TestService_PublicWebhookNeedsEncryption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _ := setupWebhookTestService(t)
	_, err := service.CreateWebhook(ctx, "ci", "admin")
	require.NoError(t, err)

	_, err = service.EnablePublicWebhook(ctx, "ci")
	require.ErrorIs(t, err, ErrWebhookPublicNotSupported)
}

func TestService_ConfigurePublicWebhookSender(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("GitHub gets a secret made for it, kept until replaced", func(t *testing.T) {
		t.Parallel()
		service := setupPublicWebhook(t)

		made, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{Sender: "github"})
		require.NoError(t, err)
		require.Len(t, made.GeneratedSecret, 64)
		assert.Equal(t, made.GeneratedSecret, made.Webhook.Public.SenderSecret)

		kept, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{
			Sender: "github",
			Events: []string{" push ", "issues", "push"},
		})
		require.NoError(t, err)
		assert.Empty(t, kept.GeneratedSecret)
		assert.Equal(t, made.GeneratedSecret, kept.Webhook.Public.SenderSecret)
		assert.Equal(t, []string{"push", "issues"}, kept.Webhook.Public.Events)

		renewed, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{Sender: "github", GenerateSecret: true})
		require.NoError(t, err)
		assert.NotEqual(t, made.GeneratedSecret, renewed.GeneratedSecret)
		assert.Equal(t, renewed.GeneratedSecret, renewed.Webhook.Public.SenderSecret)
	})

	t.Run("a sender that makes its own secret needs it entered", func(t *testing.T) {
		t.Parallel()
		service := setupPublicWebhook(t)

		_, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{Sender: "stripe"})
		require.ErrorIs(t, err, ErrInvalidPublicWebhookSettings)

		set, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{Sender: "stripe", Secret: " whsec_abc\n"})
		require.NoError(t, err)
		assert.Empty(t, set.GeneratedSecret)
		assert.Equal(t, "whsec_abc", set.Webhook.Public.SenderSecret)

		_, err = service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{Sender: "slack"})
		require.ErrorIs(t, err, ErrInvalidPublicWebhookSettings, "another sender's secret is not kept")
	})

	t.Run("anyone with the URL clears the secret", func(t *testing.T) {
		t.Parallel()
		service := setupPublicWebhook(t)
		_, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{Sender: "github"})
		require.NoError(t, err)

		open, err := service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{})
		require.NoError(t, err)
		assert.Equal(t, auth.WebhookPublic{
			Token:     open.Webhook.Public.Token,
			CreatedAt: open.Webhook.Public.CreatedAt,
		}, *open.Webhook.Public)
	})

	t.Run("invalid settings", func(t *testing.T) {
		t.Parallel()
		service := setupPublicWebhook(t)
		for name, input := range map[string]PublicWebhookSenderInput{
			"unknown sender":            {Sender: "gitlab"},
			"secret without a sender":   {Secret: "s"},
			"generated non-GitHub":      {Sender: "slack", GenerateSecret: true},
			"generated and entered":     {Sender: "github", Secret: "s", GenerateSecret: true},
			"malformed Standard secret": {Sender: "standard", Secret: "whsec_!"},
		} {
			_, err := service.ConfigurePublicWebhookSender(ctx, "ci", input)
			assert.ErrorIs(t, err, ErrInvalidPublicWebhookSettings, name)
		}
	})

	t.Run("no public URL", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		_, err := service.CreateWebhook(ctx, "ci", "admin")
		require.NoError(t, err)
		_, err = service.ConfigurePublicWebhookSender(ctx, "ci", PublicWebhookSenderInput{})
		require.ErrorIs(t, err, ErrWebhookPublicNotEnabled)
	})
}
