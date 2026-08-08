// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package incremental

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	fingerprintSchemaVersion = 1
	runWorkDirFingerprint    = "${DAG_RUN_WORK_DIR}"
)

type recipe struct {
	SchemaVersion int                        `json:"schemaVersion"`
	ExecutorType  string                     `json:"executorType"`
	Executor      map[string]any             `json:"executor,omitempty"`
	Commands      []ir.CommandEntry          `json:"commands,omitempty"`
	Script        string                     `json:"script,omitempty"`
	Shell         []string                   `json:"shell,omitempty"`
	ShellPackages []string                   `json:"shellPackages,omitempty"`
	WorkingDir    string                     `json:"workingDir"`
	WorkingDirKey string                     `json:"workingDirKey"`
	Parameters    map[string]any             `json:"parameters,omitempty"`
	Environment   map[string]string          `json:"environment,omitempty"`
	StepEnv       []string                   `json:"stepEnv,omitempty"`
	Inputs        []ir.StepInputDeclaration  `json:"inputs,omitempty"`
	Outputs       []ir.StepOutputDeclaration `json:"outputs,omitempty"`
	Tools         *ir.ToolConfig             `json:"tools,omitempty"`
	Platform      string                     `json:"platform"`
}

func recipeDigest(request PrepareRequest) (string, error) {
	workingDir, workingDirKey := recipeWorkingDir(request.WorkingDir, request.RunWorkDir)
	value := recipe{
		SchemaVersion: fingerprintSchemaVersion,
		ExecutorType:  request.Step.ExecutorConfig.Type,
		Executor:      request.Step.ExecutorConfig.Config,
		Commands:      request.Step.Commands,
		Script:        request.Step.Script,
		Shell:         request.Shell,
		ShellPackages: request.Step.ShellPackages,
		WorkingDir:    workingDir,
		WorkingDirKey: workingDirKey,
		Parameters:    request.DAG.ParamValues(),
		Environment:   recipeEnvironment(request.Environment, request.WorkingDir, request.RunWorkDir),
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

func recipeWorkingDir(workingDir, runWorkDir string) (string, string) {
	if runWorkDir != "" {
		relative, err := filepath.Rel(runWorkDir, workingDir)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if relative == "." {
				return runWorkDirFingerprint, runWorkDirFingerprint
			}
			normalized := filepath.Join(runWorkDirFingerprint, relative)
			return normalized, normalized
		}
	}
	return workingDir, ComparisonKey(workingDir)
}

func canonicalInputs(inputs []ir.StepInputDeclaration) []ir.StepInputDeclaration {
	result := append([]ir.StepInputDeclaration(nil), inputs...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func recipeEnvironment(environment map[string]string, workingDir, runWorkDir string) map[string]string {
	if len(environment) == 0 {
		return nil
	}
	result := make(map[string]string, len(environment))
	for key, value := range environment {
		if volatileRuntimeEnvironment[key] {
			continue
		}
		if key == "PWD" && value == workingDir {
			value, _ = recipeWorkingDir(value, runWorkDir)
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
	}{fingerprintSchemaVersion, recipeDigest, inputs, controlTokens}
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
