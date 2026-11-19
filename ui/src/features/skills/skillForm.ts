// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, parseDocument } from 'yaml';

/**
 * The form of a skill reads and writes the text of SKILL.md. The text stays
 * the one source. The file is a front matter between two `---` lines and a
 * Markdown body. A change of the front matter edits one key and keeps the
 * rest. A change of the body replaces the text after the second `---` line,
 * byte for byte: blank lines at the start stay.
 */

/**
 * The keys of the front matter that the Agent Skills format accepts. The form
 * edits `name` and `description`. The other keys stay as written.
 */
export const SKILL_KEYS = [
  'name',
  'description',
  'license',
  'compatibility',
  'metadata',
  'allowed-tools',
] as const;

/** The keys that the form edits. */
export type SkillFormKey = 'name' | 'description';

export interface SkillForm {
  name: string;
  description: string;
  /** The text after the closing `---` line, as written. */
  body: string;
  /** The keys that the format does not allow: the server refuses the skill. */
  unknown: string[];
}

export type SkillFormResult =
  | { ok: true; form: SkillForm }
  | { ok: false; error: string };

export type SkillEditOutcome =
  | { ok: true; text: string }
  | { ok: false; error: string };

interface Layout {
  /** The text between the two `---` lines. */
  front: string;
  /** Where the front matter starts and ends in the text. */
  frontStart: number;
  frontEnd: number;
  /** Where the body starts: after the closing `---` line and its break. */
  bodyStart: number;
}

/** The rules of the server that a front matter breaks before its keys are read. */
type FrontMatterRule =
  | 'front-matter-start'
  | 'front-matter-end'
  | 'front-matter-yaml'
  | 'front-matter-duplicate-key';

interface FrontMatterProblem {
  rule: FrontMatterRule;
  error: string;
}

/** The front matter values and the body of a SKILL.md, as the server reads them. */
type SkillFileParts =
  | { ok: true; values: Record<string, unknown>; body: string }
  | ({ ok: false } & FrontMatterProblem);

// The server compares a closing line without its trailing spaces and tabs.
const CLOSING = /^---[ \t]*$/;

/** The line at `from` without its break, and where the next line starts. */
function lineAt(text: string, from: number): { line: string; next: number } {
  const end = text.indexOf('\n', from);
  if (end === -1) return { line: text.slice(from), next: text.length };
  const line = text.slice(from, end).replace(/\r$/, '');
  return { line, next: end + 1 };
}

// The split of the server: the text must start with a line that is exactly
// ---, and the front matter ends at the next line that is --- once its
// trailing spaces and tabs are cut.
function layout(text: string): Layout | FrontMatterProblem {
  const first = lineAt(text, 0);
  if (first.line !== '---' || !text.includes('\n')) {
    return {
      rule: 'front-matter-start',
      error: 'The first line of the file must be ---.',
    };
  }
  let pos = first.next;
  while (pos < text.length) {
    const { line, next } = lineAt(text, pos);
    if (CLOSING.test(line)) {
      return {
        front: text.slice(first.next, pos),
        frontStart: first.next,
        frontEnd: pos,
        bodyStart: next,
      };
    }
    pos = next;
  }
  return {
    rule: 'front-matter-end',
    error: 'The front matter has no closing --- line.',
  };
}

function isProblem(
  found: Layout | FrontMatterProblem
): found is FrontMatterProblem {
  return 'rule' in found;
}

/**
 * The body as the server measures it: line breaks are LF and the blank lines
 * at the start are gone. The editor keeps the body as written.
 */
function serverBody(body: string): string {
  return body.replace(/\r\n/g, '\n').replace(/^\n+/, '');
}

function parseFront(
  source: string
):
  | { ok: true; layout: Layout; values: Record<string, unknown> }
  | ({ ok: false } & FrontMatterProblem) {
  const found = layout(source);
  if (isProblem(found)) return { ok: false, ...found };
  const doc = parseDocument(found.front);
  const failure = doc.errors[0];
  if (failure) {
    return {
      ok: false,
      rule:
        failure.code === 'DUPLICATE_KEY'
          ? 'front-matter-duplicate-key'
          : 'front-matter-yaml',
      error: failure.message,
    };
  }
  const values: unknown = doc.toJS() ?? {};
  if (typeof values !== 'object' || values === null || Array.isArray(values)) {
    return {
      ok: false,
      rule: 'front-matter-yaml',
      error: 'The front matter must be a map of keys.',
    };
  }
  return { ok: true, layout: found, values: values as Record<string, unknown> };
}

/**
 * Reads a SKILL.md the way the server checks it. The values keep their YAML
 * types, so a check can tell a missing key from a value that is not a string.
 */
export function readSkillParts(source: string): SkillFileParts {
  const parsed = parseFront(source);
  if (!parsed.ok) return parsed;
  return {
    ok: true,
    values: parsed.values,
    body: serverBody(source.slice(parsed.layout.bodyStart)),
  };
}

/**
 * The body of a SKILL.md as the server measures it, or the whole text when it
 * has no front matter.
 */
export function measuredBody(source: string): string {
  const found = layout(source);
  return isProblem(found) ? source : serverBody(source.slice(found.bodyStart));
}

function plainText(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/** Reads the text of a SKILL.md. A text without a front matter is an error. */
export function readSkillForm(source: string): SkillFormResult {
  const parsed = parseFront(source);
  if (!parsed.ok) return { ok: false, error: parsed.error };
  const { values } = parsed;
  const known: readonly string[] = SKILL_KEYS;
  return {
    ok: true,
    form: {
      name: plainText(values.name),
      description: plainText(values.description),
      body: source.slice(parsed.layout.bodyStart),
      unknown: Object.keys(values).filter((key) => !known.includes(key)),
    },
  };
}

/** Sets one key of the front matter. Other keys and comments stay. */
export function setSkillKey(
  source: string,
  key: SkillFormKey,
  value: string
): SkillEditOutcome {
  const found = layout(source);
  if (isProblem(found)) return { ok: false, error: found.error };
  const doc = parseDocument(found.front);
  if (doc.errors.length > 0) {
    return { ok: false, error: doc.errors[0]!.message };
  }
  if (doc.contents !== null && !isMap(doc.contents)) {
    return { ok: false, error: 'The front matter must be a map of keys.' };
  }
  doc.set(key, value);
  // A long description stays on one line.
  const front = doc.toString({ lineWidth: 0 });
  return {
    ok: true,
    text:
      source.slice(0, found.frontStart) + front + source.slice(found.frontEnd),
  };
}

/** Replaces the body. The front matter stays as it is. */
export function setSkillBody(source: string, body: string): SkillEditOutcome {
  const found = layout(source);
  if (isProblem(found)) return { ok: false, error: found.error };
  const head = source.slice(0, found.bodyStart);
  return { ok: true, text: (head.endsWith('\n') ? head : `${head}\n`) + body };
}
