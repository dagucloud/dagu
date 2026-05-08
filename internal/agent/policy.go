// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

const toolNameBash = "bash"

// KnownToolNames returns sorted names of all registered tools.
func KnownToolNames() []string {
	return RegisteredToolNames()
}

// IsKnownToolName reports whether the tool name is registered.
func IsKnownToolName(name string) bool {
	return IsRegisteredTool(name)
}

// IsBashToolName reports whether a tool name is the bash tool.
func IsBashToolName(name string) bool {
	return name == toolNameBash
}

func cloneBashRules(rules []BashRule) []BashRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]BashRule, len(rules))
	for i, r := range rules {
		out[i] = BashRule{
			Name:    r.Name,
			Pattern: r.Pattern,
			Action:  r.Action,
		}
		if r.Enabled != nil {
			v := *r.Enabled
			out[i].Enabled = &v
		}
	}
	return out
}

func cloneTools(tools map[string]bool) map[string]bool {
	if len(tools) == 0 {
		return map[string]bool{}
	}
	return maps.Clone(tools)
}

// ResolveToolPolicy applies defaults to a potentially partial policy config.
func ResolveToolPolicy(policy ToolPolicyConfig) ToolPolicyConfig {
	defaults := DefaultToolPolicy()
	resolved := ToolPolicyConfig{
		Tools: cloneTools(defaults.Tools),
		Bash: BashPolicyConfig{
			Rules:           cloneBashRules(defaults.Bash.Rules),
			DefaultBehavior: defaults.Bash.DefaultBehavior,
			DenyBehavior:    defaults.Bash.DenyBehavior,
		},
	}

	maps.Copy(resolved.Tools, policy.Tools)

	if policy.Bash.DefaultBehavior != "" {
		resolved.Bash.DefaultBehavior = policy.Bash.DefaultBehavior
	}
	if policy.Bash.DenyBehavior != "" {
		resolved.Bash.DenyBehavior = policy.Bash.DenyBehavior
	}
	// Keep defaults when nil so omitted JSON fields don't wipe rule set by accident.
	if policy.Bash.Rules != nil {
		resolved.Bash.Rules = cloneBashRules(policy.Bash.Rules)
	}

	return resolved
}

// IsToolEnabled reports whether the tool is enabled by the resolved policy.
func IsToolEnabled(policy ToolPolicyConfig, toolName string) bool {
	return IsToolEnabledResolved(ResolveToolPolicy(policy), toolName)
}

// IsToolEnabledResolved reports whether a tool is enabled in an already-resolved policy.
func IsToolEnabledResolved(resolved ToolPolicyConfig, toolName string) bool {
	enabled, ok := resolved.Tools[toolName]
	if !ok {
		// Unknown tools are denied by default.
		return false
	}
	return enabled
}

// ValidateToolPolicy validates policy settings before persistence.
func ValidateToolPolicy(policy ToolPolicyConfig) error {
	var errs []string

	// Skip unknown tools silently — saved configs may reference tools
	// that were removed between versions (e.g., web_search).
	for toolName := range policy.Tools {
		if !IsKnownToolName(toolName) {
			delete(policy.Tools, toolName)
		}
	}

	resolved := ResolveToolPolicy(policy)

	if resolved.Bash.DefaultBehavior != BashDefaultBehaviorAllow && resolved.Bash.DefaultBehavior != BashDefaultBehaviorDeny {
		errs = append(errs, "bash.defaultBehavior must be one of: allow, deny")
	}
	if resolved.Bash.DenyBehavior != BashDenyBehaviorAskUser && resolved.Bash.DenyBehavior != BashDenyBehaviorBlock {
		errs = append(errs, "bash.denyBehavior must be one of: ask_user, block")
	}

	for i, rule := range resolved.Bash.Rules {
		if strings.TrimSpace(rule.Pattern) == "" {
			errs = append(errs, fmt.Sprintf("bash.rules[%d].pattern is required", i))
			continue
		}
		if rule.Action != BashRuleActionAllow && rule.Action != BashRuleActionDeny {
			errs = append(errs, fmt.Sprintf("bash.rules[%d].action must be one of: allow, deny", i))
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			errs = append(errs, fmt.Sprintf("bash.rules[%d].pattern invalid regex: %v", i, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid tool policy: %s", strings.Join(errs, "; "))
	}
	return nil
}

// BashPolicyDecision is the result of evaluating a bash command against policy.
type BashPolicyDecision struct {
	Allowed      bool
	DenyBehavior BashDenyBehavior
	Reason       string
	Command      string
	Segment      string
	RuleName     string
}

// ExtractBashCommand extracts the bash command string from tool input.
func ExtractBashCommand(input json.RawMessage) (string, error) {
	var args BashToolInput
	if err := decodeToolInput(input, &args); err != nil {
		return "", fmt.Errorf("failed to parse bash input: %w", err)
	}
	return strings.TrimSpace(args.Command), nil
}

func isRuleEnabled(rule BashRule) bool {
	return rule.Enabled == nil || *rule.Enabled
}

// EvaluateBashPolicy evaluates bash input against policy.
func EvaluateBashPolicy(policy ToolPolicyConfig, input json.RawMessage) (BashPolicyDecision, error) {
	return EvaluateBashPolicyResolved(ResolveToolPolicy(policy), input)
}

// EvaluateBashPolicyResolved evaluates bash input against an already-resolved policy.
func EvaluateBashPolicyResolved(resolved ToolPolicyConfig, input json.RawMessage) (BashPolicyDecision, error) {
	if !IsToolEnabledResolved(resolved, toolNameBash) {
		return BashPolicyDecision{
			Allowed:      false,
			DenyBehavior: BashDenyBehaviorBlock,
			Reason:       "bash tool is disabled by policy",
		}, nil
	}

	command, err := ExtractBashCommand(input)
	if err != nil {
		return BashPolicyDecision{}, err
	}
	if command == "" {
		// Let the tool return its own validation error.
		return BashPolicyDecision{Allowed: true}, nil
	}
	if hasUnsupportedShellConstructs(command) {
		return BashPolicyDecision{
			Allowed:      false,
			DenyBehavior: resolved.Bash.DenyBehavior,
			Reason:       "command denied: unsupported shell construct detected (`...`, $(), heredoc, or process substitution)",
			Command:      command,
		}, nil
	}

	compiledRules, err := compileEnabledBashRules(resolved.Bash.Rules)
	if err != nil {
		return BashPolicyDecision{}, err
	}

	segments := splitShellCommandSegments(command)
	if len(segments) == 0 {
		return BashPolicyDecision{Allowed: true}, nil
	}

	for _, segment := range segments {
		matched, ruleName, action := matchCompiledBashRule(compiledRules, segment)
		if matched {
			if action == BashRuleActionAllow {
				continue
			}
			return BashPolicyDecision{
				Allowed:      false,
				DenyBehavior: resolved.Bash.DenyBehavior,
				Reason:       "command segment denied by matching policy rule",
				Command:      command,
				Segment:      segment,
				RuleName:     ruleName,
			}, nil
		}
		if resolved.Bash.DefaultBehavior == BashDefaultBehaviorAllow {
			continue
		}
		return BashPolicyDecision{
			Allowed:      false,
			DenyBehavior: resolved.Bash.DenyBehavior,
			Reason:       "command segment denied by default policy (no matching allow rule)",
			Command:      command,
			Segment:      segment,
		}, nil
	}

	return BashPolicyDecision{Allowed: true, Command: command}, nil
}

type compiledBashRule struct {
	name   string
	action BashRuleAction
	re     *regexp.Regexp
}

func compileEnabledBashRules(rules []BashRule) ([]compiledBashRule, error) {
	out := make([]compiledBashRule, 0, len(rules))
	for i, rule := range rules {
		if !isRuleEnabled(rule) {
			continue
		}

		re, compileErr := regexp.Compile(rule.Pattern)
		if compileErr != nil {
			return nil, fmt.Errorf("invalid bash rule regex at index %d: %w", i, compileErr)
		}

		out = append(out, compiledBashRule{
			name:   cmp.Or(rule.Name, fmt.Sprintf("rule_%d", i)),
			action: rule.Action,
			re:     re,
		})
	}
	return out, nil
}

func matchCompiledBashRule(rules []compiledBashRule, segment string) (matched bool, ruleName string, action BashRuleAction) {
	for _, rule := range rules {
		if !rule.re.MatchString(segment) {
			continue
		}
		return true, rule.name, rule.action
	}
	return false, "", ""
}

// splitShellCommandSegments splits a shell command into executable segments while
// respecting quoted strings and escaped characters.
// Limitation: this is not a full shell parser and intentionally does not support
// advanced syntax such as backticks, subshell command substitution, or heredocs.
// EvaluateBashPolicy denies commands using those constructs.
func splitShellCommandSegments(command string) []string {
	command = strings.TrimSpace(strings.ReplaceAll(command, "\r\n", "\n"))
	if command == "" {
		return nil
	}

	var (
		segments           []string
		current            strings.Builder
		inSingle, inDouble bool
		escaped            bool
	)

	flush := func() {
		s := strings.TrimSpace(current.String())
		current.Reset()
		if s != "" {
			segments = append(segments, s)
		}
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}

		if ch == '\\' && !inSingle {
			current.WriteByte(ch)
			escaped = true
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(ch)
			continue
		}

		if !inSingle && !inDouble {
			switch ch {
			case ';', '\n':
				flush()
				continue
			case '|':
				flush()
				if i+1 < len(command) && command[i+1] == '|' {
					i++
				}
				continue
			case '&':
				flush()
				if i+1 < len(command) && command[i+1] == '&' {
					i++
				}
				continue
			}
		}

		current.WriteByte(ch)
	}

	flush()
	return segments
}

func hasUnsupportedShellConstructs(command string) bool {
	var (
		inSingle bool
		inDouble bool
		escaped  bool
	)

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle {
			continue
		}

		if ch == '`' {
			return true
		}
		if ch == '$' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}
		if !inDouble && ch == '<' && i+1 < len(command) && command[i+1] == '<' {
			return true
		}
		if ch == '<' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}
		if ch == '>' && i+1 < len(command) && command[i+1] == '(' {
			return true
		}
	}

	return false
}
