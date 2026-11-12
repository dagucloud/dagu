// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  ActionCatalogEntry,
  EditorCustomActionHint,
  JSONSchema,
} from '../contracts';
import { builtinActionNames, readActionRules } from './actionRules';
import { actionTemplate } from './actionTemplate';
import { actionDescription, actionNamespace, actionTitle } from './actionText';
import { getActionWithSchema } from './withSchema';

/** The core entry for a shell command step (`run:`). */
export const RUN_ENTRY: ActionCatalogEntry = {
  name: 'run',
  namespace: 'core',
  title: 'Run command',
  description: 'Run a shell command or script.',
  source: 'core',
  // A command that passes the validation: an empty one is an error.
  template: { run: 'echo "TODO"' },
};

/**
 * Names that represent an open name pattern of the schema. Redis takes any
 * `redis.<command>` name; the catalog offers the two common ones.
 */
const PATTERN_EXAMPLES = ['redis.get', 'redis.set'];

function builtinNames(schema: JSONSchema): string[] {
  const names = new Set(builtinActionNames(schema));
  const patterns = readActionRules(schema).flatMap((rule) =>
    rule.pattern ? [rule.pattern] : []
  );
  for (const example of PATTERN_EXAMPLES) {
    if (patterns.some((pattern) => pattern.test(example))) {
      names.add(example);
    }
  }
  return [...names].sort();
}

/**
 * Builds the action catalog: the core `run` entry, one entry for each
 * builtin action of the schema (sorted by name), then the custom actions
 * (sorted by name). A custom action with a builtin name is left out.
 */
export function buildActionCatalog(
  schema: JSONSchema,
  customActions: EditorCustomActionHint[]
): ActionCatalogEntry[] {
  const entries: ActionCatalogEntry[] = [RUN_ENTRY];
  const taken = new Set<string>([RUN_ENTRY.name]);

  for (const name of builtinNames(schema)) {
    const withSchema = getActionWithSchema(schema, name, []);
    entries.push({
      name,
      namespace: actionNamespace(name),
      title: actionTitle(name),
      description: actionDescription(withSchema?.description),
      source: 'builtin',
      template: actionTemplate(name, withSchema),
    });
    taken.add(name);
  }

  const custom = [...customActions]
    .map((hint) => ({ ...hint, name: hint.name.trim() }))
    .filter((hint) => hint.name && !taken.has(hint.name))
    .sort((a, b) => a.name.localeCompare(b.name));
  for (const hint of custom) {
    if (taken.has(hint.name)) {
      continue;
    }
    taken.add(hint.name);
    entries.push({
      name: hint.name,
      namespace: 'custom',
      title: actionTitle(hint.name),
      description: hint.description,
      source: 'custom',
      template: actionTemplate(
        hint.name,
        getActionWithSchema(schema, hint.name, [hint]),
        true
      ),
    });
  }
  return entries;
}
