// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export type ReferenceTokenKind =
  | 'canonical'
  | 'legacy'
  | 'variable'
  | 'unsupported';
export type ReferenceNamespace =
  | 'params'
  | 'consts'
  | 'env'
  | 'steps'
  | 'context'
  | 'foreach';

/** A "${...}" token in a text, with its range. */
export interface ReferenceToken {
  from: number; // UTF-16 index of '$' (inclusive)
  to: number; // index just after '}' (exclusive)
  text: string; // the whole "${...}"
  path: string; // the text between "${" and "}"
  kind: ReferenceTokenKind;
  namespace?: ReferenceNamespace; // set for canonical tokens only
}

const NAME = /^[A-Za-z][A-Za-z0-9_]*$/;
const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
// A field of a foreach item is a data key, such as a workbook header: a
// letter of any script or '_', then letters, marks, digits or '_'.
const ITEM_FIELD = /^[\p{L}_][\p{L}\p{M}\p{Nd}_]*$/u;
const RESERVED = new Set<string>([
  'params',
  'consts',
  'env',
  'steps',
  'context',
  'foreach',
]);

const unsupported = { kind: 'unsupported' as const };

/** Kind and namespace of the text between "${" and "}". */
export function classifyReferencePath(path: string): {
  kind: ReferenceTokenKind;
  namespace?: ReferenceNamespace;
} {
  if (ENV_NAME.test(path)) return { kind: 'variable' };
  const [head = '', ...rest] = path.split('.');
  if (RESERVED.has(head)) {
    const namespace = head as ReferenceNamespace;
    return isCanonicalPath(namespace, rest)
      ? { kind: 'canonical', namespace }
      : unsupported;
  }
  return isLegacyPath(head, rest) ? { kind: 'legacy' } : unsupported;
}

// `rest` is the path after the namespace.
function isCanonicalPath(
  namespace: ReferenceNamespace,
  rest: string[]
): boolean {
  const [a = '', b = '', c = ''] = rest;
  switch (namespace) {
    case 'params':
    case 'consts':
      return rest.length === 1 && NAME.test(a);
    case 'env':
      return rest.length === 1 && ENV_NAME.test(a);
    case 'steps':
      return (
        rest.length === 3 && NAME.test(a) && b === 'outputs' && NAME.test(c)
      );
    case 'context':
      return rest.length === 2 && NAME.test(a) && NAME.test(b);
    case 'foreach':
      return (
        (rest.length === 1 && NAME.test(a)) ||
        (rest.length === 2 && NAME.test(a) && ITEM_FIELD.test(b))
      );
  }
}

// Older dotted forms: "<id>.stdout", "<id>.outputs.<name>" and "<VAR>.<json.path>".
// All of them are an env-style first segment and non-empty segments after it.
function isLegacyPath(head: string, rest: string[]): boolean {
  return (
    ENV_NAME.test(head) &&
    rest.length > 0 &&
    rest.every((part) => /^[^\s.{}$]+$/.test(part))
  );
}

/** True when the '$' at dollarIndex follows an odd run of backslashes. */
export function isEscapedDollar(text: string, dollarIndex: number): boolean {
  let run = 0;
  for (let j = dollarIndex - 1; j >= 0 && text[j] === '\\'; j--) run++;
  return run % 2 === 1;
}

/**
 * Find all unescaped "${...}" tokens in order. A token ends at the first '}'.
 * Its content has no '{', no '}' and no line break. A "${" without a valid end is skipped.
 * An escaped token (odd backslash run before '$') is skipped.
 */
export function findReferenceTokens(text: string): ReferenceToken[] {
  const tokens: ReferenceToken[] = [];
  let i = text.indexOf('${');
  while (i !== -1) {
    const end = isEscapedDollar(text, i) ? -1 : tokenEnd(text, i + 2);
    if (end === -1) {
      i = text.indexOf('${', i + 1);
      continue;
    }
    const path = text.slice(i + 2, end);
    tokens.push({
      from: i,
      to: end + 1,
      text: text.slice(i, end + 1),
      path,
      ...classifyReferencePath(path),
    });
    i = text.indexOf('${', end + 1);
  }
  return tokens;
}

// Index of the '}' that closes a token whose content starts at `start`, or -1.
function tokenEnd(text: string, start: number): number {
  for (let j = start; j < text.length; j++) {
    const c = text[j];
    if (c === '}') return j;
    if (c === '{' || c === '\n' || c === '\r') return -1;
  }
  return -1;
}

/**
 * The unclosed, unescaped "${" that the caret is inside, or a lone '$' just before the caret.
 * from = index of '$'; query = text between "${" (or "$") and the caret. null otherwise.
 */
export function findOpenReference(
  text: string,
  caret: number
): { from: number; query: string } | null {
  if (caret < 1 || caret > text.length) return null;
  const lineStart = text.lastIndexOf('\n', caret - 1) + 1;
  const open = caret >= 2 ? text.lastIndexOf('${', caret - 2) : -1;
  if (open >= lineStart) {
    const query = text.slice(open + 2, caret);
    if (
      !isEscapedDollar(text, open) &&
      /^[\p{L}\p{M}\p{Nd}_.]*$/u.test(query)
    ) {
      return { from: open, query };
    }
  }
  const lone = caret - 1;
  if (text[lone] === '$' && lone >= lineStart && !isEscapedDollar(text, lone)) {
    return { from: lone, query: '' };
  }
  return null;
}

/** Replace text[from, to) with token. caret = from + token.length. */
export function insertReference(
  text: string,
  from: number,
  to: number,
  token: string
): { text: string; caret: number } {
  return {
    text: text.slice(0, from) + token + text.slice(to),
    caret: from + token.length,
  };
}

/** The token that contains the offset (from <= offset < to), or null. */
export function referenceTokenAt(
  text: string,
  offset: number
): ReferenceToken | null {
  return (
    findReferenceTokens(text).find((t) => t.from <= offset && offset < t.to) ??
    null
  );
}
