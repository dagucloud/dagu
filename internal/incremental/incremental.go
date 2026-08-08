// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package incremental owns local file materialization evaluation.
package incremental

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const maxStagingBaseLength = 128

// PrepareRequest contains resolved runtime information used before execution.
type PrepareRequest struct {
	DAG                  *ir.DAG
	Step                 ir.Step
	DAGRunID             string
	AttemptID            string
	WorkingDir           string
	RunWorkDir           string
	Shell                []string
	Environment          map[string]string
	HasSecrets           bool
	NoReuse              bool
	Dry                  bool
	Deferred             bool
	ControlDependencyRan bool
	ControlTokens        map[string]string
}

// Session holds path locks and one ready-node materialization decision.
type Session struct {
	store        exec.MaterializationStore
	lock         exec.MaterializationLock
	pathKeys     *PathKeyResolver
	request      PrepareRequest
	inputs       []exec.FileSnapshot
	inputPaths   map[string]string
	output       ir.StepOutputDeclaration
	outputPath   string
	outputKey    string
	recipeDigest string
	fingerprint  string
	materialKey  string
	metadata     exec.IncrementalExecution
	pathBacked   bool
	evaluated    bool
	closed       bool
}

// Prepare acquires path locks for a ready node before its preconditions run.
func Prepare(ctx context.Context, store exec.MaterializationStore, request PrepareRequest) (*Session, error) {
	if request.DAG == nil {
		return nil, fmt.Errorf("incremental evaluation requires a workflow")
	}
	session := &Session{
		store:      store,
		pathKeys:   NewPathKeyResolver(),
		request:    request,
		inputPaths: make(map[string]string, len(request.Step.Inputs)),
		metadata: exec.IncrementalExecution{
			Decision: exec.IncrementalDecisionNone,
			Phase:    exec.IncrementalPhasePrecondition,
		},
	}
	for _, input := range request.Step.Inputs {
		session.inputPaths[input.Name] = input.Path
	}
	output, hasPathOutput := request.Step.PathOutput()
	session.output = output
	session.outputPath = output.Path
	session.pathBacked = hasPathOutput

	if len(request.Step.Inputs) == 0 && !hasPathOutput {
		session.metadata = exec.IncrementalExecution{
			Decision: exec.IncrementalDecisionAlways,
			Phase:    exec.IncrementalPhaseExecute,
			Reason:   exec.IncrementalReasonIneligible,
			Detail:   "step has no incremental file paths",
		}
		session.evaluated = true
		return session, nil
	}
	if store == nil {
		session.metadata.Phase = exec.IncrementalPhaseEvaluate
		session.metadata.Reason = exec.IncrementalReasonStoreUnavailable
		return session, fmt.Errorf("incremental materialization store is unavailable")
	}

	locks := make([]exec.PathLockRequest, 0, len(request.Step.Inputs)+1)
	for _, input := range request.Step.Inputs {
		locks = append(locks, exec.PathLockRequest{Key: session.pathKeys.ComparisonKey(input.Path), Mode: exec.PathLockShared})
	}
	if hasPathOutput {
		session.outputKey = session.pathKeys.ComparisonKey(output.Path)
		locks = append(locks, exec.PathLockRequest{Key: session.outputKey, Mode: exec.PathLockExclusive})
	}
	if !request.Dry {
		lock, err := store.AcquirePaths(ctx, locks)
		if err != nil {
			session.metadata.Phase = exec.IncrementalPhaseEvaluate
			session.metadata.Reason = exec.IncrementalReasonEvaluationFailed
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				session.metadata.Reason = exec.IncrementalReasonCancelledBeforeDecision
			} else if errors.Is(err, exec.ErrMaterializationRecovery) {
				session.metadata.Reason = exec.IncrementalReasonRecoveryFailed
			}
			return session, err
		}
		session.lock = lock
	}
	return session, nil
}

// Evaluate snapshots inputs and selects execution or reuse after preconditions pass.
func (s *Session) Evaluate(ctx context.Context) error {
	if s == nil || s.evaluated {
		return nil
	}
	s.evaluated = true
	s.metadata.Phase = exec.IncrementalPhaseEvaluate
	s.metadata.Reason = exec.IncrementalReasonEvaluationFailed
	if s.request.Dry && s.request.Deferred {
		s.metadata = exec.IncrementalExecution{
			Decision: exec.IncrementalDecisionDeferred,
			Phase:    exec.IncrementalPhaseEvaluate,
			Reason:   exec.IncrementalReasonUpstreamWouldExecute,
			Detail:   "an upstream file producer would execute; evaluate after its output is known",
		}
		return nil
	}

	for _, input := range s.request.Step.Inputs {
		resolved, err := ResolvePath(input.Path, "", false)
		if err != nil || s.pathKeys.ComparisonKey(resolved) != s.pathKeys.ComparisonKey(input.Path) {
			s.metadata.Reason = exec.IncrementalReasonEvaluationFailed
			return fmt.Errorf("input path identity changed before evaluation: %s", input.Path)
		}
	}
	if s.pathBacked {
		resolved, err := ResolvePath(s.output.Path, "", true)
		if err != nil || s.pathKeys.ComparisonKey(resolved) != s.pathKeys.ComparisonKey(s.output.Path) {
			s.metadata.Reason = exec.IncrementalReasonEvaluationFailed
			return fmt.Errorf("output path identity changed before evaluation: %s", s.output.Path)
		}
	}

	for _, input := range s.request.Step.Inputs {
		snapshot, err := Snapshot(input.Name, input.Path)
		if err != nil {
			s.metadata.Reason = exec.IncrementalReasonInputMissing
			s.metadata.Detail = err.Error()
			return err
		}
		s.inputs = append(s.inputs, snapshot)
	}
	sort.Slice(s.inputs, func(i, j int) bool { return s.inputs[i].Name < s.inputs[j].Name })
	if s.pathBacked {
		s.materialKey = materializationKey(s.request.DAG.Name, s.request.Step.ID, IdentityKey(s.outputPath))
		s.metadata.MaterializationKey = s.materialKey
	}

	eligible, detail := eligible(s.request)
	if !eligible {
		s.metadata.Decision = exec.IncrementalDecisionAlways
		s.metadata.Phase = exec.IncrementalPhaseExecute
		s.metadata.Reason = exec.IncrementalReasonIneligible
		s.metadata.Detail = detail
		return nil
	}

	recipeDigest, err := recipeDigest(s.request)
	if err != nil {
		return err
	}
	s.recipeDigest = recipeDigest
	s.fingerprint = fingerprint(recipeDigest, s.inputs, s.request.ControlTokens)
	s.metadata.Fingerprint = s.fingerprint

	if s.request.ControlDependencyRan {
		s.metadata.Decision = exec.IncrementalDecisionExecute
		s.metadata.Phase = exec.IncrementalPhaseExecute
		s.metadata.Reason = exec.IncrementalReasonControlDependencyRan
		s.metadata.Detail = "an explicit control dependency executed in this run"
		return nil
	}
	if s.request.NoReuse {
		s.metadata.Decision = exec.IncrementalDecisionExecute
		s.metadata.Phase = exec.IncrementalPhaseExecute
		s.metadata.Reason = exec.IncrementalReasonReuseDisabled
		s.metadata.Detail = "reuse was disabled for this run"
		return nil
	}

	manifest, err := s.store.Get(ctx, s.materialKey)
	if errors.Is(err, exec.ErrMaterializationNotFound) {
		s.metadata.Decision = exec.IncrementalDecisionExecute
		s.metadata.Phase = exec.IncrementalPhaseExecute
		s.metadata.Reason = exec.IncrementalReasonManifestMissing
		s.metadata.Detail = "no prior successful materialization exists"
		return nil
	}
	if err != nil {
		return err
	}
	if manifest.RecipeDigest != recipeDigest {
		s.executeReason(exec.IncrementalReasonRecipeChanged, "the step recipe changed")
		return nil
	}
	if !snapshotsEqual(manifest.Inputs, s.inputs) || manifest.Fingerprint != s.fingerprint {
		s.executeReason(exec.IncrementalReasonInputChanged, "declared input content changed")
		return nil
	}
	currentOutput, err := Snapshot(s.output.Name, s.output.Path)
	if err != nil {
		s.executeReason(exec.IncrementalReasonOutputMissing, "the prior materialized output is unavailable")
		return nil
	}
	if !snapshotEqual(currentOutput, manifest.Output) {
		s.executeReason(exec.IncrementalReasonOutputChanged, "the prior materialized output changed")
		return nil
	}
	s.metadata.Decision = exec.IncrementalDecisionReuse
	s.metadata.Phase = exec.IncrementalPhaseComplete
	s.metadata.Reason = exec.IncrementalReasonMatched
	s.metadata.Detail = "recipe, inputs, and output match the committed manifest"
	s.metadata.ProducerRun = manifest.ProducerRun
	s.metadata.ProducerAttemptID = manifest.ProducerAttemptID
	return nil
}

func (s *Session) executeReason(reason exec.IncrementalReason, detail string) {
	s.metadata.Decision = exec.IncrementalDecisionExecute
	s.metadata.Phase = exec.IncrementalPhaseExecute
	s.metadata.Reason = reason
	s.metadata.Detail = detail
}

// SetResolvedRecipe supplies the execution fields used by the reuse decision.
// It must be called before Evaluate.
func (s *Session) SetResolvedRecipe(step ir.Step, environment map[string]string) {
	s.request.Step.ExecutorConfig = step.ExecutorConfig
	s.request.Step.Commands = step.Commands
	s.request.Step.Script = step.Script
	s.request.Environment = environment
}

// Metadata returns the current persisted decision metadata.
func (s *Session) Metadata() exec.IncrementalExecution { return s.metadata }

// Reused reports whether executor execution is unnecessary.
func (s *Session) Reused() bool { return s.metadata.Decision == exec.IncrementalDecisionReuse }

// HasPathOutput reports whether the session stages and publishes an output.
func (s *Session) HasPathOutput() bool { return s.pathBacked }

// InputPaths returns final input paths scoped to the step.
func (s *Session) InputPaths() map[string]string {
	result := make(map[string]string, len(s.inputPaths))
	maps.Copy(result, s.inputPaths)
	return result
}

// PublishedOutputs returns final path outputs after reuse or commit.
func (s *Session) PublishedOutputs() map[string]string {
	if !s.pathBacked {
		return nil
	}
	return map[string]string{s.output.Name: s.outputPath}
}

// NewAttempt allocates a fresh absent sibling staging path.
func (s *Session) NewAttempt(retry int) (map[string]string, string, error) {
	if !s.pathBacked {
		return nil, "", nil
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, "", err
	}
	base := truncateToken(filepath.Base(s.outputPath), maxStagingBaseLength)
	staging := filepath.Join(filepath.Dir(s.outputPath), fmt.Sprintf(".%s.dagu-%s-%d-%s.tmp",
		base, safeToken(s.request.AttemptID), retry, hex.EncodeToString(nonce[:])))
	if _, err := os.Lstat(staging); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("staging output path is not absent: %s", staging)
	}
	return map[string]string{s.output.Name: staging}, staging, nil
}

// Commit verifies an attempt and publishes its materialization.
func (s *Session) Commit(ctx context.Context, staging string) error {
	if !s.pathBacked {
		s.metadata.Phase = exec.IncrementalPhaseComplete
		return nil
	}
	if s.store == nil {
		return fmt.Errorf("incremental materialization store is unavailable")
	}
	s.metadata.Phase = exec.IncrementalPhaseVerify
	output, err := Snapshot(s.output.Name, staging)
	if err != nil {
		return fmt.Errorf("verify staged output: %w", err)
	}
	for _, expected := range s.inputs {
		current, err := Snapshot(expected.Name, expected.Path)
		if err != nil || !snapshotEqual(current, expected) {
			s.metadata.Reason = exec.IncrementalReasonInputChangedDuringExecution
			return fmt.Errorf("input %s changed during execution", expected.Name)
		}
	}
	output.Path = s.outputPath
	commitID, err := randomID()
	if err != nil {
		return err
	}
	manifest := exec.Materialization{
		SchemaVersion:      exec.MaterializationSchemaVersion,
		MaterializationKey: s.materialKey,
		CommitID:           commitID,
		DAGName:            s.request.DAG.Name,
		StepID:             s.request.Step.ID,
		RecipeDigest:       s.recipeDigest,
		Fingerprint:        s.fingerprint,
		Inputs:             s.inputs,
		Output:             output,
		ProducerRun:        exec.NewDAGRunRef(s.request.DAG.Name, s.request.DAGRunID),
		ProducerAttemptID:  s.request.AttemptID,
		CompletedAt:        time.Now().UTC(),
	}
	s.metadata.Phase = exec.IncrementalPhaseCommit
	if err := s.store.Commit(ctx, s.lock, exec.MaterializationCommit{
		StagingPath:      staging,
		FinalPath:        s.outputPath,
		Manifest:         manifest,
		PreserveManifest: s.metadata.Decision == exec.IncrementalDecisionAlways,
	}); err != nil {
		return err
	}
	s.metadata.Phase = exec.IncrementalPhaseComplete
	return nil
}

// Close releases path locks and removes an uncommitted staging file.
func (s *Session) Close(staging string) error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	if staging != "" {
		_ = os.Remove(staging)
	}
	if s.lock != nil {
		return s.lock.Release()
	}
	return nil
}

func eligible(request PrepareRequest) (bool, string) {
	step := request.Step
	if request.DAG == nil || request.DAG.Type != ir.TypeIncremental {
		return false, "workflow is not incremental"
	}
	if step.ID == "" {
		return false, "step has no id"
	}
	if len(step.Outputs) != 1 || step.Outputs[0].Path == "" {
		return false, "step does not declare exactly one path output"
	}
	if step.Output != "" || len(step.StructuredOutput) > 0 || step.StdoutOutputs != nil {
		return false, "step publishes dynamic outputs"
	}
	if step.HumanTask != nil || step.Approval != nil || step.RepeatPolicy.RepeatMode != "" || step.Parallel != nil || step.Foreach != nil || step.SubDAG != nil {
		return false, "step lifecycle is not reusable"
	}
	if request.DAG.Container != nil || step.Container != nil {
		return false, "container execution is not reusable"
	}
	executorType := step.ExecutorConfig.Type
	if executorType != "" && executorType != "command" && executorType != "shell" {
		return false, "executor does not support incremental paths"
	}
	if request.HasSecrets {
		return false, "secret-consuming execution is not reusable"
	}
	return true, ""
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func safeToken(value string) string {
	if value == "" {
		return "attempt"
	}
	value = strings.NewReplacer("/", "_", `\`, "_").Replace(value)
	return truncateToken(value, 24)
}

func truncateToken(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && value[limit]&0xc0 == 0x80 {
		limit--
	}
	return value[:limit]
}
