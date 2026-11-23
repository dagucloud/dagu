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

// Setup needs the setup token, and the token file is removed once the admin
// exists. A server that could not prepare a token leaves setup open.
func TestSetupToken(t *testing.T) {
	const token = "0123456789abcdef"
	tests := []struct {
		name      string
		token     string
		given     *string
		wantSetup bool
	}{
		{name: "no token", token: token},
		{name: "wrong token", token: token, given: new("other")},
		{name: "setup token", token: token, given: new(" " + token + "\n"), wantSetup: true},
		{name: "no token prepared", wantSetup: true},
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
			WithSetupToken(test.token, tokenFile)(a)

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
