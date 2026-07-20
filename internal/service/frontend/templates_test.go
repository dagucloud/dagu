// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"text/template"
	"time"

	apiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/license"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubWorkspaceStore struct {
	items []*workspacepkg.Workspace
}

func (s stubWorkspaceStore) Create(context.Context, *workspacepkg.Workspace) error {
	return nil
}

func (s stubWorkspaceStore) GetByID(context.Context, string) (*workspacepkg.Workspace, error) {
	return nil, nil
}

func (s stubWorkspaceStore) GetByName(context.Context, string) (*workspacepkg.Workspace, error) {
	return nil, nil
}

func (s stubWorkspaceStore) List(context.Context) ([]*workspacepkg.Workspace, error) {
	return s.items, nil
}

func (s stubWorkspaceStore) Update(context.Context, *workspacepkg.Workspace) error {
	return nil
}

func (s stubWorkspaceStore) Delete(context.Context, string) error {
	return nil
}

func resetAssetVersionCache() {
	assetVersion = ""
	assetVersionOnce = sync.Once{}
}

func TestFormatAssetVersionUsesBundleHashForDevBuilds(t *testing.T) {
	bundle := []byte("bundle")
	sum := sha256.Sum256(bundle)
	want := "0.0.0-" + hex.EncodeToString(sum[:8])

	assert.Equal(t, want, formatAssetVersion("0.0.0", bundle))
}

func TestFormatAssetVersionSupportsEmptyVersion(t *testing.T) {
	bundle := []byte("bundle")
	sum := sha256.Sum256(bundle)
	want := hex.EncodeToString(sum[:8])

	assert.Equal(t, want, formatAssetVersion("", bundle))
}

func TestCurrentAssetVersionUsesReleaseVersionAndBundleHashWhenSet(t *testing.T) {
	originalVersion := config.Version
	t.Cleanup(func() {
		config.Version = originalVersion
		resetAssetVersionCache()
	})

	config.Version = "1.2.3"
	resetAssetVersionCache()

	data, err := assetsFS.ReadFile("assets/bundle.js")
	if err != nil {
		assert.Equal(t, "1.2.3", currentAssetVersion())
		return
	}

	assert.Equal(t, formatAssetVersion("1.2.3", data), currentAssetVersion())
}

func TestDefaultFunctionsExposeInitialWorkspacesJSON(t *testing.T) {
	createdAt := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, time.March, 2, 10, 0, 0, 0, time.UTC)
	funcs := defaultFunctions(&funcsConfig{
		WorkspaceStore: stubWorkspaceStore{
			items: []*workspacepkg.Workspace{
				{
					ID:          "ws-1",
					Name:        "ops",
					Description: "Operations",
					CreatedAt:   createdAt,
					UpdatedAt:   updatedAt,
				},
			},
		},
	})

	initialWorkspacesJSON, ok := funcs["initialWorkspacesJSON"].(func() string)
	require.True(t, ok)

	var workspaces []apiv1.WorkspaceResponse
	err := json.Unmarshal([]byte(initialWorkspacesJSON()), &workspaces)
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	assert.Equal(t, "ws-1", workspaces[0].Id)
	assert.Equal(t, "ops", workspaces[0].Name)
	require.NotNil(t, workspaces[0].Description)
	assert.Equal(t, "Operations", *workspaces[0].Description)
	require.NotNil(t, workspaces[0].CreatedAt)
	assert.True(t, workspaces[0].CreatedAt.Equal(createdAt))
	require.NotNil(t, workspaces[0].UpdatedAt)
	assert.True(t, workspaces[0].UpdatedAt.Equal(updatedAt))
}

func TestDefaultFunctionsExposeLicensedTrustedProxyLogin(t *testing.T) {
	t.Parallel()

	licensed := license.NewTestManager(license.FeatureSSO)
	funcs := defaultFunctions(&funcsConfig{
		TrustedProxyEnabled:     true,
		TrustedProxyButtonLabel: "Continue with Corporate SSO",
		LicenseChecker:          licensed.Checker(),
	})

	enabled, ok := funcs["trustedProxyEnabled"].(func() string)
	require.True(t, ok)
	assert.Equal(t, "true", enabled())
	label, ok := funcs["trustedProxyButtonLabel"].(func() string)
	require.True(t, ok)
	assert.Equal(t, "Continue with Corporate SSO", label())

	unlicensed := license.NewTestManager(license.FeatureRBAC)
	funcs = defaultFunctions(&funcsConfig{
		TrustedProxyEnabled: true,
		LicenseChecker:      unlicensed.Checker(),
	})
	enabled = funcs["trustedProxyEnabled"].(func() string)
	assert.Equal(t, "false", enabled())
}

func TestBaseTemplateEscapesTrustedProxyLoginLabelForJavaScript(t *testing.T) {
	t.Parallel()

	const label = `</script><script>alert("injected")</script>`
	tmpl, err := template.New("base").Funcs(defaultFunctions(&funcsConfig{
		TrustedProxyEnabled:     true,
		TrustedProxyButtonLabel: label,
	})).ParseFS(assetsFS, "templates/base.gohtml")
	require.NoError(t, err)
	tmpl, err = tmpl.Parse(`{{define "content"}}{{end}}`)
	require.NoError(t, err)

	var output bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&output, "base", nil))
	assert.NotContains(t, output.String(), label)
	assert.NotContains(t, output.String(), `</script><script>alert`)
}

func TestDefaultFunctionsExposeLicenseGraceEndsAt(t *testing.T) {
	expiry := time.Date(2026, time.March, 15, 12, 0, 0, 0, time.UTC)

	t.Run("uses claims grace days when present", func(t *testing.T) {
		var checker license.State
		graceDays := 30
		checker.Update(&license.LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expiry),
			},
			GraceDays: &graceDays,
		}, "tok")

		funcs := defaultFunctions(&funcsConfig{LicenseChecker: &checker})
		graceEndsAtFn, ok := funcs["licenseGraceEndsAt"].(func() string)
		require.True(t, ok)

		assert.Equal(t, "2026-04-14T12:00:00Z", graceEndsAtFn())
	})

	t.Run("falls back to the default grace period", func(t *testing.T) {
		var checker license.State
		checker.Update(&license.LicenseClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expiry),
			},
		}, "tok")

		funcs := defaultFunctions(&funcsConfig{LicenseChecker: &checker})
		graceEndsAtFn, ok := funcs["licenseGraceEndsAt"].(func() string)
		require.True(t, ok)

		assert.Equal(t, "2026-03-29T12:00:00Z", graceEndsAtFn())
	})
}
