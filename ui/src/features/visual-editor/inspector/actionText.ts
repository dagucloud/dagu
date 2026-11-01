// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

const ACRONYMS: Record<string, string> = {
  api: 'API',
  csv: 'CSV',
  dag: 'DAG',
  http: 'HTTP',
  js: 'JS',
  json: 'JSON',
  k8s: 'K8s',
  llm: 'LLM',
  s3: 'S3',
  sftp: 'SFTP',
  sql: 'SQL',
  ssh: 'SSH',
  url: 'URL',
  xlsx: 'XLSX',
};

/** Namespace of an action: the text before the first dot. */
export function actionNamespace(name: string): string {
  const dot = name.indexOf('.');
  return dot > 0 ? name.slice(0, dot) : name;
}

/**
 * Readable title of an action name: words split at dots, underscores and
 * hyphens, known acronyms in upper case, the first word capitalized.
 * `git.worktree.add` gives "Git worktree add".
 */
export function actionTitle(name: string): string {
  const words = name.split(/[._-]+/).filter(Boolean);
  return words
    .map((word, index) => {
      const lower = word.toLowerCase();
      // Only an own key is an acronym: `constructor` is a word too.
      if (Object.prototype.hasOwnProperty.call(ACRONYMS, lower)) {
        return ACRONYMS[lower]!;
      }
      return index === 0
        ? lower.charAt(0).toUpperCase() + lower.slice(1)
        : lower;
    })
    .join(' ');
}

const GENERIC_PREFIX = /^Configuration for action:\s*\S.*?\.(?=\s|$)\s*/;

/**
 * Description of an action from the description of its `with` schema.
 * The generic "Configuration for action: X." sentence is removed; gives
 * undefined when nothing remains.
 */
export function actionDescription(
  withDescription: unknown
): string | undefined {
  if (typeof withDescription !== 'string') {
    return undefined;
  }
  const text = withDescription.replace(GENERIC_PREFIX, '').trim();
  return text || undefined;
}
