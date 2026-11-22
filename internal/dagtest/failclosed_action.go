// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// actionNames names the action of each executor type that serves one action.
// The loader keeps only the executor type, so findings name the action the
// author wrote from it.
var actionNames = map[string]string{
	"browser":                  "browser.run",
	"chat":                     "chat.completion",
	"clickhouse":               "clickhouse.query",
	"computer":                 "computer.run",
	"container":                "container.run",
	"decision":                 "decision.evaluate",
	"docker":                   "docker.run",
	"harness":                  "harness.run",
	"http":                     "http.request",
	"jq":                       "jq.filter",
	"js":                       "js.run",
	"k8s":                      "k8s.run",
	"kubernetes":               "kubernetes.run",
	"log":                      "log.write",
	"mail":                     "mail.send",
	"mcp":                      "mcp.call",
	"noop":                     "noop",
	"postgres":                 "postgres.query",
	"sqlite":                   "sqlite.query",
	"ssh":                      "ssh.run",
	"template":                 "template.render",
	ir.ExecutorTypeDAG:         "dag.run",
	ir.ExecutorTypeDAGEnqueue:  "dag.enqueue",
	ir.ExecutorTypeParallel:    "dag.run",
	ir.ExecutorTypeRouter:      "router.route",
	ir.ExecutorTypeSubworkflow: "dag.run",
}

// operationExecutors lists the executor types whose action name ends with
// the operation in the step's command, as in file.copy.
var operationExecutors = map[string]struct{}{
	"a2a":                  {},
	"archive":              {},
	"artifact":             {},
	"data":                 {},
	"file":                 {},
	"git":                  {},
	"mail":                 {},
	"s3":                   {},
	"state":                {},
	"wait":                 {},
	ir.ExecutorTypeOutputs: {},
	ir.ExecutorTypeXlsx:    {},
}

// describeAction names a step's action for a finding, such as
// "action ssh.run" or "command" for a run step.
func describeAction(step ir.Step) string {
	var what string
	switch step.ExecutorConfig.Type {
	case "", "command", "shell":
		what = "command"
	default:
		what = "action " + actionName(step)
	}
	if step.Nodes != nil {
		what += " on nodes"
	}
	return what
}

func actionName(step ir.Step) string {
	execType := step.ExecutorConfig.Type
	cfg := step.ExecutorConfig.Config
	if custom, _ := step.ExecutorConfig.Metadata["custom_type"].(string); custom != "" {
		return custom
	}
	switch execType {
	case ir.ExecutorTypeAction:
		if ref, _ := cfg["ref"].(string); ref != "" {
			return ref
		}
	case "redis":
		if command, _ := cfg["command"].(string); command != "" {
			return "redis." + strings.ToLower(command)
		}
	case "sftp":
		if direction, _ := cfg["direction"].(string); direction != "" {
			return "sftp." + direction
		}
	case "postgres", "sqlite":
		if _, ok := cfg["import"]; ok {
			return execType + ".import"
		}
	}
	if _, ok := operationExecutors[execType]; ok && len(step.Commands) > 0 {
		return execType + "." + step.Commands[0].Command
	}
	if name, ok := actionNames[execType]; ok {
		return name
	}
	return execType
}
