// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailbox"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.Executor                = (*organizeExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*organizeExecutor)(nil)
)

type organizeConfig struct {
	Mailbox string `mapstructure:"mailbox"`
	Emails  any    `mapstructure:"emails"`
	Mark    string `mapstructure:"mark"`
	Move    string `mapstructure:"move"`
	Folder  string `mapstructure:"folder"`
	DryRun  bool   `mapstructure:"dry_run"`
}

type organizeExecutor struct {
	stdout  io.Writer
	address string
	account mailboxAccount
	options mailbox.OrganizeOptions

	mu      sync.Mutex
	cancel  context.CancelFunc
	outputs map[string]any
}

func newOrganize(ctx context.Context, step ir.Step) (executor.Executor, error) {
	var cfg organizeConfig
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}
	if cfg.Mark == "" && cfg.Move == "" {
		return nil, errors.New("mail.organize requires with.mark or with.move")
	}
	if marks := ir.MailMarks(); cfg.Mark != "" && !slices.Contains(marks, cfg.Mark) {
		return nil, fmt.Errorf("with.mark must be one of %s", strings.Join(marks, ", "))
	}
	if moves := ir.MailMoves(); cfg.Move != "" && !slices.Contains(moves, cfg.Move) {
		return nil, fmt.Errorf("with.move must be one of %s", strings.Join(moves, ", "))
	}
	items, err := parseEmails(cfg.Emails)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if !mailbox.ValidID(item.ID) {
			return nil, fmt.Errorf("with.emails: malformed email ID %q", item.ID)
		}
		if cfg.Move == mailbox.MoveFolder && cfg.Folder == "" && item.MoveTo == "" {
			return nil, errors.New("move: folder needs with.folder or a move_to on every email")
		}
	}

	address := accountAddress(cfg.Mailbox)
	resolved, err := runtime.NewEnv(ctx, step).MailAccount(ctx, address)
	if err != nil {
		return nil, err
	}
	account, err := newMailboxAccount(address, resolved)
	if err != nil {
		return nil, err
	}
	return &organizeExecutor{
		stdout:  os.Stdout,
		address: address,
		account: account,
		options: mailbox.OrganizeOptions{
			Items:  items,
			Mark:   cfg.Mark,
			Move:   cfg.Move,
			Folder: cfg.Folder,
			DryRun: cfg.DryRun,
		},
	}, nil
}

// parseEmails reads one item or a list. An item is an ID, or an object with
// an id and an optional move_to. An email from mail.search is such an object;
// its folder field says where it is, not where to move it. A string holding
// JSON, as a loop item or a step output arrives, is decoded first.
func parseEmails(value any) ([]mailbox.Item, error) {
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
			var decoded any
			if err := json.Unmarshal([]byte(text), &decoded); err != nil {
				return nil, fmt.Errorf("with.emails: %w", err)
			}
			value = decoded
		}
	}
	list, isList := value.([]any)
	if !isList {
		list = []any{value}
	}
	items := make([]mailbox.Item, 0, len(list))
	for i, element := range list {
		switch v := element.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return nil, fmt.Errorf("with.emails[%d] is empty", i)
			}
			items = append(items, mailbox.Item{ID: strings.TrimSpace(v)})
		case map[string]any:
			id, _ := v["id"].(string)
			if strings.TrimSpace(id) == "" {
				return nil, fmt.Errorf("with.emails[%d] has no id", i)
			}
			moveTo, _ := v["move_to"].(string)
			items = append(items, mailbox.Item{ID: strings.TrimSpace(id), MoveTo: strings.TrimSpace(moveTo)})
		default:
			return nil, fmt.Errorf("with.emails[%d] must be an ID or an object with an id", i)
		}
	}
	return items, nil
}

func (e *organizeExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *organizeExecutor) SetStderr(io.Writer)     {}

func (e *organizeExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *organizeExecutor) Run(ctx context.Context) error {
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

	result, err := client.Organize(e.options)
	if err != nil {
		return accountError(e.address, err)
	}

	e.mu.Lock()
	e.outputs = map[string]any{"changed": result.Changed, "missing": result.Missing}
	e.mu.Unlock()

	verb := "Changed"
	if e.options.DryRun {
		verb = "Would change"
	}
	_, _ = fmt.Fprintf(e.stdout, "%s %d emails; %d missing\n", verb, result.Changed, len(result.Missing))
	return nil
}

// GetOutputs returns changed and missing after a successful run.
func (e *organizeExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

// PublishesDeclaredOutputs exposes the fixed outputs to step references.
func (e *organizeExecutor) PublishesDeclaredOutputs() bool { return true }
