// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { BuiltinToolName, ToolCategory } from './model';

/**
 * What a pick of the Tools dock adds: an entry for one category of the tool
 * map (`entry` is its plain YAML value), or one built-in tool of an agent
 * step.
 */
export type ToolPick =
  | { category: ToolCategory; entry: unknown }
  | { category: 'builtin'; tool: BuiltinToolName };

/** The plain YAML value of a new inline MCP entry, with its URL. The user fills it in. */
export const NEW_MCP_ENTRY = { name: '', url: '', allow: [] };

/**
 * The plain YAML value of a MCP entry that names a server entry of the AI page
 * (Spec 095): no url, and the key allow that the user fills in.
 */
export function namedMcpEntry(name: string): {
  name: string;
  allow: string[];
} {
  return { name, allow: [] };
}
