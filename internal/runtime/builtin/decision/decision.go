// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

type decisionExecutor struct {
	stdout io.Writer
	cfg    config
	client *client
	masker *masking.Masker
	cancel context.CancelFunc
	ctx    context.Context
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{})
}

func newExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(false); err != nil {
		return nil, err
	}
	endpoint, keyName := cfg.connection()
	scope := runtime.GetEnv(ctx).Scope
	if scope == nil {
		return nil, fmt.Errorf("decision: API key environment variable %q is not set", keyName)
	}
	key, ok := scope.Get(keyName)
	if !ok || key == "" {
		return nil, fmt.Errorf("decision: API key environment variable %q is not set", keyName)
	}
	secrets := []string{keyName + "=" + key}
	for name, secret := range scope.AllSecrets() {
		secrets = append(secrets, name+"="+secret)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	return &decisionExecutor{
		stdout: os.Stdout,
		cfg:    cfg,
		client: &client{http: llm.NewHTTPClient(llm.DefaultConfig()), endpoint: endpoint, apiKey: key},
		masker: masking.NewMasker(masking.SourcedEnvVars{Secrets: secrets}),
		cancel: cancel,
		ctx:    requestCtx,
	}, nil
}

func (e *decisionExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (*decisionExecutor) SetStderr(io.Writer)       {}
func (e *decisionExecutor) Kill(os.Signal) error {
	e.cancel()
	return nil
}
func (e *decisionExecutor) Close() error {
	e.cancel()
	return nil
}

func (e *decisionExecutor) Run(ctx context.Context) error {
	stop := context.AfterFunc(ctx, e.cancel)
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	questions := make(map[string]question, len(e.cfg.Questions))
	for id, q := range e.cfg.Questions {
		q.Instructions = maskValue(q.Instructions, e.masker)
		q.Criteria = maskValue(q.Criteria, e.masker)
		questions[id] = q
	}
	result, err := e.client.evaluate(e.ctx, request{
		Model: e.cfg.Model, State: maskValue(e.cfg.State, e.masker), Questions: questions,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.ctx.Err() != nil {
			return e.ctx.Err()
		}
		return errors.New(e.masker.MaskString(err.Error()))
	}
	return json.NewEncoder(e.stdout).Encode(maskValue(result, e.masker))
}

func maskValue(v any, masker *masking.Masker) any {
	switch v := v.(type) {
	case string:
		return masker.MaskString(v)
	case map[string]any:
		masked := make(map[string]any, len(v))
		for key, val := range v {
			masked[key] = maskValue(val, masker)
		}
		return masked
	case []any:
		masked := make([]any, len(v))
		for i, val := range v {
			masked[i] = maskValue(val, masker)
		}
		return masked
	default:
		return v
	}
}
