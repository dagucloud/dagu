// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

func (a *Agent) maskStatusSecrets(status *ir.DAGRunStatus) {
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

// newStatusSecretMasker returns a masker for run status. Stored outputs are
// JSON text, so each secret is also masked in the escaped forms JSON encoding
// gives it.
func newStatusSecretMasker(secretEnvs []string) *masking.Masker {
	if len(secretEnvs) == 0 {
		return nil
	}
	secrets := make([]string, 0, len(secretEnvs))
	for _, env := range secretEnvs {
		secrets = append(secrets, env)
		name, value, ok := strings.Cut(env, "=")
		if !ok || value == "" {
			continue
		}
		for _, escapeHTML := range []bool{true, false} {
			if escaped := jsonStringBody(value, escapeHTML); escaped != value {
				secrets = append(secrets, name+"="+escaped)
			}
		}
	}
	return masking.NewMasker(masking.SourcedEnvVars{Secrets: secrets})
}

// jsonStringBody returns value encoded as a JSON string, without the quotes.
func jsonStringBody(value string, escapeHTML bool) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(escapeHTML)
	if err := encoder.Encode(value); err != nil {
		return value
	}
	encoded := strings.TrimSuffix(buf.String(), "\n")
	return encoded[1 : len(encoded)-1]
}

func maskNodeSecrets(masker *masking.Masker, node *ir.Node) {
	if node == nil {
		return
	}
	node.Step = maskStepSecrets(masker, node.Step)
	node.Error = masker.MaskString(node.Error)
	node.StatusDetails = maskNodeStatusDetails(masker, node.StatusDetails)
	var masked maskedOutputs
	if prior := node.MaskedOutputs; prior != nil {
		masked.add(prior.Names, prior.All)
	}
	if node.OutputVariables != nil {
		node.OutputVariables = maskOutputVariables(masker, node.OutputVariables, &masked)
	}
	outputValue := maskStringPointer(masker, node.OutputValue)
	if outputValue != nil && *outputValue != *node.OutputValue {
		masked.add([]string{node.Step.Output}, false)
	}
	node.OutputValue = outputValue
	outputsValue := maskOutputDocument(masker, node.OutputsValue)
	masked.add(changedOutputNames(node.OutputsValue, outputsValue))
	node.OutputsValue = outputsValue
	stepOutputsValue := maskStepOutputs(masker, node)
	masked.add(changedOutputNames(node.StepOutputsValue, stepOutputsValue))
	node.StepOutputsValue = stepOutputsValue
	node.MaskedOutputs = masked.record()
	node.AgentSession = ir.MaskAgentSession(node.AgentSession, masker.MaskString)
}

// maskStepOutputs masks the outputs a step published. Human-task outputs are
// kept: they are operator input, stored as entered next to HumanTaskInput, and
// a resumed run reads them back from status.
func maskStepOutputs(masker *masking.Masker, node *ir.Node) *string {
	if node.Step.HumanTask != nil {
		return node.StepOutputsValue
	}
	return maskOutputDocument(masker, node.StepOutputsValue)
}

// maskOutputDocument masks a stored JSON output document. Plain replacement
// can split a JSON token, such as a secret matching a number; such a document
// is masked value by value instead, so a run that reuses it can still read it.
// It never keeps secret text that plain replacement masks.
func maskOutputDocument(masker *masking.Masker, value *string) *string {
	masked := maskStringPointer(masker, value)
	if masked == nil || *masked == *value || json.Valid([]byte(*masked)) || !json.Valid([]byte(*value)) {
		return masked
	}
	decoder := json.NewDecoder(strings.NewReader(*value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return masked
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(maskJSONValue(masker, decoded)); err != nil {
		return masked
	}
	document := strings.TrimSuffix(buf.String(), "\n")
	// A secret spanning JSON values is in no single decoded value, so
	// re-encoding writes it back out.
	if masker.MaskString(document) != document {
		return masked
	}
	return &document
}

// maskJSONValue masks secrets in decoded JSON. A number, boolean, or null
// holding a secret becomes the masked string.
func maskJSONValue(masker *masking.Masker, value any) any {
	switch typed := value.(type) {
	case string:
		return masker.MaskString(typed)
	case json.Number:
		return maskJSONLiteral(masker, typed.String(), typed)
	case bool:
		return maskJSONLiteral(masker, strconv.FormatBool(typed), typed)
	case nil:
		return maskJSONLiteral(masker, "null", nil)
	case []any:
		masked := make([]any, len(typed))
		for i, item := range typed {
			masked[i] = maskJSONValue(masker, item)
		}
		return masked
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, item := range typed {
			masked[masker.MaskString(key)] = maskJSONValue(masker, item)
		}
		return masked
	default:
		return value
	}
}

// maskJSONLiteral returns the masked text of a JSON literal holding a secret,
// and value otherwise.
func maskJSONLiteral(masker *masking.Masker, literal string, value any) any {
	if masked := masker.MaskString(literal); masked != literal {
		return masked
	}
	return value
}

func maskNodeStatusDetails(masker *masking.Masker, details []ir.NodeStatusDetail) []ir.NodeStatusDetail {
	if len(details) == 0 {
		return details
	}
	masked := append([]ir.NodeStatusDetail(nil), details...)
	for i := range masked {
		masked[i].Label = masker.MaskString(masked[i].Label)
	}
	return masked
}

// maskedOutputs collects the outputs of one node that masking changed.
type maskedOutputs struct {
	names []string
	all   bool
}

func (m *maskedOutputs) add(names []string, all bool) {
	m.names = append(m.names, names...)
	m.all = m.all || all
}

// record returns what to store on the node, or nil when masking changed no
// output.
func (m *maskedOutputs) record() *ir.MaskedOutputs {
	names := slices.DeleteFunc(m.names, func(name string) bool { return name == "" })
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) == 0 && !m.all {
		return nil
	}
	return &ir.MaskedOutputs{Names: names, All: m.all}
}

func maskOutputVariables(masker *masking.Masker, values *collections.SyncMap, masked *maskedOutputs) *collections.SyncMap {
	if values == nil {
		return nil
	}
	result := &collections.SyncMap{}
	values.Range(func(key, value any) bool {
		text, ok := value.(string)
		if !ok {
			result.Store(key, value)
			return true
		}
		maskedText := masker.MaskString(text)
		if maskedText != text {
			// The stored text is NAME=value, so the key is the output name.
			name, _ := key.(string)
			masked.add([]string{name}, false)
		}
		result.Store(key, maskedText)
		return true
	})
	return result
}

// changedOutputNames returns the top-level names of the output document whose
// text masking changed, and whether the names cannot be trusted: the document
// is not an object, or masking changed a key or the number of keys.
func changedOutputNames(before, after *string) ([]string, bool) {
	if before == nil || after == nil || *before == *after {
		return nil, false
	}
	var old, now map[string]json.RawMessage
	if json.Unmarshal([]byte(*before), &old) != nil || json.Unmarshal([]byte(*after), &now) != nil || len(old) != len(now) {
		return nil, true
	}
	var names []string
	for name, raw := range old {
		current, ok := now[name]
		if !ok {
			return nil, true
		}
		if !sameJSON(raw, current) {
			names = append(names, name)
		}
	}
	return names, false
}

// sameJSON reports whether two encoded values decode to the same value. The
// masking fallback re-encodes a whole document, which reorders and respaces
// values it did not mask.
func sameJSON(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	decode := func(raw json.RawMessage) (any, bool) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err == nil
	}
	left, okLeft := decode(a)
	right, okRight := decode(b)
	return okLeft && okRight && reflect.DeepEqual(left, right)
}

func maskStepSecrets(masker *masking.Masker, step ir.Step) ir.Step {
	step.Command = masker.MaskString(step.Command)
	step.CmdWithArgs = masker.MaskString(step.CmdWithArgs)
	step.CmdArgsSys = masker.MaskString(step.CmdArgsSys)
	step.ShellCmdArgs = masker.MaskString(step.ShellCmdArgs)
	step.Script = masker.MaskString(step.Script)
	step.Args = maskStrings(masker, step.Args)
	step.Env = maskStrings(masker, step.Env)
	if step.HumanTask != nil {
		humanTask := *step.HumanTask
		humanTask.Prompt = masker.MaskString(humanTask.Prompt)
		humanTask.Artifacts = maskStrings(masker, humanTask.Artifacts)
		step.HumanTask = &humanTask
	}

	if len(step.Commands) > 0 {
		commands := append([]ir.CommandEntry(nil), step.Commands...)
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
