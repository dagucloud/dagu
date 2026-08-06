// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package incremental owns local file materialization evaluation.
package incremental

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
)

const schemaVersion = 1

const (
	DecisionExecute  = "execute"
	DecisionReuse    = "reuse"
	DecisionAlways   = "always"
	DecisionDeferred = "deferred"
	DecisionNone     = "none"
)

// PrepareRequest contains resolved runtime information used before execution.
type PrepareRequest struct {
	DAG                  *core.DAG
	Step                 core.Step
	DAGRunID             string
	AttemptID            string
	WorkingDir           string
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
	request      PrepareRequest
	inputs       []exec.FileSnapshot
	inputPaths   map[string]string
	output       core.StepOutputDeclaration
	outputPath   string
	outputKey    string
	recipeDigest string
	fingerprint  string
	materialKey  string
	metadata     exec.IncrementalExecution
	manifest     *exec.Materialization
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
		request:    request,
		inputPaths: make(map[string]string, len(request.Step.Inputs)),
		metadata: exec.IncrementalExecution{
			Decision: DecisionNone,
			Phase:    "precondition",
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
			Decision: DecisionAlways,
			Phase:    "execute",
			Reason:   "ineligible",
			Detail:   "step has no incremental file paths",
		}
		session.evaluated = true
		return session, nil
	}
	if store == nil {
		session.metadata.Phase = "evaluate"
		session.metadata.Reason = "store_unavailable"
		return session, fmt.Errorf("incremental materialization store is unavailable")
	}

	locks := make([]exec.PathLockRequest, 0, len(request.Step.Inputs)+1)
	for _, input := range request.Step.Inputs {
		locks = append(locks, exec.PathLockRequest{Key: ComparisonKey(input.Path), Mode: exec.PathLockShared})
	}
	if hasPathOutput {
		session.outputKey = ComparisonKey(output.Path)
		locks = append(locks, exec.PathLockRequest{Key: session.outputKey, Mode: exec.PathLockExclusive})
	}
	if !request.Dry {
		lock, err := store.AcquirePaths(ctx, locks)
		if err != nil {
			session.metadata.Phase = "evaluate"
			session.metadata.Reason = "evaluation_failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				session.metadata.Reason = "cancelled_before_decision"
			} else if errors.Is(err, exec.ErrMaterializationRecovery) {
				session.metadata.Reason = "recovery_failed"
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
	s.metadata.Phase = "evaluate"
	s.metadata.Reason = "evaluation_failed"
	if s.request.Dry && s.request.Deferred {
		s.metadata = exec.IncrementalExecution{
			Decision: DecisionDeferred,
			Phase:    "evaluate",
			Reason:   "upstream_would_execute",
			Detail:   "an upstream file producer would execute; evaluate after its output is known",
		}
		return nil
	}

	for _, input := range s.request.Step.Inputs {
		resolved, err := ResolvePath(input.Path, "", false)
		if err != nil || ComparisonKey(resolved) != ComparisonKey(input.Path) {
			s.metadata.Reason = "evaluation_failed"
			return fmt.Errorf("input path identity changed before evaluation: %s", input.Path)
		}
	}
	if s.pathBacked {
		resolved, err := ResolvePath(s.output.Path, "", true)
		if err != nil || ComparisonKey(resolved) != ComparisonKey(s.output.Path) {
			s.metadata.Reason = "evaluation_failed"
			return fmt.Errorf("output path identity changed before evaluation: %s", s.output.Path)
		}
	}

	for _, input := range s.request.Step.Inputs {
		snapshot, err := Snapshot(input.Name, input.Path)
		if err != nil {
			s.metadata.Reason = "input_missing"
			s.metadata.Detail = err.Error()
			return err
		}
		s.inputs = append(s.inputs, snapshot)
	}
	sort.Slice(s.inputs, func(i, j int) bool { return s.inputs[i].Name < s.inputs[j].Name })
	if s.pathBacked {
		s.materialKey = materializationKey(s.request.DAG.Name, s.request.Step.ID, s.outputKey)
		s.metadata.MaterializationKey = s.materialKey
	}

	eligible, detail := eligible(s.request)
	if !eligible {
		s.metadata.Decision = DecisionAlways
		s.metadata.Phase = "execute"
		s.metadata.Reason = "ineligible"
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
		s.metadata.Decision = DecisionExecute
		s.metadata.Phase = "execute"
		s.metadata.Reason = "control_dependency_ran"
		s.metadata.Detail = "an explicit control dependency executed in this run"
		return nil
	}
	if s.request.NoReuse {
		s.metadata.Decision = DecisionExecute
		s.metadata.Phase = "execute"
		s.metadata.Reason = "reuse_disabled"
		s.metadata.Detail = "reuse was disabled for this run"
		return nil
	}

	manifest, err := s.store.Get(ctx, s.materialKey)
	if errors.Is(err, exec.ErrMaterializationNotFound) {
		s.metadata.Decision = DecisionExecute
		s.metadata.Phase = "execute"
		s.metadata.Reason = "manifest_missing"
		s.metadata.Detail = "no prior successful materialization exists"
		return nil
	}
	if err != nil {
		return err
	}
	s.manifest = manifest
	if manifest.RecipeDigest != recipeDigest {
		s.executeReason("recipe_changed", "the step recipe changed")
		return nil
	}
	if !snapshotsEqual(manifest.Inputs, s.inputs) || manifest.Fingerprint != s.fingerprint {
		s.executeReason("input_changed", "declared input content changed")
		return nil
	}
	currentOutput, err := Snapshot(s.output.Name, s.output.Path)
	if err != nil {
		s.executeReason("output_missing", "the prior materialized output is unavailable")
		return nil
	}
	if !snapshotEqual(currentOutput, manifest.Output) {
		s.executeReason("output_changed", "the prior materialized output changed")
		return nil
	}
	s.metadata.Decision = DecisionReuse
	s.metadata.Phase = "complete"
	s.metadata.Reason = "matched"
	s.metadata.Detail = "recipe, inputs, and output match the committed manifest"
	s.metadata.ProducerRun = manifest.ProducerRun
	s.metadata.ProducerAttemptID = manifest.ProducerAttemptID
	return nil
}

func (s *Session) executeReason(reason, detail string) {
	s.metadata.Decision = DecisionExecute
	s.metadata.Phase = "execute"
	s.metadata.Reason = reason
	s.metadata.Detail = detail
}

// Metadata returns the current persisted decision metadata.
func (s *Session) Metadata() exec.IncrementalExecution { return s.metadata }

// Reused reports whether executor execution is unnecessary.
func (s *Session) Reused() bool { return s.metadata.Decision == DecisionReuse }

// HasPathOutput reports whether the session stages and publishes an output.
func (s *Session) HasPathOutput() bool { return s.pathBacked }

// InputPaths returns final input paths scoped to the step.
func (s *Session) InputPaths() map[string]string {
	result := make(map[string]string, len(s.inputPaths))
	for name, path := range s.inputPaths {
		result[name] = path
	}
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
	base := filepath.Base(s.outputPath)
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
		s.metadata.Phase = "complete"
		return nil
	}
	s.metadata.Phase = "verify"
	output, err := Snapshot(s.output.Name, staging)
	if err != nil {
		return fmt.Errorf("verify staged output: %w", err)
	}
	for _, expected := range s.inputs {
		current, err := Snapshot(expected.Name, expected.Path)
		if err != nil || !snapshotEqual(current, expected) {
			s.metadata.Reason = "input_changed_during_execution"
			return fmt.Errorf("input %s changed during execution", expected.Name)
		}
	}
	output.Path = s.outputPath
	commitID, err := randomID()
	if err != nil {
		return err
	}
	manifest := exec.Materialization{
		SchemaVersion:      schemaVersion,
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
	s.metadata.Phase = "commit"
	if err := s.store.Commit(ctx, s.lock, exec.MaterializationCommit{
		StagingPath: staging,
		FinalPath:   s.outputPath,
		Manifest:    manifest,
	}); err != nil {
		return err
	}
	s.metadata.Phase = "complete"
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
	if request.DAG == nil || request.DAG.Type != core.TypeIncremental {
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

type recipe struct {
	SchemaVersion int                          `json:"schemaVersion"`
	ExecutorType  string                       `json:"executorType"`
	Executor      map[string]any               `json:"executor,omitempty"`
	Commands      []core.CommandEntry          `json:"commands,omitempty"`
	Script        string                       `json:"script,omitempty"`
	Shell         []string                     `json:"shell,omitempty"`
	ShellPackages []string                     `json:"shellPackages,omitempty"`
	WorkingDir    string                       `json:"workingDir"`
	WorkingDirKey string                       `json:"workingDirKey"`
	Parameters    map[string]any               `json:"parameters,omitempty"`
	Environment   map[string]string            `json:"environment,omitempty"`
	StepEnv       []string                     `json:"stepEnv,omitempty"`
	Inputs        []core.StepInputDeclaration  `json:"inputs,omitempty"`
	Outputs       []core.StepOutputDeclaration `json:"outputs,omitempty"`
	Tools         *core.ToolConfig             `json:"tools,omitempty"`
	Platform      string                       `json:"platform"`
}

func recipeDigest(request PrepareRequest) (string, error) {
	value := recipe{
		SchemaVersion: schemaVersion,
		ExecutorType:  request.Step.ExecutorConfig.Type,
		Executor:      request.Step.ExecutorConfig.Config,
		Commands:      request.Step.Commands,
		Script:        request.Step.Script,
		Shell:         request.Shell,
		ShellPackages: request.Step.ShellPackages,
		WorkingDir:    request.WorkingDir,
		WorkingDirKey: ComparisonKey(request.WorkingDir),
		Parameters:    request.DAG.ParamValues(),
		Environment:   recipeEnvironment(request.Environment),
		StepEnv:       request.Step.Env,
		Inputs:        canonicalInputs(request.Step.Inputs),
		Outputs:       request.Step.Outputs,
		Tools:         request.DAG.Tools,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func canonicalInputs(inputs []core.StepInputDeclaration) []core.StepInputDeclaration {
	result := append([]core.StepInputDeclaration(nil), inputs...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func recipeEnvironment(environment map[string]string) map[string]string {
	if len(environment) == 0 {
		return nil
	}
	result := make(map[string]string, len(environment))
	for key, value := range environment {
		if volatileRuntimeEnvironment[key] {
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

var volatileRuntimeEnvironment = map[string]bool{
	exec.EnvKeyDAGRunID:                      true,
	exec.EnvKeyDAGRunLogFile:                 true,
	exec.EnvKeyDAGRunStepStdoutFile:          true,
	exec.EnvKeyDAGRunStepStderrFile:          true,
	exec.EnvKeyDAGUOutputFile:                true,
	exec.EnvKeyDAGRunStatus:                  true,
	exec.EnvKeyDAGWaitingSteps:               true,
	exec.EnvKeyDAGRunWorkDir:                 true,
	exec.EnvKeyDAGRunArtifactsDir:            true,
	exec.EnvKeyDAGPushBack:                   true,
	exec.EnvKeyDAGPushBackIteration:          true,
	exec.EnvKeyDAGPushBackPreviousStdoutFile: true,
}

func fingerprint(recipeDigest string, inputs []exec.FileSnapshot, controlTokens map[string]string) string {
	value := struct {
		SchemaVersion int                 `json:"schemaVersion"`
		RecipeDigest  string              `json:"recipeDigest"`
		Inputs        []exec.FileSnapshot `json:"inputs,omitempty"`
		Control       map[string]string   `json:"control,omitempty"`
	}{schemaVersion, recipeDigest, inputs, controlTokens}
	data, _ := json.Marshal(value)
	return digest(data)
}

func materializationKey(dagName, stepID, outputPath string) string {
	return strings.TrimPrefix(digest([]byte(dagName+"\x00"+stepID+"\x00"+outputPath)), "sha256:")
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(value[:])
}

// Snapshot hashes a stable regular-file snapshot.
func Snapshot(name, path string) (exec.FileSnapshot, error) {
	for range 3 {
		before, err := os.Lstat(path)
		if err != nil {
			return exec.FileSnapshot{}, err
		}
		if !before.Mode().IsRegular() {
			return exec.FileSnapshot{}, fmt.Errorf("%s is not a regular non-symlink file", path)
		}
		file, err := os.Open(path) //nolint:gosec
		if err != nil {
			return exec.FileSnapshot{}, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return exec.FileSnapshot{}, copyErr
		}
		if closeErr != nil {
			return exec.FileSnapshot{}, closeErr
		}
		after, err := os.Lstat(path)
		if err != nil {
			return exec.FileSnapshot{}, err
		}
		if os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) {
			return exec.FileSnapshot{
				Name: name, Path: path, Size: after.Size(), Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
			}, nil
		}
	}
	return exec.FileSnapshot{}, fmt.Errorf("file changed while hashing: %s", path)
}

func snapshotsEqual(left, right []exec.FileSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if !snapshotEqual(left[idx], right[idx]) {
			return false
		}
	}
	return true
}

func snapshotEqual(left, right exec.FileSnapshot) bool {
	return left.Name == right.Name && left.Path == right.Path && left.Size == right.Size && left.Digest == right.Digest
}

// ResolvePath returns an absolute path with existing ancestors resolved.
func ResolvePath(raw, base string, output bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("materialization path is empty")
	}
	path := raw
	if !filepath.IsAbs(path) {
		if base == "" {
			return "", fmt.Errorf("relative materialization path %q has no stable working directory", raw)
		}
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	if output {
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", fmt.Errorf("resolve output parent for %s: %w", path, err)
		}
		path = filepath.Join(parent, filepath.Base(path))
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("output path must not be a symlink: %s", path)
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("output path must be a regular file: %s", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return path, nil
	}
	return resolveExistingAncestor(path)
}

func resolveExistingAncestor(path string) (string, error) {
	suffix := make([]string, 0)
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for idx := len(suffix) - 1; idx >= 0; idx-- {
				resolved = filepath.Join(resolved, suffix[idx])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

// ComparisonKey returns the canonical lock and ownership key for a path.
func ComparisonKey(path string) string {
	path = filepath.Clean(path)
	if filesystemIsCaseInsensitive(path) {
		path = strings.ToLower(path)
	}
	if runtime.GOOS == "windows" {
		path = filepath.ToSlash(path)
	}
	return path
}

func filesystemIsCaseInsensitive(path string) bool {
	dir := filepath.Dir(path)
	for {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			continue
		}
		if err == nil {
			name := filepath.Base(dir)
			if alternate, ok := alternateASCIICase(name); ok {
				alternateInfo, alternateErr := os.Lstat(filepath.Join(filepath.Dir(dir), alternate))
				switch {
				case alternateErr == nil:
					return os.SameFile(info, alternateInfo)
				case errors.Is(alternateErr, os.ErrNotExist):
					return false
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func alternateASCIICase(value string) (string, bool) {
	bytes := []byte(value)
	for idx, ch := range bytes {
		switch {
		case ch >= 'a' && ch <= 'z':
			bytes[idx] = ch - ('a' - 'A')
			return string(bytes), true
		case ch >= 'A' && ch <= 'Z':
			bytes[idx] = ch + ('a' - 'A')
			return string(bytes), true
		}
	}
	return value, false
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
	value = strings.ReplaceAll(value, string(filepath.Separator), "_")
	if len(value) > 24 {
		return value[:24]
	}
	return value
}
