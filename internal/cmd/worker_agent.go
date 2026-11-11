// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
)

// flagAgent is the option of `dagu worker` that makes an agent worker.
const flagAgent = "agent"

// agentWorkerOption is the checked value of the option --agent.
type agentWorkerOption struct {
	provider string
	path     string
}

// agentWorkerFromFlags checks the option --agent and the configuration of an
// agent worker (Spec 092, "Start", checks 1 to 5). It returns nil when the
// command has no option --agent.
func agentWorkerFromFlags(ctx *Context) (*agentWorkerOption, error) {
	flags := ctx.Command.Flags()
	if !flags.Changed(flagAgent) {
		return nil, nil
	}
	values, err := flags.GetStringArray(flagAgent)
	if err != nil {
		return nil, fmt.Errorf("--agent: %w", err)
	}
	return checkAgentWorker(values, flags.Changed("worker.max-active-runs"), ctx.Config)
}

// checkAgentWorker makes the checks of an agent worker that need no
// coordinator. maxActiveRunsFlag tells whether the command line sets
// --worker.max-active-runs. Each error names the option or the key, the
// expected value and the value.
func checkAgentWorker(values []string, maxActiveRunsFlag bool, cfg *config.Config) (*agentWorkerOption, error) {
	// 1. The form, and one option.
	if len(values) != 1 {
		return nil, fmt.Errorf("--agent: the option is given %d times: an agent worker serves one provider", len(values))
	}
	provider, path, ok := strings.Cut(values[0], "=")
	if !ok || provider == "" || path == "" {
		return nil, fmt.Errorf("--agent: the value %q does not have the form <provider>=<path>", values[0])
	}

	// 2. A CLI provider and an absolute path.
	if !ir.IsAgentCLIProvider(provider) {
		return nil, fmt.Errorf("--agent: provider %q is not a CLI provider: expected one of %s",
			provider, strings.Join(ir.AgentCLIProviderNames(), ", "))
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("--agent: provider %q: the path %q is not absolute", provider, path)
	}

	// 3. A regular file that the restricted user can run.
	if err := checkAgentBinary(provider, path); err != nil {
		return nil, err
	}
	if cfg.Untrusted.Enabled {
		if err := untrusted.CheckExecutable(path, cfg.Untrusted); err != nil {
			return nil, fmt.Errorf("--agent: provider %q: %w", provider, err)
		}
	}

	// 4. No explicit worker.max_active_runs.
	if maxActiveRunsFlag || cfg.Worker.MaxActiveRunsSource != "" {
		return nil, fmt.Errorf("--agent: key %q is set to %q: an agent worker always runs one run",
			"worker.max_active_runs", fmt.Sprint(cfg.Worker.MaxActiveRuns))
	}

	// 5. Mutual TLS to the coordinator.
	peer := cfg.Core.Peer
	for _, key := range []struct {
		name string
		on   bool
	}{
		{"peer.insecure", peer.Insecure},
		{"peer.skip_tls_verify", peer.SkipTLSVerify},
	} {
		if key.on {
			return nil, fmt.Errorf("--agent: key %q is true: an agent worker needs mutual TLS to the coordinator", key.name)
		}
	}
	for _, key := range []struct {
		name  string
		value string
	}{
		{"peer.cert_file", peer.CertFile},
		{"peer.key_file", peer.KeyFile},
		{"peer.client_ca_file", peer.ClientCaFile},
	} {
		if strings.TrimSpace(key.value) == "" {
			return nil, fmt.Errorf("--agent: key %q is not set: an agent worker needs mutual TLS to the coordinator", key.name)
		}
	}
	return &agentWorkerOption{provider: provider, path: path}, nil
}

// checkAgentBinary checks that the path is a regular file with the execute bit
// for other users. The restricted user is not the owner of the binary of the
// image, so the bit for other users is the bit that applies to it.
func checkAgentBinary(provider, path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("--agent: provider %q: binary %s does not exist", provider, path)
	}
	if err != nil {
		return fmt.Errorf("--agent: provider %q: binary %s: %w", provider, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("--agent: provider %q: binary %s is not a regular file (mode %s)", provider, path, info.Mode())
	}
	if info.Mode().Perm()&0o001 == 0 {
		return fmt.Errorf("--agent: provider %q: binary %s is not executable by the restricted user: expected the execute bit for other users, got mode %s",
			provider, path, info.Mode().Perm())
	}
	return nil
}
