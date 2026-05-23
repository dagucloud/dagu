// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	cryptoutil "github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupWebhookTestService(t *testing.T) (*Service, string) {
	t.Helper()
	webhookStore, err := store.NewWebhookStore(testutil.NewMemoryBackend().Collection("webhooks"), nil)
	require.NoError(t, err)
	service := New(nil, Config{
		TokenSecret: mustTokenSecret("test"),
		TokenTTL:    time.Hour,
		BcryptCost:  4, // Low cost for fast tests
	}, WithWebhookStore(webhookStore))
	return service, ""
}

func setupWebhookTestServiceWithEncryptedStore(t *testing.T) (*Service, string) {
	t.Helper()
	encryptor, err := cryptoutil.NewEncryptor("test-encryption-key")
	require.NoError(t, err)
	webhookStore, err := store.NewWebhookStore(testutil.NewMemoryBackend().Collection("webhooks"), encryptor)
	require.NoError(t, err)
	service := New(nil, Config{
		TokenSecret: mustTokenSecret("test"),
		TokenTTL:    time.Hour,
		BcryptCost:  4,
	}, WithWebhookStore(webhookStore))
	return service, ""
}

func signWebhookBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestService_CreateWebhook(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		result, err := service.CreateWebhook(ctx, "test-dag", "admin-user")
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.NotEmpty(t, result.Webhook.ID)
		assert.Equal(t, "test-dag", result.Webhook.DAGName)
		assert.True(t, result.Webhook.Enabled)
		assert.Equal(t, "admin-user", result.Webhook.CreatedBy)
		assert.NotEmpty(t, result.FullToken)
		assert.Contains(t, result.FullToken, "dagu_wh_")
	})

	t.Run("DefaultsToTokenOnlyAuthMode", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		result, err := service.CreateWebhook(ctx, "default-auth-mode-dag", "admin-user")
		require.NoError(t, err)

		raw, err := json.Marshal(result.Webhook)
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))

		assert.Equal(t, "token_only", decoded["authMode"])

		hmacDetails, ok := decoded["hmac"].(map[string]any)
		require.True(t, ok, "expected hmac details object in webhook JSON")
		assert.Equal(t, false, hmacDetails["enabled"])
		assert.Equal(t, false, hmacDetails["secretConfigured"])
	})

	t.Run("EmptyDAGName", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "", "admin-user")
		assert.ErrorIs(t, err, auth.ErrInvalidWebhookDAGName)
	})

	t.Run("EmptyCreatorID", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "test-dag", "")
		assert.ErrorIs(t, err, ErrInvalidCreatorID)
	})

	t.Run("DuplicateDAGName", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "duplicate-dag", "admin")
		require.NoError(t, err)

		_, err = service.CreateWebhook(ctx, "duplicate-dag", "admin")
		assert.ErrorIs(t, err, auth.ErrWebhookAlreadyExists)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "test-dag", "admin")
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})
}

func TestService_ValidateWebhookToken(t *testing.T) {
	t.Parallel()

	t.Run("ValidToken", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook and get token
		result, err := service.CreateWebhook(ctx, "valid-token-dag", "admin")
		require.NoError(t, err)

		// Validate token
		webhook, err := service.ValidateWebhookToken(ctx, "valid-token-dag", result.FullToken)
		require.NoError(t, err)
		assert.Equal(t, result.Webhook.ID, webhook.ID)
		assert.Equal(t, "valid-token-dag", webhook.DAGName)
	})

	t.Run("InvalidTokenPrefix", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook first
		_, err := service.CreateWebhook(ctx, "test-dag", "admin")
		require.NoError(t, err)

		// Try with wrong prefix
		_, err = service.ValidateWebhookToken(ctx, "test-dag", "wrong_prefix_token")
		assert.ErrorIs(t, err, ErrInvalidWebhookToken)
	})

	t.Run("InvalidTokenHash", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		_, err := service.CreateWebhook(ctx, "test-dag", "admin")
		require.NoError(t, err)

		// Try with valid prefix but wrong token
		_, err = service.ValidateWebhookToken(ctx, "test-dag", "dagu_wh_wrongtoken12345")
		assert.ErrorIs(t, err, ErrInvalidWebhookToken)
	})

	t.Run("WebhookNotFound", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.ValidateWebhookToken(ctx, "nonexistent-dag", "dagu_wh_sometoken")
		assert.ErrorIs(t, err, ErrInvalidWebhookToken)
	})

	t.Run("WebhookDisabled", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook and get token
		result, err := service.CreateWebhook(ctx, "disabled-dag", "admin")
		require.NoError(t, err)

		// Disable the webhook
		_, err = service.ToggleWebhook(ctx, "disabled-dag", false)
		require.NoError(t, err)

		// Try to validate
		_, err = service.ValidateWebhookToken(ctx, "disabled-dag", result.FullToken)
		assert.ErrorIs(t, err, ErrWebhookDisabled)
	})

	t.Run("UpdatesLastUsed", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		result, err := service.CreateWebhook(ctx, "lastused-dag", "admin")
		require.NoError(t, err)
		assert.Nil(t, result.Webhook.LastUsedAt)

		// Validate token (should update LastUsedAt)
		_, err = service.ValidateWebhookToken(ctx, "lastused-dag", result.FullToken)
		require.NoError(t, err)

		// Retrieve and check LastUsedAt was updated
		webhook, err := service.GetWebhookByDAGName(ctx, "lastused-dag")
		require.NoError(t, err)
		assert.NotNil(t, webhook.LastUsedAt)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		_, err := service.ValidateWebhookToken(ctx, "test-dag", "dagu_wh_token")
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})

	t.Run("EmptyToken", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.ValidateWebhookToken(ctx, "test-dag", "")
		assert.ErrorIs(t, err, ErrInvalidWebhookToken)
	})
}

func TestService_RegenerateWebhookToken(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		original, err := service.CreateWebhook(ctx, "regen-dag", "admin")
		require.NoError(t, err)

		// Regenerate token
		result, err := service.RegenerateWebhookToken(ctx, "regen-dag")
		require.NoError(t, err)

		assert.NotEqual(t, original.FullToken, result.FullToken)
		assert.Equal(t, original.Webhook.ID, result.Webhook.ID)
		assert.Contains(t, result.FullToken, "dagu_wh_")
	})

	t.Run("OldTokenInvalidated", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		original, err := service.CreateWebhook(ctx, "invalidate-dag", "admin")
		require.NoError(t, err)

		// Regenerate token
		_, err = service.RegenerateWebhookToken(ctx, "invalidate-dag")
		require.NoError(t, err)

		// Old token should no longer work
		_, err = service.ValidateWebhookToken(ctx, "invalidate-dag", original.FullToken)
		assert.ErrorIs(t, err, ErrInvalidWebhookToken)
	})

	t.Run("NewTokenWorks", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		_, err := service.CreateWebhook(ctx, "newtoken-dag", "admin")
		require.NoError(t, err)

		// Regenerate token
		result, err := service.RegenerateWebhookToken(ctx, "newtoken-dag")
		require.NoError(t, err)

		// New token should work
		webhook, err := service.ValidateWebhookToken(ctx, "newtoken-dag", result.FullToken)
		require.NoError(t, err)
		assert.Equal(t, result.Webhook.ID, webhook.ID)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.RegenerateWebhookToken(ctx, "nonexistent-dag")
		assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		_, err := service.RegenerateWebhookToken(ctx, "test-dag")
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})
}

func TestService_ToggleWebhook(t *testing.T) {
	t.Parallel()

	t.Run("Disable", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create enabled webhook
		_, err := service.CreateWebhook(ctx, "toggle-dag", "admin")
		require.NoError(t, err)

		// Disable
		webhook, err := service.ToggleWebhook(ctx, "toggle-dag", false)
		require.NoError(t, err)
		assert.False(t, webhook.Enabled)
	})

	t.Run("Enable", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook and disable it
		_, err := service.CreateWebhook(ctx, "enable-dag", "admin")
		require.NoError(t, err)
		_, err = service.ToggleWebhook(ctx, "enable-dag", false)
		require.NoError(t, err)

		// Re-enable
		webhook, err := service.ToggleWebhook(ctx, "enable-dag", true)
		require.NoError(t, err)
		assert.True(t, webhook.Enabled)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.ToggleWebhook(ctx, "nonexistent", true)
		assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		_, err := service.ToggleWebhook(ctx, "test-dag", true)
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})
}

func TestService_DeleteWebhook(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		_, err := service.CreateWebhook(ctx, "delete-dag", "admin")
		require.NoError(t, err)

		// Delete
		err = service.DeleteWebhook(ctx, "delete-dag")
		require.NoError(t, err)

		// Verify deleted
		_, err = service.GetWebhookByDAGName(ctx, "delete-dag")
		assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		err := service.DeleteWebhook(ctx, "nonexistent")
		assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		err := service.DeleteWebhook(ctx, "test-dag")
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})
}

func TestService_ListWebhooks(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create multiple webhooks
		_, err := service.CreateWebhook(ctx, "dag-1", "admin")
		require.NoError(t, err)
		_, err = service.CreateWebhook(ctx, "dag-2", "admin")
		require.NoError(t, err)

		// List
		webhooks, err := service.ListWebhooks(ctx)
		require.NoError(t, err)
		assert.Len(t, webhooks, 2)
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		webhooks, err := service.ListWebhooks(ctx)
		require.NoError(t, err)
		assert.Empty(t, webhooks)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		_, err := service.ListWebhooks(ctx)
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})
}

func TestService_GetWebhookByDAGName(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		// Create webhook
		result, err := service.CreateWebhook(ctx, "get-dag", "admin")
		require.NoError(t, err)

		// Get
		webhook, err := service.GetWebhookByDAGName(ctx, "get-dag")
		require.NoError(t, err)
		assert.Equal(t, result.Webhook.ID, webhook.ID)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		ctx := context.Background()

		_, err := service.GetWebhookByDAGName(ctx, "nonexistent")
		assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
	})

	t.Run("NoWebhookStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		ctx := context.Background()

		_, err := service.GetWebhookByDAGName(ctx, "test-dag")
		assert.ErrorIs(t, err, ErrWebhookNotConfigured)
	})
}

func TestService_HasWebhookStore(t *testing.T) {
	t.Parallel()

	t.Run("WithStore", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestService(t)
		assert.True(t, service.HasWebhookStore())
	})

	t.Run("WithoutStore", func(t *testing.T) {
		t.Parallel()
		service := New(nil, Config{TokenSecret: mustTokenSecret("test")})
		assert.False(t, service.HasWebhookStore())
	})
}

func TestService_EnableWebhookHMAC(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "enable-hmac-dag", "admin")
		require.NoError(t, err)

		result, err := service.EnableWebhookHMAC(
			ctx,
			"enable-hmac-dag",
			auth.WebhookAuthModeTokenAndHMAC,
			auth.WebhookHMACEnforcementModeStrict,
		)
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, auth.WebhookAuthModeTokenAndHMAC, result.Webhook.AuthMode)
		assert.Equal(t, auth.WebhookHMACEnforcementModeStrict, result.Webhook.HMACEnforcementMode)
		assert.NotEmpty(t, result.FullSecret)
		assert.NotNil(t, result.Webhook.HMACSecretGeneratedAt)
	})

	t.Run("RejectsObserveForHMACOnly", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "hmac-only-observe-dag", "admin")
		require.NoError(t, err)

		_, err = service.EnableWebhookHMAC(
			ctx,
			"hmac-only-observe-dag",
			auth.WebhookAuthModeHMACOnly,
			auth.WebhookHMACEnforcementModeObserve,
		)
		assert.ErrorIs(t, err, ErrInvalidWebhookHMACEnforcementMode)
	})
}

func TestService_ConfigureWebhookHMAC(t *testing.T) {
	t.Parallel()

	t.Run("PreservesExistingObserveWhenOmitted", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "configure-preserve-observe-dag", "admin")
		require.NoError(t, err)

		_, err = service.EnableWebhookHMAC(
			ctx,
			"configure-preserve-observe-dag",
			auth.WebhookAuthModeTokenAndHMAC,
			auth.WebhookHMACEnforcementModeObserve,
		)
		require.NoError(t, err)

		webhook, err := service.ConfigureWebhookHMAC(
			ctx,
			"configure-preserve-observe-dag",
			auth.WebhookAuthModeTokenAndHMAC,
			"",
		)
		require.NoError(t, err)
		assert.Equal(t, auth.WebhookHMACEnforcementModeObserve, webhook.HMACEnforcementMode)
	})

	t.Run("OmittedEnforcementKeepsHMACOnlyStrict", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "configure-hmac-only-strict-dag", "admin")
		require.NoError(t, err)

		_, err = service.EnableWebhookHMAC(
			ctx,
			"configure-hmac-only-strict-dag",
			auth.WebhookAuthModeTokenAndHMAC,
			auth.WebhookHMACEnforcementModeObserve,
		)
		require.NoError(t, err)

		webhook, err := service.ConfigureWebhookHMAC(
			ctx,
			"configure-hmac-only-strict-dag",
			auth.WebhookAuthModeHMACOnly,
			"",
		)
		require.NoError(t, err)
		assert.Equal(t, auth.WebhookHMACEnforcementModeStrict, webhook.HMACEnforcementMode)
	})
}

func TestService_AuthorizeWebhookRequest(t *testing.T) {
	t.Parallel()

	t.Run("TokenAndHMACStrictRequiresBoth", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		created, err := service.CreateWebhook(ctx, "strict-auth-dag", "admin")
		require.NoError(t, err)

		hmacResult, err := service.EnableWebhookHMAC(
			ctx,
			"strict-auth-dag",
			auth.WebhookAuthModeTokenAndHMAC,
			auth.WebhookHMACEnforcementModeStrict,
		)
		require.NoError(t, err)

		body := []byte(`{"payload":{"event":"push"}}`)
		signature := signWebhookBody(hmacResult.FullSecret, body)

		_, err = service.AuthorizeWebhookRequest(ctx, "strict-auth-dag", created.FullToken, "", body)
		assert.ErrorIs(t, err, ErrMissingWebhookHMACSignature)

		webhook, err := service.AuthorizeWebhookRequest(ctx, "strict-auth-dag", created.FullToken, signature, body)
		require.NoError(t, err)
		assert.Equal(t, "strict-auth-dag", webhook.DAGName)
	})

	t.Run("TokenAndHMACObserveAllowsMissingSignature", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		created, err := service.CreateWebhook(ctx, "observe-auth-dag", "admin")
		require.NoError(t, err)

		_, err = service.EnableWebhookHMAC(
			ctx,
			"observe-auth-dag",
			auth.WebhookAuthModeTokenAndHMAC,
			auth.WebhookHMACEnforcementModeObserve,
		)
		require.NoError(t, err)

		body := []byte(`{"payload":{"event":"push"}}`)
		webhook, err := service.AuthorizeWebhookRequest(ctx, "observe-auth-dag", created.FullToken, "", body)
		require.NoError(t, err)
		assert.Equal(t, "observe-auth-dag", webhook.DAGName)
	})

	t.Run("HMACOnlyAllowsSignedRequestWithoutToken", func(t *testing.T) {
		t.Parallel()
		service, _ := setupWebhookTestServiceWithEncryptedStore(t)
		ctx := context.Background()

		_, err := service.CreateWebhook(ctx, "hmac-only-dag", "admin")
		require.NoError(t, err)

		hmacResult, err := service.EnableWebhookHMAC(
			ctx,
			"hmac-only-dag",
			auth.WebhookAuthModeHMACOnly,
			auth.WebhookHMACEnforcementModeStrict,
		)
		require.NoError(t, err)

		body := []byte(`{"payload":{"event":"push"}}`)
		signature := signWebhookBody(hmacResult.FullSecret, body)

		webhook, err := service.AuthorizeWebhookRequest(ctx, "hmac-only-dag", "", signature, body)
		require.NoError(t, err)
		assert.Equal(t, "hmac-only-dag", webhook.DAGName)
	})
}

func TestGenerateWebhookToken(t *testing.T) {
	t.Parallel()

	t.Run("Format", func(t *testing.T) {
		t.Parallel()
		parts, err := generateWebhookToken(4)
		require.NoError(t, err)

		// Verify token starts with prefix
		assert.True(t, len(parts.fullToken) > len("dagu_wh_"))
		assert.Contains(t, parts.fullToken, "dagu_wh_")

		// Verify prefix is stored
		assert.True(t, len(parts.tokenPrefix) >= webhookTokenPrefixLength ||
			len(parts.tokenPrefix) == len(parts.fullToken))

		// Verify hash is generated
		assert.NotEmpty(t, parts.tokenHash)
		assert.Contains(t, parts.tokenHash, "$2a$") // bcrypt hash prefix
	})

	t.Run("Uniqueness", func(t *testing.T) {
		t.Parallel()

		tokens := make(map[string]bool)
		for range 100 {
			parts, err := generateWebhookToken(4)
			require.NoError(t, err)
			assert.False(t, tokens[parts.fullToken], "Duplicate token generated")
			tokens[parts.fullToken] = true
		}
	})
}
