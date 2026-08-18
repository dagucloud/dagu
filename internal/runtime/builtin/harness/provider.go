// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

type builtinStdinMode uint8

const (
	builtinStdinPipe builtinStdinMode = iota
	builtinStdinFold
)

type providerDescriptor struct {
	name           string
	binary         string
	prefixArgs     []string
	promptMode     ir.HarnessPromptMode
	promptFlag     string
	promptPosition ir.HarnessPromptPosition
	defaultConfig  map[string]any
	stdinMode      builtinStdinMode
}

func (p *providerDescriptor) buildInvocation(flags map[string]any, prompt, script string) ([]string, io.Reader, error) {
	if p.stdinMode == builtinStdinFold {
		prompt = promptAndScript(prompt, script)
		script = ""
	}

	args := append([]string(nil), p.prefixArgs...)
	flagArgs := configToFlags(flags, nil)
	promptArgs := p.promptArgs(prompt)
	if promptArgs == nil {
		return nil, nil, fmt.Errorf("harness: unsupported prompt_mode %q for provider %q", p.promptMode, p.name)
	}
	if p.promptPosition == ir.HarnessPromptPositionAfterFlags {
		args = append(args, flagArgs...)
		args = append(args, promptArgs...)
	} else {
		args = append(args, promptArgs...)
		args = append(args, flagArgs...)
	}
	if script == "" {
		return args, nil, nil
	}
	return args, strings.NewReader(script), nil
}

func (p *providerDescriptor) promptArgs(prompt string) []string {
	switch p.promptMode {
	case ir.HarnessPromptModeArg:
		return []string{prompt}
	case ir.HarnessPromptModeFlag:
		return []string{p.promptFlag, prompt}
	default:
		return nil
	}
}

var builtinProviderCatalog = []providerDescriptor{
	{name: "claude", binary: "claude", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-p"},
	{name: "codex", binary: "codex", prefixArgs: []string{"exec"}, promptMode: ir.HarnessPromptModeArg, defaultConfig: map[string]any{"skip-git-repo-check": true}},
	{name: "copilot", binary: "copilot", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-p"},
	{name: "opencode", binary: "opencode", prefixArgs: []string{"run"}, promptMode: ir.HarnessPromptModeArg},
	{name: "pi", binary: "pi", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-p"},
	{name: "gemini", binary: "gemini", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-p"},
	{name: "cursor", binary: "cursor-agent", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-p", defaultConfig: map[string]any{"output-format": "text"}, stdinMode: builtinStdinFold},
	{name: "cline", binary: "cline", promptMode: ir.HarnessPromptModeArg, promptPosition: ir.HarnessPromptPositionAfterFlags},
	{name: "aider", binary: "aider", promptMode: ir.HarnessPromptModeFlag, promptFlag: "--message", stdinMode: builtinStdinFold},
	{name: "qwen", binary: "qwen", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-p"},
	{name: "goose", binary: "goose", prefixArgs: []string{"run"}, promptMode: ir.HarnessPromptModeFlag, promptFlag: "--text", defaultConfig: map[string]any{"quiet": true}, stdinMode: builtinStdinFold},
	{name: "kiro", binary: "kiro-cli", prefixArgs: []string{"chat", "--no-interactive"}, promptMode: ir.HarnessPromptModeArg},
	{name: "droid", binary: "droid", prefixArgs: []string{"exec"}, promptMode: ir.HarnessPromptModeArg, stdinMode: builtinStdinFold},
	{name: "amp", binary: "amp", promptMode: ir.HarnessPromptModeFlag, promptFlag: "-x"},
	{name: "deepseek", binary: "dsh", prefixArgs: []string{"--profile", "headless"}, promptMode: ir.HarnessPromptModeArg, promptPosition: ir.HarnessPromptPositionAfterFlags, stdinMode: builtinStdinFold},
}

var providers = map[string]*providerDescriptor{}

func init() {
	for i := range builtinProviderCatalog {
		registerProvider(&builtinProviderCatalog[i])
	}
}

func registerProvider(p *providerDescriptor) {
	name := p.name
	if _, exists := providers[name]; exists {
		panic(fmt.Sprintf("harness: duplicate provider registration %q", name))
	}
	providers[name] = p
}

func getProvider(name string) (*providerDescriptor, error) {
	p, ok := providers[name]
	if !ok {
		names := make([]string, 0, len(providers))
		for k := range providers {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("harness: unknown provider %q; registered: %v", name, names)
	}
	return p, nil
}
