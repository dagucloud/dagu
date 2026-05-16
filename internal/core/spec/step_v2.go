// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

var v2LegacyExecutionFields = map[string]struct{}{
	"agent":          {},
	"call":           {},
	"command":        {},
	"config":         {},
	"exec":           {},
	"llm":            {},
	"messages":       {},
	"params":         {},
	"routes":         {},
	"script":         {},
	"shell":          {},
	"shell_args":     {},
	"shell_packages": {},
	"type":           {},
	"value":          {},
}

var v2RunWithFields = map[string]struct{}{
	"shell":          {},
	"shell_args":     {},
	"shell_packages": {},
}

type actionNormalizer func(normalized map[string]any, with map[string]any) error

var builtinActionNormalizers = map[string]actionNormalizer{
	"agent.run":       normalizeAgentAction,
	"artifact.list":   operationAction("artifact", "list"),
	"artifact.read":   operationAction("artifact", "read"),
	"artifact.write":  operationAction("artifact", "write"),
	"archive.create":  operationAction("archive", "create"),
	"archive.extract": operationAction("archive", "extract"),
	"archive.list":    operationAction("archive", "list"),
	"chat.completion": normalizeChatAction,
	"container.run":   optionalCommandAction("container", "command"),
	"dag.run":         normalizeDagRunAction,
	"data.convert":    operationAction("data", "convert"),
	"data.pick":       operationAction("data", "pick"),
	"docker.run":      optionalCommandAction("docker", "command"),
	"duckdb.query":    commandAction("duckdb", "query"),
	"duckdb.import":   importAction("duckdb"),
	"exec":            normalizeExecAction,
	"file.copy":       operationAction("file", "copy"),
	"file.delete":     operationAction("file", "delete"),
	"file.list":       operationAction("file", "list"),
	"file.mkdir":      operationAction("file", "mkdir"),
	"file.move":       operationAction("file", "move"),
	"file.read":       operationAction("file", "read"),
	"file.stat":       operationAction("file", "stat"),
	"file.write":      operationAction("file", "write"),
	"git.checkout":    operationAction("git", "checkout"),
	"harness.run":     normalizeHarnessRunAction,
	"http.request":    normalizeHTTPRequestAction,
	"jq.filter":       normalizeJQFilterAction,
	"k8s.run":         optionalCommandAction("k8s", "command"),
	"kubernetes.run":  optionalCommandAction("kubernetes", "command"),
	"log.write":       normalizeLogAction,
	"mail.send":       typedAction("mail"),
	"noop":            normalizeNoopAction,
	"postgres.query":  commandAction("postgres", "query"),
	"postgres.import": importAction("postgres"),
	"router.route":    normalizeRouterAction,
	"s3.delete":       operationAction("s3", "delete"),
	"s3.download":     operationAction("s3", "download"),
	"s3.list":         operationAction("s3", "list"),
	"s3.upload":       operationAction("s3", "upload"),
	"sftp.download":   directionAction("sftp", "download"),
	"sftp.upload":     directionAction("sftp", "upload"),
	"sqlite.query":    commandAction("sqlite", "query"),
	"sqlite.import":   importAction("sqlite"),
	"ssh.run":         commandAction("ssh", "command"),
	"template.render": normalizeTemplateAction,
	"wait.duration":   operationAction("wait", "duration"),
	"wait.file":       operationAction("wait", "file"),
	"wait.http":       operationAction("wait", "http"),
	"wait.until":      operationAction("wait", "until"),
}

// BuiltinActionNames returns the currently accepted built-in action names in
// sorted order. Redis operations are intentionally exposed as a pattern because
// they normalize dynamically from any redis.<operation> action.
func BuiltinActionNames() []string {
	names := make([]string, 0, len(builtinActionNormalizers)+1)
	for name := range builtinActionNormalizers {
		names = append(names, name)
	}
	names = append(names, "redis.<operation>")
	sort.Strings(names)
	return names
}

func normalizeStepExecutionRaw(raw map[string]any, registry *customStepTypeRegistry) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}

	_, hasRun := raw["run"]
	_, hasAction := raw["action"]
	if !hasRun && !hasAction {
		return raw, nil
	}
	if hasRun && hasAction {
		return nil, core.NewValidationError("action", raw["action"], fmt.Errorf("run cannot be used together with action"))
	}

	normalized := cloneMap(raw)
	if hasRun {
		if err := normalizeRunStep(normalized, raw); err != nil {
			return nil, err
		}
		return normalized, nil
	}

	if err := normalizeActionStep(normalized, raw, registry); err != nil {
		return nil, err
	}
	return normalized, nil
}

func normalizeRunStep(normalized, raw map[string]any) error {
	for field := range v2LegacyExecutionFields {
		if _, exists := raw[field]; exists {
			return core.NewValidationError("run", raw["run"], fmt.Errorf("run cannot be used together with %s", field))
		}
	}

	runValue := raw["run"]
	switch runValue.(type) {
	case string, []any:
	default:
		return core.NewValidationError("run", runValue, fmt.Errorf("run must be a string or array"))
	}

	if withRaw, exists := raw["with"]; exists {
		with, ok := withRaw.(map[string]any)
		if !ok {
			return core.NewValidationError("with", withRaw, fmt.Errorf("with must be an object"))
		}
		for key, val := range with {
			if _, ok := v2RunWithFields[key]; !ok {
				return core.NewValidationError("with", withRaw, fmt.Errorf("run only supports shell, shell_args, and shell_packages in with; got %q", key))
			}
			normalized[key] = cloneAny(val)
		}
		delete(normalized, "with")
	}

	normalized["command"] = cloneAny(runValue)
	delete(normalized, "run")
	return nil
}

func normalizeActionStep(normalized, raw map[string]any, registry *customStepTypeRegistry) error {
	for field := range v2LegacyExecutionFields {
		if _, exists := raw[field]; exists {
			return core.NewValidationError("action", raw["action"], fmt.Errorf("action cannot be used together with %s", field))
		}
	}

	action, ok := raw["action"].(string)
	if !ok {
		return core.NewValidationError("action", raw["action"], fmt.Errorf("action must be a string"))
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return core.NewValidationError("action", raw["action"], fmt.Errorf("action is required"))
	}
	if strings.Contains(action, "@") {
		return core.NewValidationError("action", raw["action"], fmt.Errorf("versioned action references are reserved for the future action registry"))
	}

	if registry != nil {
		if customType, ok := registry.Lookup(action); ok && customType.Kind == customStepKindAction {
			normalized["type"] = action
			delete(normalized, "action")
			return nil
		}
	}

	with, err := actionWith(raw)
	if err != nil {
		return err
	}

	if isRegisteredExecutorTypeName(action) {
		return finishAction(normalized, action, with)
	}

	if normalizer, ok := builtinActionNormalizers[action]; ok {
		return normalizer(normalized, with)
	}
	if after, ok0 := strings.CutPrefix(action, "redis."); ok0 {
		return normalizeRedisAction(normalized, with, after)
	}
	return core.NewValidationError("action", raw["action"], fmt.Errorf("unknown action %q", action))
}

func actionWith(raw map[string]any) (map[string]any, error) {
	withRaw, exists := raw["with"]
	if !exists {
		return nil, nil
	}
	with, ok := withRaw.(map[string]any)
	if !ok {
		return nil, core.NewValidationError("with", withRaw, fmt.Errorf("with must be an object"))
	}
	return cloneMap(with), nil
}

func requireActionField(with map[string]any, field string) (any, error) {
	if with == nil {
		return nil, core.NewValidationError("with", nil, fmt.Errorf("with.%s is required", field))
	}
	value, exists := with[field]
	if !exists {
		return nil, core.NewValidationError("with", with, fmt.Errorf("with.%s is required", field))
	}
	return value, nil
}

func requireActionStringField(with map[string]any, field string) (string, error) {
	value, err := requireActionField(with, field)
	if err != nil {
		return "", err
	}
	str, ok := value.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return "", core.NewValidationError("with", with, fmt.Errorf("with.%s must be a non-empty string", field))
	}
	return strings.TrimSpace(str), nil
}

func finishAction(normalized map[string]any, executorType string, with map[string]any) error {
	normalized["type"] = executorType
	if len(with) == 0 {
		delete(normalized, "with")
	} else {
		normalized["with"] = with
	}
	delete(normalized, "action")
	return nil
}

func normalizeTypedAction(normalized map[string]any, executorType string, with map[string]any) error {
	return finishAction(normalized, executorType, with)
}

func typedAction(executorType string) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		return normalizeTypedAction(normalized, executorType, with)
	}
}

func normalizeHTTPRequestAction(normalized map[string]any, with map[string]any) error {
	if _, err := requireActionStringField(with, "method"); err != nil {
		return err
	}
	if _, err := requireActionStringField(with, "url"); err != nil {
		return err
	}
	return finishAction(normalized, "http", with)
}

func normalizeDagRunAction(normalized map[string]any, with map[string]any) error {
	dagName, err := requireActionStringField(with, "dag")
	if err != nil {
		return err
	}
	for key := range with {
		if key != "dag" && key != "params" {
			return core.NewValidationError("with", with, fmt.Errorf("dag.run does not support with.%s", key))
		}
	}
	normalized["call"] = dagName
	if params, ok := with["params"]; ok {
		normalized["params"] = cloneAny(params)
	}
	delete(normalized, "with")
	delete(normalized, "action")
	return nil
}

func normalizeExecAction(normalized map[string]any, with map[string]any) error {
	command, err := requireActionStringField(with, "command")
	if err != nil {
		return err
	}
	exec := map[string]any{"command": command}
	if args, ok := with["args"]; ok {
		exec["args"] = cloneAny(args)
	}
	for key := range with {
		if key != "command" && key != "args" {
			return core.NewValidationError("with", with, fmt.Errorf("exec does not support with.%s", key))
		}
	}
	normalized["exec"] = exec
	delete(normalized, "with")
	delete(normalized, "action")
	return nil
}

func normalizeCommandAction(normalized map[string]any, executorType string, with map[string]any, field string) error {
	value, err := requireActionField(with, field)
	if err != nil {
		return err
	}
	delete(with, field)
	normalized["command"] = cloneAny(value)
	return finishAction(normalized, executorType, with)
}

func commandAction(executorType, field string) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		return normalizeCommandAction(normalized, executorType, with, field)
	}
}

func normalizeOptionalCommandAction(normalized map[string]any, executorType string, with map[string]any, field string) error {
	if value, ok := with[field]; ok {
		delete(with, field)
		normalized["command"] = cloneAny(value)
	}
	return finishAction(normalized, executorType, with)
}

func optionalCommandAction(executorType, field string) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		return normalizeOptionalCommandAction(normalized, executorType, with, field)
	}
}

func normalizeHarnessRunAction(normalized map[string]any, with map[string]any) error {
	if err := normalizeCommandAction(normalized, "harness", with, "prompt"); err != nil {
		return err
	}
	cfg, _ := normalized["with"].(map[string]any)
	if stdin, ok := cfg["stdin"]; ok {
		text, ok := stdin.(string)
		if !ok {
			return core.NewValidationError("with.stdin", stdin, fmt.Errorf("with.stdin must be a string"))
		}
		normalized["script"] = text
		delete(cfg, "stdin")
		if len(cfg) == 0 {
			delete(normalized, "with")
		}
	}
	return nil
}

func normalizeImportAction(normalized map[string]any, executorType string, with map[string]any) error {
	if _, err := requireActionField(with, "import"); err != nil {
		return err
	}
	return finishAction(normalized, executorType, with)
}

func importAction(executorType string) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		return normalizeImportAction(normalized, executorType, with)
	}
}

func normalizeDirectionAction(normalized map[string]any, executorType string, with map[string]any, direction string) error {
	if with == nil {
		with = map[string]any{}
	}
	if existing, ok := with["direction"]; ok && existing != direction {
		return core.NewValidationError("with.direction", existing, fmt.Errorf("direction must be %q for this action", direction))
	}
	with["direction"] = direction
	return finishAction(normalized, executorType, with)
}

func directionAction(executorType, direction string) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		return normalizeDirectionAction(normalized, executorType, with, direction)
	}
}

func normalizeOperationAction(normalized map[string]any, executorType string, with map[string]any, operation string) error {
	normalized["command"] = operation
	return finishAction(normalized, executorType, with)
}

func operationAction(executorType, operation string) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		return normalizeOperationAction(normalized, executorType, with, operation)
	}
}

func normalizeTemplateAction(normalized map[string]any, with map[string]any) error {
	template, err := requireActionStringField(with, "template")
	if err != nil {
		return err
	}
	delete(with, "template")
	normalized["script"] = template
	return finishAction(normalized, "template", with)
}

func normalizeJQFilterAction(normalized map[string]any, with map[string]any) error {
	filter, err := requireActionField(with, "filter")
	if err != nil {
		return err
	}
	delete(with, "filter")
	normalized["command"] = cloneAny(filter)
	if data, ok := with["data"]; ok {
		if _, hasInput := with["input"]; hasInput {
			return core.NewValidationError("with", with, fmt.Errorf("jq.filter does not allow both with.data and with.input"))
		}
		script, err := stringifyActionData(data)
		if err != nil {
			return core.NewValidationError("with.data", data, err)
		}
		normalized["script"] = script
		delete(with, "data")
	}
	return finishAction(normalized, "jq", with)
}

func stringifyActionData(data any) (string, error) {
	switch val := data.(type) {
	case string:
		if strings.TrimSpace(val) == "" {
			return "", fmt.Errorf("with.data must not be empty")
		}
		return val, nil
	default:
		encoded, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("with.data must be JSON-serializable: %w", err)
		}
		return string(encoded), nil
	}
}

func normalizeLogAction(normalized map[string]any, with map[string]any) error {
	if _, err := requireActionStringField(with, "message"); err != nil {
		return err
	}
	return finishAction(normalized, "log", with)
}

func normalizeRouterAction(normalized map[string]any, with map[string]any) error {
	value, err := requireActionStringField(with, "value")
	if err != nil {
		return err
	}
	routes, err := requireActionField(with, "routes")
	if err != nil {
		return err
	}
	normalized["value"] = value
	normalized["routes"] = cloneAny(routes)
	delete(normalized, "with")
	normalized["type"] = "router"
	delete(normalized, "action")
	return nil
}

func normalizeChatAction(normalized map[string]any, with map[string]any) error {
	messages, ok, err := actionMessages(with)
	if err != nil {
		return err
	}
	if !ok {
		return core.NewValidationError("with", with, fmt.Errorf("chat.completion requires with.prompt or with.messages"))
	}
	normalized["messages"] = messages
	delete(with, "prompt")
	delete(with, "messages")
	if len(with) > 0 {
		normalized["llm"] = with
	}
	delete(normalized, "with")
	normalized["type"] = "chat"
	delete(normalized, "action")
	return nil
}

func normalizeAgentAction(normalized map[string]any, with map[string]any) error {
	messages, ok, err := actionMessages(with)
	if err != nil {
		return err
	}
	if !ok {
		task, err := requireActionStringField(with, "task")
		if err != nil {
			return core.NewValidationError("with", with, fmt.Errorf("agent.run requires with.task, with.prompt, or with.messages"))
		}
		messages = []any{map[string]any{"role": "user", "content": task}}
		delete(with, "task")
	}
	normalized["messages"] = messages
	delete(with, "prompt")
	delete(with, "messages")
	if len(with) > 0 {
		normalized["agent"] = with
	}
	delete(normalized, "with")
	normalized["type"] = "agent"
	delete(normalized, "action")
	return nil
}

func actionMessages(with map[string]any) ([]any, bool, error) {
	if with == nil {
		return nil, false, nil
	}
	if promptRaw, ok := with["prompt"]; ok {
		prompt, ok := promptRaw.(string)
		if !ok || strings.TrimSpace(prompt) == "" {
			return nil, false, core.NewValidationError("with.prompt", promptRaw, fmt.Errorf("with.prompt must be a non-empty string"))
		}
		return []any{map[string]any{"role": "user", "content": prompt}}, true, nil
	}
	if messagesRaw, ok := with["messages"]; ok {
		messages, ok := messagesRaw.([]any)
		if !ok || len(messages) == 0 {
			return nil, false, core.NewValidationError("with.messages", messagesRaw, fmt.Errorf("with.messages must be a non-empty array"))
		}
		return cloneAny(messages).([]any), true, nil
	}
	return nil, false, nil
}

func normalizeRedisAction(normalized map[string]any, with map[string]any, op string) error {
	op = strings.TrimSpace(op)
	if op == "" {
		return core.NewValidationError("action", normalized["action"], fmt.Errorf("redis action requires an operation name"))
	}
	if with == nil {
		with = map[string]any{}
	}
	if existing, ok := with["command"]; ok && !strings.EqualFold(fmt.Sprintf("%v", existing), op) {
		return core.NewValidationError("with.command", existing, fmt.Errorf("command must be %q for this action", strings.ToUpper(op)))
	}
	with["command"] = strings.ToUpper(op)
	return finishAction(normalized, "redis", with)
}

func normalizeNoopAction(normalized map[string]any, with map[string]any) error {
	if len(with) > 0 {
		return core.NewValidationError("with", with, fmt.Errorf("noop does not accept with"))
	}
	return finishAction(normalized, "noop", nil)
}

func isBuiltinActionName(name string) bool {
	name = strings.TrimSpace(name)
	if _, ok := builtinActionNormalizers[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "redis.") && strings.TrimPrefix(name, "redis.") != ""
}
