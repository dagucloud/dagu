// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Errors for condition evaluation
var (
	ErrConditionNotMet = fmt.Errorf("condition was not met")

	// errConditionInterrupted marks an evaluation cut short by workflow abort
	// or timeout. The owning DAG or step follows that outcome instead of
	// treating the result as not met or failed.
	errConditionInterrupted = errors.New("condition check interrupted")
)

// Back-fill messages for conditions that passed while a sibling decided the
// outcome. The wording mirrors how the owner is reported: a not-met condition
// skips it, while an evaluation error fails it.
const (
	ErrMsgOtherConditionNotMet = "other condition was not met"
	ErrMsgOtherConditionFailed = "other condition failed to evaluate"
)

// EvaluateConditions evaluates conditions and returns their runtime results.
func EvaluateConditions(ctx context.Context, shell []string, conditions []*ir.Condition) ([]ir.ConditionResult, error) {
	results := conditionResults(conditions)
	var lastErr, evalErr error

	for i := range conditions {
		if err := EvalCondition(ctx, shell, conditions[i]); err != nil {
			results[i].Error = err.Error()
			lastErr = err
			if evalErr == nil && !errors.Is(err, ErrConditionNotMet) {
				evalErr = err
			}
		}
	}

	if lastErr != nil {
		siblingErr := ErrMsgOtherConditionNotMet
		if evalErr != nil {
			siblingErr = ErrMsgOtherConditionFailed
		}
		for i := range results {
			if results[i].Error != "" {
				continue
			}
			results[i].Error = siblingErr
		}
	}

	// An evaluation error outranks a not-met condition regardless of the order
	// they appear in, so that a broken gate fails the owning DAG or step
	// instead of being downgraded to a skip by a later mismatch.
	err := lastErr
	if evalErr != nil {
		err = evalErr
	}

	// A context that ended during evaluation means abort or timeout cut the
	// checks short, whatever each condition reported.
	if err != nil && ctx.Err() != nil && !errors.Is(err, errConditionInterrupted) {
		err = fmt.Errorf("%w: %w", errConditionInterrupted, ctx.Err())
	}

	return results, err
}

func conditionResults(conditions []*ir.Condition) []ir.ConditionResult {
	if len(conditions) == 0 {
		return nil
	}
	results := make([]ir.ConditionResult, len(conditions))
	for i, condition := range conditions {
		if condition != nil {
			results[i].Condition = *condition
		}
	}
	return results
}

// EvalCondition evaluates the condition and returns the actual value.
// It returns an error if the evaluation failed or the condition is invalid.
// If c.Negate is true, the result is inverted: the condition passes when it
// would normally fail, and vice versa.
func EvalCondition(ctx context.Context, shell []string, c *ir.Condition) error {
	var err error
	switch {
	case len(c.ExpectedPatterns()) > 0 && (c.Condition != "" || c.Eval != ""):
		err = matchCondition(ctx, shell, c)
	case c.Eval != "":
		err = fmt.Errorf("expected is required when eval is set")

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

// matchCondition evaluates the condition and checks if it matches any of the
// expected patterns. It returns an error if the condition was not met.
func matchCondition(ctx context.Context, shell []string, c *ir.Condition) error {
	raw := c.Condition
	field := cmnvalue.ConditionRuntimeValueField("condition")
	if c.Eval != "" {
		raw = c.Eval
		field = cmnvalue.ConditionEvalField("eval")
		ctx = conditionEvalContext(ctx, shell)
	}

	evaluatedVal, err := resolveRuntimeString(ctx, raw, field)
	if err != nil {
		return fmt.Errorf("failed to evaluate the value: Error=%v", err)
	}

	patterns := c.ExpectedPatterns()
	var textPatterns []string
	numericMatched := false
	for _, pattern := range patterns {
		if !stringutil.HasNumericPrefix(pattern) {
			textPatterns = append(textPatterns, pattern)
			continue
		}
		// Every numeric pattern is evaluated even after one matches, so a
		// pattern that cannot be evaluated fails the check regardless of order.
		err := matchNumericCondition(ctx, pattern, evaluatedVal)
		switch {
		case err == nil:
			numericMatched = true
		case !errors.Is(err, ErrConditionNotMet):
			return err
		}
	}
	if numericMatched {
		return nil
	}

	if len(textPatterns) > 0 {
		// Get maxOutputSize from DAG configuration
		var maxOutputSize = defaultMaxOutputSizeBytes
		if rCtx := GetDAGContext(ctx); rCtx.DAG != nil && rCtx.DAG.MaxOutputSize > 0 {
			maxOutputSize = rCtx.DAG.MaxOutputSize
		}

		matchOpts := []stringutil.MatchOption{
			stringutil.WithExactMatch(),
			stringutil.WithMaxBufferSize(maxOutputSize),
		}

		if stringutil.MatchPattern(ctx, evaluatedVal, textPatterns, matchOpts...) {
			return nil
		}
	}

	// Return an helpful error message if the condition is not met
	if len(patterns) == 1 {
		return fmt.Errorf("%w: expected %q, got %q", ErrConditionNotMet, patterns[0], evaluatedVal)
	}
	return fmt.Errorf("%w: expected one of %q, got %q", ErrConditionNotMet, patterns, evaluatedVal)
}

// matchNumericCondition compares an actual value against a numeric-comparison
// pattern. A value that is not a number is an evaluation error rather than a
// not-met condition, so that a numeric gate cannot silently stop gating.
//
// Every message reports the pattern as authored, never as resolved: a threshold
// can come from a secret, and these strings are persisted with the run.
func matchNumericCondition(ctx context.Context, expected, actual string) error {
	comparison, err := ResolveNumericComparison(ctx, expected, "expected")
	if err != nil {
		return fmt.Errorf("invalid numeric comparison %q: %w", expected, err)
	}
	matched, err := comparison.Match(actual)
	if err != nil {
		return fmt.Errorf("numeric comparison %q: %w", expected, err)
	}
	if matched {
		return nil
	}
	return fmt.Errorf("%w: expected %q, got %q", ErrConditionNotMet, expected, actual)
}

// ResolveNumericComparison parses a numeric-comparison pattern, resolving a value
// reference in its threshold first. fieldPath names the field for notices.
//
// The whole pattern is resolved rather than just the threshold, which is
// equivalent because the numeric prefix and the ordering operators contain no
// dollar sign. A reference that cannot be resolved is preserved as its own text,
// so it reaches the parser as a non-number and fails there.
//
// No returned error quotes a resolved threshold, which may hold a secret, so a
// caller can wrap the error while reporting the pattern as authored. Every
// surface that compares a numeric pattern must go through here, so that gating
// and routing cannot disagree about what a threshold means.
func ResolveNumericComparison(ctx context.Context, pattern, fieldPath string) (stringutil.NumericComparison, error) {
	if !strings.ContainsRune(pattern, '$') {
		return stringutil.ParseNumericPattern(pattern)
	}
	resolved, err := resolveRuntimeString(ctx, pattern, cmnvalue.ConditionRuntimeValueField(fieldPath))
	if err != nil {
		return stringutil.NumericComparison{}, err
	}
	comparison, err := stringutil.ParseNumericPattern(resolved)
	if err != nil {
		if resolved == pattern {
			// Nothing was substituted, so the reference has no value. Say so
			// rather than calling the reference text a bad number: the caller
			// already quotes the pattern, which names the reference.
			return stringutil.NumericComparison{}, fmt.Errorf("threshold reference did not resolve")
		}
		return stringutil.NumericComparison{}, fmt.Errorf("threshold did not resolve to a number")
	}
	return comparison, nil
}

func conditionEvalContext(ctx context.Context, shell []string) context.Context {
	if len(shell) > 0 {
		ctx = cmnvalue.WithCommandSubstitutionShell(ctx, shell)
	}
	if env, ok := conditionEnv(ctx); ok {
		ctx = cmnvalue.WithCommandSubstitutionWorkingDir(ctx, env.WorkingDir)
	}
	return ctx
}

func evalCommand(ctx context.Context, shell []string, c *ir.Condition) error {
	command := cmnvalue.CommandContext{
		Target:          cmnvalue.CommandTargetLocal,
		Shell:           shell,
		ShellConfigured: len(shell) > 0,
	}
	commandToRun, err := resolveRuntimeString(ctx, c.Condition, cmnvalue.ConditionCommandField("condition", command))
	if err != nil {
		return fmt.Errorf("failed to evaluate command: %w", err)
	}
	workingDir := ""
	if env, ok := conditionEnv(ctx); ok {
		workingDir = env.WorkingDir
	}
	if len(shell) > 0 {
		return runShellCommand(ctx, shell, commandToRun, workingDir)
	}
	return runDirectCommand(ctx, commandToRun, workingDir)
}

func conditionEnv(ctx context.Context) (Env, bool) {
	if env, ok := LookupEnv(ctx); ok {
		return env, true
	}
	rCtx, ok := LookupDAGContext(ctx)
	if !ok || rCtx.DAG == nil {
		return Env{}, false
	}
	return NewEnv(ctx, ir.Step{}), true
}

func runShellCommand(ctx context.Context, shell []string, commandToRun string, workingDir string) error {
	args := make([]string, len(shell)-1)
	copy(args, shell[1:])
	args = appendShellCommandFlag(shell[0], args)
	args = append(args, commandToRun)
	cmd := exec.CommandContext(ctx, shell[0], args...) // nolint:gosec
	prepareConditionCommand(cmd)
	cmd.Env = append(cmd.Env, AllEnvs(ctx)...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	if err := cmd.Run(); err != nil {
		return commandCheckError(ctx, err)
	}
	return nil
}

func appendShellCommandFlag(shell string, args []string) []string {
	if hasShellCommandFlag(shell, args) {
		return args
	}
	return append(args, cmdutil.ShellCommandFlag(shell))
}

func hasShellCommandFlag(shell string, args []string) bool {
	for _, arg := range args {
		switch {
		case cmdutil.IsPowerShell(shell):
			if strings.EqualFold(arg, "-Command") || strings.EqualFold(arg, "-C") {
				return true
			}
		case cmdutil.IsCmdShell(shell):
			if strings.EqualFold(arg, "/c") {
				return true
			}
		case cmdutil.IsNixShell(shell):
			if arg == "--run" {
				return true
			}
		default:
			if arg == "-c" {
				return true
			}
		}
	}
	return false
}

func runDirectCommand(ctx context.Context, commandToRun string, workingDir string) error {
	cmd := exec.CommandContext(ctx, commandToRun)
	prepareConditionCommand(cmd)
	cmd.Env = append(cmd.Env, AllEnvs(ctx)...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	if err := cmd.Run(); err != nil {
		return commandCheckError(ctx, err)
	}
	return nil
}

// prepareConditionCommand groups the check's process and points the context
// kill at that group, so abort or timeout also reaps children the check
// spawned. Stdout and stderr stay unset: the result is the exit status alone,
// and without output pipes a lingering descendant cannot delay it.
func prepareConditionCommand(cmd *exec.Cmd) {
	cmdutil.SetupCommand(cmd)
	cmd.Cancel = func() error {
		return cmdutil.TerminateProcessGroup(cmd, cmdutil.ForceTermination())
	}
}

// commandCheckError classifies a failed command-check run. A context that is
// canceled or past its deadline means the check was interrupted by abort or
// timeout rather than answered no, so the error propagates as an evaluation
// error for the owning DAG or step. Any other failure, including a non-zero
// exit and a process that never started, is a not-met result.
func commandCheckError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", errConditionInterrupted, ctxErr)
	}
	return fmt.Errorf("%w: %s", ErrConditionNotMet, err)
}
