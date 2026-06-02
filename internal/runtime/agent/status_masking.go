// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"maps"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/masking"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func (a *Agent) maskStatusSecrets(status *exec.DAGRunStatus) {
	if a.secretMasker == nil {
		return
	}

	for _, node := range status.Nodes {
		maskNodeSecrets(a.secretMasker, node)
	}
	maskNodeSecrets(a.secretMasker, status.OnInit)
	maskNodeSecrets(a.secretMasker, status.OnExit)
	maskNodeSecrets(a.secretMasker, status.OnSuccess)
	maskNodeSecrets(a.secretMasker, status.OnFailure)
	maskNodeSecrets(a.secretMasker, status.OnAbort)
	maskNodeSecrets(a.secretMasker, status.OnWait)
	status.Error = a.secretMasker.MaskString(status.Error)
}

func newStatusSecretMasker(secretEnvs []string) *masking.Masker {
	if len(secretEnvs) == 0 {
		return nil
	}
	return masking.NewMasker(masking.SourcedEnvVars{Secrets: secretEnvs})
}

func maskNodeSecrets(masker *masking.Masker, node *exec.Node) {
	if node == nil {
		return
	}
	node.Step = maskStepSecrets(masker, node.Step)
	node.Error = masker.MaskString(node.Error)
	node.OutputVariables = maskOutputVariables(masker, node.OutputVariables)
	node.OutputValue = maskStringPointer(masker, node.OutputValue)
	node.OutputsValue = maskStringPointer(masker, node.OutputsValue)
}

func maskOutputVariables(masker *masking.Masker, values *collections.SyncMap) *collections.SyncMap {
	if values == nil {
		return nil
	}
	masked := &collections.SyncMap{}
	values.Range(func(key, value any) bool {
		text, ok := value.(string)
		if !ok {
			masked.Store(key, value)
			return true
		}
		masked.Store(key, masker.MaskString(text))
		return true
	})
	return masked
}

func maskStepSecrets(masker *masking.Masker, step core.Step) core.Step {
	step.Command = masker.MaskString(step.Command)
	step.CmdWithArgs = masker.MaskString(step.CmdWithArgs)
	step.CmdArgsSys = masker.MaskString(step.CmdArgsSys)
	step.ShellCmdArgs = masker.MaskString(step.ShellCmdArgs)
	step.Script = masker.MaskString(step.Script)
	step.Args = maskStrings(masker, step.Args)
	step.Env = maskStrings(masker, step.Env)

	if len(step.Commands) > 0 {
		commands := append([]core.CommandEntry(nil), step.Commands...)
		for i := range commands {
			commands[i].Command = masker.MaskString(commands[i].Command)
			commands[i].Args = maskStrings(masker, commands[i].Args)
			commands[i].CmdWithArgs = masker.MaskString(commands[i].CmdWithArgs)
		}
		step.Commands = commands
	}

	if len(step.ExecutorConfig.Config) > 0 {
		step.ExecutorConfig.Config = maskAnyStringMap(masker, step.ExecutorConfig.Config)
	}

	if len(step.ExecutorConfig.Metadata) > 0 {
		step.ExecutorConfig.Metadata = maskAnyStringMap(masker, step.ExecutorConfig.Metadata)
	}

	return step
}

func maskStrings(masker *masking.Masker, values []string) []string {
	if len(values) == 0 {
		return values
	}
	masked := append([]string(nil), values...)
	for i := range masked {
		masked[i] = masker.MaskString(masked[i])
	}
	return masked
}

func maskStringPointer(masker *masking.Masker, value *string) *string {
	if value == nil {
		return nil
	}
	masked := masker.MaskString(*value)
	return &masked
}

func maskAnyStringValues(masker *masking.Masker, value any) any {
	switch typed := value.(type) {
	case string:
		return masker.MaskString(typed)
	case []string:
		return maskStrings(masker, typed)
	case []any:
		masked := append([]any(nil), typed...)
		for i := range masked {
			masked[i] = maskAnyStringValues(masker, masked[i])
		}
		return masked
	case map[string]string:
		masked := maps.Clone(typed)
		for key, val := range masked {
			masked[key] = masker.MaskString(val)
		}
		return masked
	case map[string]any:
		masked := maps.Clone(typed)
		for key, val := range masked {
			masked[key] = maskAnyStringValues(masker, val)
		}
		return masked
	default:
		return value
	}
}

func maskAnyStringMap(masker *masking.Masker, values map[string]any) map[string]any {
	masked := maps.Clone(values)
	for key, val := range masked {
		masked[key] = maskAnyStringValues(masker, val)
	}
	return masked
}
