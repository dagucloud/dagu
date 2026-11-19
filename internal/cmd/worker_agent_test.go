// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentWorkerConfig returns a configuration that passes the checks of an agent
// worker: mutual TLS and no explicit worker.max_active_runs.
func agentWorkerConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Core.Peer = config.Peer{CertFile: "/tls/agent.crt", KeyFile: "/tls/agent.key", ClientCaFile: "/tls/ca.crt"}
	cfg.Worker.MaxActiveRuns = 100
	return cfg
}

func agentBinary(t *testing.T, mode os.FileMode) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the file mode of an agent binary has no executable bit on Windows; an agent worker runs on Unix")
	}
	path := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

// Spec 092, "Start", checks 1 to 5. Each error names the option or the key, the
// expected value and the value.
func TestCheckAgentWorker(t *testing.T) {
	t.Parallel()

	binary := agentBinary(t, 0o755)

	option, err := checkAgentWorker([]string{"claude=" + binary}, false, agentWorkerConfig())
	require.NoError(t, err)
	assert.Equal(t, &agentWorkerOption{provider: "claude", path: binary}, option)

	for _, tc := range []struct {
		name   string
		values []string
		flag   bool
		cfg    func(*config.Config)
		want   string
	}{
		{name: "TwoOptions", values: []string{"claude=" + binary, "codex=" + binary}, want: "--agent: the option is given 2 times: an agent worker serves one provider"},
		{name: "EmptyValue", values: nil, want: "the option is given 0 times"},
		{name: "NoEquals", values: []string{"claude"}, want: `--agent: the value "claude" does not have the form <provider>=<path>`},
		{name: "NoPath", values: []string{"claude="}, want: "does not have the form <provider>=<path>"},
		{name: "NoProvider", values: []string{"=" + binary}, want: "does not have the form <provider>=<path>"},
		{name: "UnknownProvider", values: []string{"nope=" + binary}, want: `--agent: provider "nope" is not a CLI provider: expected one of`},
		{name: "ChatProvider", values: []string{"gemini=" + binary}, want: `provider "gemini" is not a CLI provider`},
		{name: "RelativePath", values: []string{"claude=bin/claude"}, want: `--agent: provider "claude": the path "bin/claude" is not absolute`},
		{name: "MissingBinary", values: []string{"claude=/opt/none/claude"}, want: `--agent: provider "claude": binary /opt/none/claude does not exist`},
		{name: "Directory", values: []string{"claude=" + filepath.Dir(binary)}, want: "is not a regular file"},
		{name: "NotExecutableByOthers", values: []string{"claude=" + agentBinary(t, 0o750)}, want: "is not executable by the restricted user"},
		{name: "MaxActiveRunsFlag", values: []string{"claude=" + binary}, flag: true, cfg: func(c *config.Config) { c.Worker.MaxActiveRuns = 4 },
			want: `--agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run`},
		{name: "MaxActiveRunsFile", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Worker.MaxActiveRunsSource = "file" },
			want: `key "worker.max_active_runs" is set to "100"`},
		{name: "MaxActiveRunsEnv", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Worker.MaxActiveRunsSource = "env:DAGU_WORKER_MAX_ACTIVE_RUNS" },
			want: `key "worker.max_active_runs" is set to`},
		{name: "Insecure", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Core.Peer.Insecure = true },
			want: `--agent: key "peer.insecure" is true: an agent worker needs mutual TLS to the coordinator`},
		{name: "SkipTLSVerify", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Core.Peer.SkipTLSVerify = true },
			want: `--agent: key "peer.skip_tls_verify" is true`},
		{name: "NoCertFile", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Core.Peer.CertFile = "" },
			want: `--agent: key "peer.cert_file" is not set: an agent worker needs mutual TLS to the coordinator`},
		{name: "NoKeyFile", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Core.Peer.KeyFile = "" },
			want: `key "peer.key_file" is not set`},
		{name: "NoClientCA", values: []string{"claude=" + binary}, cfg: func(c *config.Config) { c.Core.Peer.ClientCaFile = "" },
			want: `key "peer.client_ca_file" is not set`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := agentWorkerConfig()
			if tc.cfg != nil {
				tc.cfg(cfg)
			}
			_, err := checkAgentWorker(tc.values, tc.flag, cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
