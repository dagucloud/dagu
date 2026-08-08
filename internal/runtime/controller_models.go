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
	"github.com/dagucloud/dagu/v2/internal/runtime/controller"
)

type controllerModelPlanner struct {
	candidates []controllerModelCandidate
	current    int
	failures   []error
}

type controllerModelCandidate struct {
	cfg      *ir.LLMConfig
	planner  *controller.Planner
	setupErr error
}

func newControllerModelPlanner(
	ctx context.Context,
	cfg *ir.LLMConfig,
	models []ir.ModelEntry,
	catalog *controller.Catalog,
	system string,
	mask controller.MaskFunc,
) *controllerModelPlanner {
	candidates := make([]controllerModelCandidate, len(models))
	for i, model := range models {
		effectiveCfg := EffectiveLLMConfig(cfg, model)
		provider, err := NewLLMProvider(ctx, effectiveCfg)
		candidates[i] = controllerModelCandidate{cfg: effectiveCfg, setupErr: err}
		if err == nil {
			candidates[i].planner = controller.NewPlanner(
				provider, effectiveCfg, catalog, system, mask)
		}
	}
	return &controllerModelPlanner{candidates: candidates}
}

func (p *controllerModelPlanner) Next(
	ctx context.Context,
	state *controller.State,
	observationKeepRecent int,
	observationMaxBytes int,
) (*controller.Decision, error) {
	recoveryAttempted := false

	for {
		candidate := &p.candidates[p.current]
		if len(p.candidates) > 1 {
			logger.Info(ctx, "Attempting controller decision",
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
				logger.Warn(ctx, "Controller context overflowed; retrying with aged observations",
					slog.String("provider", candidate.cfg.Provider),
					slog.String("model", candidate.cfg.Model),
					slog.Int("compactedObservations", compacted))
				decision, err = candidate.Next(ctx, state)
				if err == nil {
					return decision, nil
				}
				err = fmt.Errorf("controller decision failed after aging observations: %w", err)
			}
		}

		if len(p.candidates) == 1 || ctx.Err() != nil {
			return nil, err
		}

		logger.Warn(ctx, "Controller model failed",
			slog.String("provider", candidate.cfg.Provider),
			slog.String("model", candidate.cfg.Model),
			tag.Error(err))
		p.failures = append(p.failures, fmt.Errorf(
			"%s/%s: %w", candidate.cfg.Provider, candidate.cfg.Model, err))

		if p.current == len(p.candidates)-1 {
			return nil, fmt.Errorf("all %d controller models exhausted: %w",
				len(p.candidates), errors.Join(p.failures...))
		}

		p.current++
		next := &p.candidates[p.current]
		logger.Info(ctx, "Falling back to next controller model",
			slog.String("provider", next.cfg.Provider),
			slog.String("model", next.cfg.Model),
			slog.Int("modelIndex", p.current))
	}
}

func (c *controllerModelCandidate) Next(
	ctx context.Context,
	state *controller.State,
) (*controller.Decision, error) {
	if c.setupErr != nil {
		return nil, c.setupErr
	}
	return c.planner.Next(ctx, state)
}
