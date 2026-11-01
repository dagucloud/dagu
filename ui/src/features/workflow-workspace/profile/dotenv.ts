// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { YamlToken } from '@/features/visual-editor/yamlTokenizer';
import { tokenizeYamlValue } from '@/features/visual-editor/yamlTokenizer';
import type { EffectiveEntry } from './runtimeProfile';

/** What a secret shows in place of its value; the API sends no value. */
export const DOTENV_MASK = '******';

/** The number of variables that a group shows before "Show more". */
export const DOTENV_PREVIEW_LINES = 8;

/**
 * The text of a value in a .env file. A value with a space, a quote, a `#`
 * or a line end is double quoted, with `\`, `"` and line ends escaped.
 */
export function formatDotenvValue(value: string): string {
  if (!/[\s"'#\\]/.test(value)) return value;
  const escaped = value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r');
  return `"${escaped}"`;
}

/** The `NAME=value` text of a variable. A secret shows the mask. */
export function dotenvText(entry: EffectiveEntry): string {
  const value =
    entry.value === undefined ? DOTENV_MASK : formatDotenvValue(entry.value);
  return `${entry.key}=${value}`;
}

/** The coloured pieces of the value of a variable. */
export function dotenvValueTokens(entry: EffectiveEntry): YamlToken[] {
  if (entry.value === undefined)
    return [{ kind: 'comment', text: DOTENV_MASK }];
  const text = formatDotenvValue(entry.value);
  if (text === '') return [];
  return tokenizeYamlValue(text);
}

/** The entries to show: all of them when expanded, else the first ones. */
export function visibleEntries<T>(
  entries: readonly T[],
  expanded: boolean,
  limit: number = DOTENV_PREVIEW_LINES
): { shown: readonly T[]; hidden: number } {
  if (expanded || entries.length <= limit) {
    return { shown: entries, hidden: 0 };
  }
  return { shown: entries.slice(0, limit), hidden: entries.length - limit };
}
