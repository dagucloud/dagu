// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectLifecycle proves Spec 102 "Lifecycle": an admin creates a
// project over the API, lists it, and deletes it once the server that
// serves it has released it; the default project cannot be deleted; and
// dagu project delete removes a project the same way. With projects off,
// /projects does not exist.
func TestProjectLifecycle(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	in.write(t, "projects/weather/forecast.yaml", "steps:\n  - run: echo forecast\n")
	auth := []string{
		"DAGU_AUTH_MODE=builtin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=admin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=adminpass",
	}

	in.serve(t, false, auth, func(base string) {
		code, body := authorized(t, http.MethodGet, base+"/projects", login(t, base), "")
		require.Equal(t, http.StatusNotFound, code, body)
	})

	in.serve(t, true, auth, func(base string) {
		session := login(t, base)
		code, body := authorized(t, http.MethodPost, base+"/projects", session, `{"name":"ops","displayName":"Operations"}`)
		require.Equal(t, http.StatusCreated, code, body)
		require.DirExists(t, filepath.Join(in.home, "projects", "ops"))

		code, body = authorized(t, http.MethodGet, base+"/projects", session, "")
		require.Equal(t, http.StatusOK, code, body)
		var list struct {
			Projects []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"projects"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &list))
		names := make([]string, 0, len(list.Projects))
		for _, p := range list.Projects {
			names = append(names, p.Name)
			assert.Equal(t, "active", p.State, p.Name)
		}
		assert.Equal(t, []string{"default", "ops", "weather"}, names)

		code, body = authorized(t, http.MethodDelete, base+"/projects/default", session, "")
		require.Equal(t, http.StatusConflict, code, body)
		code, body = authorized(t, http.MethodDelete, base+"/projects/OPS", session, "")
		require.Equal(t, http.StatusNoContent, code, body)
		assert.NoDirExists(t, filepath.Join(in.home, "projects", "ops"))
		assert.NoDirExists(t, filepath.Join(in.home, "data", "projects", "ops"))
	})

	result := in.run(true, nil, "project", "delete", "weather")
	require.Equal(t, 0, result.ExitCode(), result.Stdout()+result.Stderr())
	requireOutput(t, result, "Deleted project weather.")
	assert.NoDirExists(t, filepath.Join(in.home, "projects", "weather"))
	requireOutput(t, in.run(true, nil, "project", "delete", "default"), "the default project cannot be deleted")
}
