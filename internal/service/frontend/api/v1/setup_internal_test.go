// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
)

// setupAuthService counts and creates users in memory for first-run setup.
type setupAuthService struct {
	AuthService
	users int64
}

func (s *setupAuthService) CountUsers(context.Context) (int64, error) {
	return s.users, nil
}

func (s *setupAuthService) CreateUser(_ context.Context, input authservice.CreateUserInput) (*auth.User, error) {
	s.users++
	return &auth.User{ID: "admin-id", Username: input.Username, Role: input.Role}, nil
}

func (s *setupAuthService) GenerateToken(*auth.User) (*authservice.TokenResult, error) {
	return &authservice.TokenResult{Token: "jwt", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// For a while after startup, setup works without the setup token, so scripts
// that create the admin as the server starts keep working. After that, setup
// needs the token, and the token file is removed once the admin exists.
func TestSetupToken(t *testing.T) {
	const token = "0123456789abcdef"
	started := map[string]time.Time{
		"now":      time.Now(),
		"long ago": time.Now().Add(-2 * setupTokenlessWindow),
	}
	tests := []struct {
		name      string
		started   string
		token     string
		given     *string
		wantSetup bool
	}{
		{name: "no token right after start", started: "now", token: token, wantSetup: true},
		{name: "no token after the window", started: "long ago", token: token},
		{name: "wrong token after the window", started: "long ago", token: token, given: new("other")},
		{name: "setup token after the window", started: "long ago", token: token, given: new(" " + token + "\n"), wantSetup: true},
		{name: "no token prepared", started: "long ago", wantSetup: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			tokenFile := filepath.Join(dir, "auth", "setup-token")
			require.NoError(t, os.MkdirAll(filepath.Dir(tokenFile), 0o700))
			require.NoError(t, os.WriteFile(tokenFile, []byte(token+"\n"), 0o600))
			a := &API{
				config:      &config.Config{Paths: config.PathsConfig{UsersDir: filepath.Join(dir, "users")}},
				authService: &setupAuthService{},
			}
			WithSetupToken(test.token, tokenFile, started[test.started])(a)

			resp, err := a.Setup(context.Background(), api.SetupRequestObject{Body: &api.SetupRequest{
				Username:   "admin",
				Password:   "securepass1",
				SetupToken: test.given,
			}})
			require.NoError(t, err)

			if !test.wantSetup {
				assert.IsType(t, api.Setup401JSONResponse{}, resp)
				assert.FileExists(t, tokenFile)
				return
			}
			assert.IsType(t, api.Setup200JSONResponse{}, resp)
			if test.token != "" {
				assert.NoFileExists(t, tokenFile)
			}
		})
	}
}
