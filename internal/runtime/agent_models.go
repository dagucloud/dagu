// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
)

type agentModelPlanner struct {
	candidates []agentModelCandidate
	current    int
	failures   []error
}

type agentModelCandidate struct {
	cfg      *ir.LLMConfig
	planner  *agentloop.Planner
	setupErr error
}

func newAgentModelPlanner(
	ctx context.Context,
	cfg *ir.LLMConfig,
	models []ir.ModelEntry,
	catalog *agentloop.Catalog,
	system string,
	mask agentloop.MaskFunc,
) *agentModelPlanner {
	candidates := make([]agentModelCandidate, len(models))
	for i, model := range models {
		effectiveCfg := EffectiveLLMConfig(cfg, model)
		provider, err := NewLLMProvider(ctx, effectiveCfg)
		candidates[i] = agentModelCandidate{cfg: effectiveCfg, setupErr: err}
		if err == nil {
			candidates[i].planner = agentloop.NewPlanner(
				provider, effectiveCfg, catalog, system, mask)
		}
	}
	return &agentModelPlanner{candidates: candidates}
}

func (p *agentModelPlanner) Next(
	ctx context.Context,
	state *agentloop.State,
	observationKeepRecent int,
	observationMaxBytes int,
) ([]agentloop.Decision, error) {
	recoveryAttempted := false

	for {
		candidate := &p.candidates[p.current]
		if len(p.candidates) > 1 {
			logger.Info(ctx, "Attempting agent decision",
				slog.String("provider", candidate.cfg.Provider),
				slog.String("model", candidate.cfg.Model),
				slog.Int("modelIndex", p.current))
		}

		decision, err := candidate.Next(ctx, state)
		if err == nil {
			return decision, nil
		}

		if errors.Is(err, llmpkg.ErrContextTooLong) &&
			observationKeepRecent > 0 && !recoveryAttempted {
			compacted := state.CompactAllObservations(observationMaxBytes)
			if compacted > 0 {
				state.EnableObservationAging()
				recoveryAttempted = true
				logger.Warn(ctx, "Agent context overflowed; retrying with aged observations",
					slog.String("provider", candidate.cfg.Provider),
					slog.String("model", candidate.cfg.Model),
					slog.Int("compactedObservations", compacted))
				decision, err = candidate.Next(ctx, state)
				if err == nil {
					return decision, nil
				}
				err = fmt.Errorf("agent decision failed after aging observations: %w", err)
			}
		}

		if len(p.candidates) == 1 || ctx.Err() != nil {
			return nil, err
		}

		logger.Warn(ctx, "Agent model failed",
			slog.String("provider", candidate.cfg.Provider),
			slog.String("model", candidate.cfg.Model),
			tag.Error(err))
		p.failures = append(p.failures, fmt.Errorf(
			"%s/%s: %w", candidate.cfg.Provider, candidate.cfg.Model, err))

		if p.current == len(p.candidates)-1 {
			return nil, fmt.Errorf("all %d agent models exhausted: %w",
				len(p.candidates), errors.Join(p.failures...))
		}

		p.current++
		next := &p.candidates[p.current]
		logger.Info(ctx, "Falling back to next agent model",
			slog.String("provider", next.cfg.Provider),
			slog.String("model", next.cfg.Model),
			slog.Int("modelIndex", p.current))
	}
}

func (c *agentModelCandidate) Next(
	ctx context.Context,
	state *agentloop.State,
) ([]agentloop.Decision, error) {
	if c.setupErr != nil {
		return nil, c.setupErr
	}
	return c.planner.Next(ctx, state)
}
