// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectService(t *testing.T) {
	systemUnit := func(_, _, systemUnitDir string) string {
		return filepath.Join(systemUnitDir, "dagu.service")
	}
	userUnit := func(_, homeDir, _ string) string {
		return filepath.Join(homeDir, ".config", "systemd", "user", "dagu.service")
	}
	systemRestart := "sudo systemctl restart dagu"
	stopsRunsSystem := &Service{RestartCommand: systemRestart, StopsRuns: true, KeepRunsCommand: systemKeepRunsCommand}
	keepsRunsSystem := &Service{RestartCommand: systemRestart}

	tests := []struct {
		name       string
		goos       string
		definition func(execDir, homeDir, systemUnitDir string) string
		content    string
		dropIns    map[string]string
		want       *Service
	}{
		{
			name: "macOS launch agent",
			goos: "darwin",
			definition: func(_, homeDir, _ string) string {
				return filepath.Join(homeDir, "Library", "LaunchAgents", "local.dagu.server.plist")
			},
			want: &Service{RestartCommand: "launchctl kickstart -k gui/$(id -u)/local.dagu.server"},
		},
		{
			name:       "Linux system unit with the default KillMode",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nExecStart=/usr/local/bin/dagu start-all\n",
			want:       stopsRunsSystem,
		},
		{
			name:       "Linux system unit that keeps runs",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nExecStart=/usr/local/bin/dagu start-all\nKillMode = process\n",
			want:       keepsRunsSystem,
		},
		{
			name:       "Linux system unit that ends runs",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nKillMode=mixed\n",
			want:       stopsRunsSystem,
		},
		{
			name:       "KillMode outside the Service section",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Unit]\nKillMode=process\n[Service]\n# KillMode=process\n",
			want:       stopsRunsSystem,
		},
		{
			name:       "drop-in keeps runs",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nKillMode=mixed\n",
			dropIns:    map[string]string{"keep-runs.conf": "[Service]\nKillMode=process\n"},
			want:       keepsRunsSystem,
		},
		{
			name:       "later drop-in resets KillMode",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nKillMode=process\n",
			dropIns: map[string]string{
				"a.conf":     "[Service]\nKillMode=process\n",
				"b.conf":     "[Service]\nKillMode=\n",
				"c.conf.bak": "[Service]\nKillMode=process\n",
			},
			want: stopsRunsSystem,
		},
		{
			name:       "Linux user unit with the default KillMode",
			goos:       "linux",
			definition: userUnit,
			content:    "[Service]\nExecStart=/home/me/.local/bin/dagu start-all\n",
			want: &Service{
				RestartCommand:  "systemctl --user restart dagu",
				StopsRuns:       true,
				KeepRunsCommand: userKeepRunsCommand,
			},
		},
		{
			name: "Windows service wrapper",
			goos: "windows",
			definition: func(execDir, _, _ string) string {
				return filepath.Join(execDir, "dagu-service.xml")
			},
			want: &Service{RestartCommand: "Restart-Service Dagu", StopsRuns: true},
		},
		{
			name: "definition from another platform",
			goos: "linux",
			definition: func(_, homeDir, _ string) string {
				return filepath.Join(homeDir, "Library", "LaunchAgents", "local.dagu.server.plist")
			},
			want: nil,
		},
		{
			name: "no service installed",
			goos: "darwin",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execDir, homeDir, systemUnitDir := t.TempDir(), t.TempDir(), t.TempDir()
			if tt.definition != nil {
				path := tt.definition(execDir, homeDir, systemUnitDir)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
				require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))
				for name, content := range tt.dropIns {
					require.NoError(t, os.MkdirAll(path+".d", 0o750))
					require.NoError(t, os.WriteFile(filepath.Join(path+".d", name), []byte(content), 0o600))
				}
			}

			assert.Equal(t, tt.want, detectService(tt.goos, execDir, homeDir, systemUnitDir))
		})
	}
}

// The keep-runs commands must write a drop-in that detection then accepts.
// Stand-ins for sudo and systemctl let them run without privileges.
func TestKeepRunsCommandsKeepRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the commands target systemd")
	}
	binDir := t.TempDir()
	reloads := filepath.Join(t.TempDir(), "reloads")
	writeScript := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)) //nolint:gosec // test stand-in must be executable
	}
	writeScript("sudo", `exec "$@"`)
	writeScript("systemctl", `echo "$*" >> `+reloads)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Run("system unit", func(t *testing.T) {
		systemUnitDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(systemUnitDir, "dagu.service"), []byte("[Service]\n"), 0o600))
		command := strings.ReplaceAll(systemKeepRunsCommand, systemdSystemUnitDir, systemUnitDir)

		require.NoError(t, (&Service{KeepRunsCommand: command}).KeepRuns(t.Context()))

		service := detectService("linux", "", "", systemUnitDir)
		require.NotNil(t, service)
		assert.False(t, service.StopsRuns)
	})

	t.Run("user unit", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		unitDir := filepath.Join(homeDir, ".config", "systemd", "user")
		require.NoError(t, os.MkdirAll(unitDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(unitDir, "dagu.service"), []byte("[Service]\n"), 0o600))

		require.NoError(t, (&Service{KeepRunsCommand: userKeepRunsCommand}).KeepRuns(t.Context()))

		service := detectService("linux", "", homeDir, t.TempDir())
		require.NotNil(t, service)
		assert.False(t, service.StopsRuns)
	})

	recorded, err := os.ReadFile(reloads) //nolint:gosec // test-owned file
	require.NoError(t, err)
	assert.Equal(t, "daemon-reload\n--user daemon-reload\n", string(recorded))
}
