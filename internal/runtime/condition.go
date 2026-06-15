// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
)

// Errors for condition evaluation
var (
	ErrConditionNotMet = fmt.Errorf("condition was not met")
)

// Error message for when not all conditions are met
const ErrMsgOtherConditionNotMet = "other condition was not met"

// EvalConditions evaluates a list of conditions and checks the results.
// It returns an error if any of the conditions were not met.
func EvalConditions(ctx context.Context, shell []string, cond []*core.Condition) error {
	var lastErr error

	for i := range cond {
		if err := EvalCondition(ctx, shell, cond[i]); err != nil {
			cond[i].SetErrorMessage(err.Error())
			lastErr = err
		}
	}

	if lastErr != nil {
		// Set error message
		for i := range cond {
			if cond[i].GetErrorMessage() != "" {
				continue
			}
			cond[i].SetErrorMessage(ErrMsgOtherConditionNotMet)
		}
	}

	return lastErr
}

// EvalCondition evaluates the condition and returns the actual value.
// It returns an error if the evaluation failed or the condition is invalid.
// If c.Negate is true, the result is inverted: the condition passes when it
// would normally fail, and vice versa.
func EvalCondition(ctx context.Context, shell []string, c *core.Condition) error {
	var err error
	switch {
	case c.Condition != "" && c.Expected != "":
		err = matchCondition(ctx, c)

	default:
		err = evalCommand(ctx, shell, c)
	}

	// Apply negation if needed
	if c.Negate {
		if err == nil {
			return fmt.Errorf("%w: condition matched but negate is true", ErrConditionNotMet)
		}
		// Only invert logical "not met" failures; keep evaluation/runtime errors.
		if errors.Is(err, ErrConditionNotMet) {
			return nil
		}
		// Evaluation or runtime error - don't swallow it
		return err
	}

	return err
}

// matchCondition evaluates the condition and checks if it matches the expected value.
// It returns an error if the condition was not met.
func matchCondition(ctx context.Context, c *core.Condition) error {
	evaluatedVal, err := EvalString(ctx, c.Condition, cmnvalue.WithoutSubstitute())
	if err != nil {
		return fmt.Errorf("failed to evaluate the value: Error=%v", err)
	}

	// Get maxOutputSize from DAG configuration
	var maxOutputSize = defaultMaxOutputSizeBytes
	if rCtx := GetDAGContext(ctx); rCtx.DAG != nil && rCtx.DAG.MaxOutputSize > 0 {
		maxOutputSize = rCtx.DAG.MaxOutputSize
	}

	matchOpts := []stringutil.MatchOption{
		stringutil.WithExactMatch(),
		stringutil.WithMaxBufferSize(maxOutputSize),
	}

	if stringutil.MatchPattern(ctx, evaluatedVal, []string{c.Expected}, matchOpts...) {
		return nil
	}
	// Return an helpful error message if the condition is not met
	return fmt.Errorf("%w: expected %q, got %q", ErrConditionNotMet, c.Expected, evaluatedVal)
}

func evalCommand(ctx context.Context, shell []string, c *core.Condition) error {
	opts := append([]cmnvalue.Option{cmnvalue.WithoutSubstitute()}, CommandEvalOptions(shell)...)
	commandToRun, err := EvalString(ctx, c.Condition, opts...)
	if err != nil {
		return fmt.Errorf("failed to evaluate command: %w", err)
	}
	if len(shell) > 0 {
		return runShellCommand(ctx, shell, commandToRun)
	}
	return runDirectCommand(ctx, commandToRun)
}

func runShellCommand(ctx context.Context, shell []string, commandToRun string) error {
	args := make([]string, len(shell)-1)
	copy(args, shell[1:])
	if !slices.Contains(args, "-c") {
		args = append(args, "-c")
	}
	args = append(args, commandToRun)
	cmd := exec.CommandContext(ctx, shell[0], args...) // nolint:gosec
	cmd.Env = append(cmd.Env, AllEnvs(ctx)...)
	_, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConditionNotMet, err)
	}
	return nil
}

func runDirectCommand(ctx context.Context, commandToRun string) error {
	cmd := exec.CommandContext(ctx, commandToRun)
	cmd.Env = append(cmd.Env, AllEnvs(ctx)...)
	_, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConditionNotMet, err)
	}
	return nil
}
