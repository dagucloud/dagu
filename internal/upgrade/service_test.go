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
	launchAgent := func(_, homeDir, _ string) string {
		return filepath.Join(homeDir, "Library", "LaunchAgents", "local.dagu.server.plist")
	}
	winswConfig := func(execDir, _, _ string) string {
		return filepath.Join(execDir, "dagu-service.xml")
	}
	// launchAgentPlist is the installer's launch agent with keepAlive as the
	// value of KeepAlive.
	launchAgentPlist := func(keepAlive string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>local.dagu.server</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>DAGU_HOME</key>
    <string>/Users/me/.dagu</string>
  </dict>
  <key>KeepAlive</key>
  ` + keepAlive + `
</dict>
</plist>
`
	}
	launchdRestart := "launchctl kickstart -k gui/$(id -u)/local.dagu.server"
	winswRestart := "Restart-Service Dagu"
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
			name:       "macOS launch agent that restarts any exit",
			goos:       "darwin",
			definition: launchAgent,
			content:    launchAgentPlist("<true/>"),
			want:       &Service{RestartCommand: launchdRestart, RestartsOnExit: true},
		},
		{
			name:       "macOS launch agent that restarts only a failure",
			goos:       "darwin",
			definition: launchAgent,
			content:    launchAgentPlist("<dict><key>SuccessfulExit</key><false/></dict>"),
			want:       &Service{RestartCommand: launchdRestart},
		},
		{
			name:       "macOS launch agent that stays stopped",
			goos:       "darwin",
			definition: launchAgent,
			content:    launchAgentPlist("<false/>"),
			want:       &Service{RestartCommand: launchdRestart},
		},
		{
			name:       "Linux system unit that restarts any exit",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nRestart=always\nKillMode=process\n",
			want:       &Service{RestartCommand: systemRestart, RestartsOnExit: true},
		},
		{
			name:       "Linux system unit that restarts only a failure",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nRestart=on-failure\nKillMode=process\n",
			want:       keepsRunsSystem,
		},
		{
			name:       "drop-in stops restarting",
			goos:       "linux",
			definition: systemUnit,
			content:    "[Service]\nRestart=always\nKillMode=process\n",
			dropIns:    map[string]string{"no-restart.conf": "[Service]\nRestart=no\n"},
			want:       keepsRunsSystem,
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
			name:       "Windows service wrapper that restarts a failure",
			goos:       "windows",
			definition: winswConfig,
			content:    `<service><id>Dagu</id><onfailure action="restart" delay="10 sec"/></service>`,
			want:       &Service{RestartCommand: winswRestart, RestartsOnExit: true},
		},
		{
			name:       "Windows service wrapper that restarts only the first failure",
			goos:       "windows",
			definition: winswConfig,
			content:    `<service><onfailure action="restart"/><onfailure action="none"/></service>`,
			want:       &Service{RestartCommand: winswRestart},
		},
		{
			name:       "Windows service wrapper without failure actions",
			goos:       "windows",
			definition: winswConfig,
			content:    `<service><id>Dagu</id></service>`,
			want:       &Service{RestartCommand: winswRestart},
		},
		{
			name:       "definition from another platform",
			goos:       "linux",
			definition: launchAgent,
			want:       nil,
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

// Only the process the service runs is restarted by it; a Dagu started by
// hand on the same machine is not.
func TestRunsUnderService(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(key string) string { return vars[key] }
	}
	none := env(nil)

	tests := []struct {
		name   string
		goos   string
		getenv func(string) string
		cgroup string
		want   bool
	}{
		{name: "systemd system unit", goos: "linux", getenv: none, cgroup: "0::/system.slice/dagu.service\n", want: true},
		{name: "systemd user unit", goos: "linux", getenv: none, cgroup: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/dagu.service\n", want: true},
		{name: "systemd v1 hierarchy", goos: "linux", getenv: none, cgroup: "12:pids:/system.slice/dagu.service\n1:name=systemd:/system.slice/dagu.service\n", want: true},
		{name: "terminal session", goos: "linux", getenv: none, cgroup: "0::/user.slice/user-1000.slice/session-3.scope\n", want: false},
		{name: "another unit", goos: "linux", getenv: none, cgroup: "0::/system.slice/my-dagu.service\n", want: false},
		{name: "launchd agent", goos: "darwin", getenv: env(map[string]string{"XPC_SERVICE_NAME": "local.dagu.server"}), want: true},
		{name: "macOS terminal", goos: "darwin", getenv: env(map[string]string{"XPC_SERVICE_NAME": "0"}), want: false},
		{name: "WinSW service", goos: "windows", getenv: env(map[string]string{"SERVICE_ID": "Dagu", "BASE": "/opt/dagu"}), want: true},
		{name: "WinSW service of another binary", goos: "windows", getenv: env(map[string]string{"SERVICE_ID": "Dagu", "BASE": "/opt/other"}), want: false},
		{name: "Windows terminal", goos: "windows", getenv: none, want: false},
		// A workflow step that starts Dagu shares the service's cgroup and
		// environment, but the service restarts only its own process.
		{name: "systemd unit, started by a step", goos: "linux", getenv: env(map[string]string{"DAG_RUN_ID": "run-1"}), cgroup: "0::/system.slice/dagu.service\n", want: false},
		{name: "launchd agent, started by a step", goos: "darwin", getenv: env(map[string]string{"XPC_SERVICE_NAME": "local.dagu.server", "DAG_RUN_ID": "run-1"}), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, runsUnderService(tt.goos, tt.getenv, tt.cgroup, "/opt/dagu"))
		})
	}
}
