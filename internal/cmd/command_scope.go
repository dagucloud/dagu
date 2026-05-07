// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

type commandScope string

const (
	commandScopeContextAware commandScope = "context-aware"
	commandScopeLocalOnly    commandScope = "local-only"
	commandScopeStatic       commandScope = "static"
)

func scopeForCommand(name string) commandScope {
	switch name {
	case "start", "enqueue", "status", "history", "stop", "retry", "restart", "dequeue", "agent":
		return commandScopeContextAware
	case "version", "schema":
		return commandScopeStatic
	case "context":
		return commandScopeStatic
	default:
		return commandScopeLocalOnly
	}
}
