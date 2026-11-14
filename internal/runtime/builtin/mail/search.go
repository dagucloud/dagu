// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/mailbox"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

const (
	// Output budget: 900 KiB, or the DAG's max_output_size less this margin.
	outputBudget = 900 << 10
	outputMargin = 64 << 10
)

var (
	_ executor.Executor                = (*searchExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*searchExecutor)(nil)
)

type searchConfig struct {
	Mailbox         string `mapstructure:"mailbox"`
	Folder          string `mapstructure:"folder"`
	Unread          bool   `mapstructure:"unread"`
	From            string `mapstructure:"from"`
	Subject         string `mapstructure:"subject"`
	Within          string `mapstructure:"within"`
	HasAttachments  bool   `mapstructure:"has_attachments"`
	SaveAttachments bool   `mapstructure:"save_attachments"`
	Limit           int    `mapstructure:"limit"`
}

type searchExecutor struct {
	stdout  io.Writer
	address string
	account mailboxAccount
	options mailbox.SearchOptions
	budget  int

	mu      sync.Mutex
	cancel  context.CancelFunc
	outputs map[string]any
}

func newSearch(ctx context.Context, step ir.Step) (executor.Executor, error) {
	cfg := searchConfig{Limit: ir.MailSearchDefaultLimit}
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}
	if cfg.Limit < ir.MailSearchMinLimit || cfg.Limit > ir.MailSearchMaxLimit {
		return nil, fmt.Errorf("with.limit must be an integer from %d to %d", ir.MailSearchMinLimit, ir.MailSearchMaxLimit)
	}
	options := mailbox.SearchOptions{
		Folder:         cfg.Folder,
		Unread:         cfg.Unread,
		From:           cfg.From,
		Subject:        cfg.Subject,
		HasAttachments: cfg.HasAttachments,
		Limit:          cfg.Limit,
	}
	if cfg.Within != "" {
		within, err := spec.ParseDuration(cfg.Within)
		if err != nil {
			return nil, fmt.Errorf("with.within must be a duration such as 24h or 7d")
		}
		options.Within = within
	}

	env := runtime.NewEnv(ctx, step)
	if cfg.SaveAttachments {
		dir := ""
		if env.Scope != nil {
			dir, _ = env.Scope.Get(runenv.EnvKeyDAGRunArtifactsDir)
		}
		if dir == "" {
			return nil, errors.New("save_attachments requires artifact storage")
		}
		options.AttachmentsDir = filepath.Join(dir, "mail", fileutil.SafeName(step.Name))
	}

	address := accountAddress(cfg.Mailbox)
	resolved, err := env.MailAccount(ctx, address)
	if err != nil {
		return nil, err
	}
	account, err := newMailboxAccount(address, resolved)
	if err != nil {
		return nil, err
	}

	limit := ir.DefaultMaxOutputSize
	if env.DAG != nil && env.DAG.MaxOutputSize > 0 {
		limit = env.DAG.MaxOutputSize
	}
	return &searchExecutor{
		stdout:  os.Stdout,
		address: address,
		account: account,
		options: options,
		budget:  max(min(outputBudget, limit-outputMargin), 0),
	}, nil
}

func (e *searchExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *searchExecutor) SetStderr(io.Writer)     {}

func (e *searchExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *searchExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	client, err := e.account.open(ctx)
	if err != nil {
		return accountError(e.address, err)
	}
	defer func() { _ = client.Close() }()

	messages, partial, err := client.Search(e.options)
	if err != nil {
		return accountError(e.address, err)
	}
	messages, truncated := mailbox.Fit(messages, e.budget)

	e.mu.Lock()
	e.outputs = map[string]any{"messages": messages, "count": len(messages), "truncated": truncated || partial}
	e.mu.Unlock()

	folder := e.options.Folder
	if folder == "" {
		folder = "INBOX"
	}
	_, _ = fmt.Fprintf(e.stdout, "Found %d emails in %s\n", len(messages), folder)
	return nil
}

// GetOutputs returns messages, count, and truncated after a successful run.
func (e *searchExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

// PublishesDeclaredOutputs exposes the fixed outputs to step references.
func (e *searchExecutor) PublishesDeclaredOutputs() bool { return true }
