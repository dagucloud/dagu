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

const DELIMITER = /^---[ \t]*$/;

/** The line at `from` without its break, and where the next line starts. */
function lineAt(text: string, from: number): { line: string; next: number } {
  const end = text.indexOf('\n', from);
  if (end === -1) return { line: text.slice(from), next: text.length };
  const line = text.slice(from, end).replace(/\r$/, '');
  return { line, next: end + 1 };
}

function layout(text: string): Layout | string {
  const first = lineAt(text, 0);
  if (!DELIMITER.test(first.line)) {
    return 'The first line of the file must be ---.';
  }
  let pos = first.next;
  while (pos < text.length) {
    const { line, next } = lineAt(text, pos);
    if (DELIMITER.test(line)) {
      return {
        front: text.slice(first.next, pos),
        frontStart: first.next,
        frontEnd: pos,
        bodyStart: next,
      };
    }
    pos = next;
  }
  return 'The front matter has no closing --- line.';
}

function plainText(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

/** Reads the text of a SKILL.md. A text without a front matter is an error. */
export function readSkillForm(source: string): SkillFormResult {
  const found = layout(source);
  if (typeof found === 'string') return { ok: false, error: found };
  const doc = parseDocument(found.front);
  if (doc.errors.length > 0) {
    return { ok: false, error: doc.errors[0]!.message };
  }
  const values = doc.toJS() ?? {};
  if (typeof values !== 'object' || Array.isArray(values)) {
    return { ok: false, error: 'The front matter must be a map of keys.' };
  }
  const known: readonly string[] = SKILL_KEYS;
  return {
    ok: true,
    form: {
      name: plainText(values.name),
      description: plainText(values.description),
      body: source.slice(found.bodyStart),
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
  if (typeof found === 'string') return { ok: false, error: found };
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
  if (typeof found === 'string') return { ok: false, error: found };
  const head = source.slice(0, found.bodyStart);
  return { ok: true, text: (head.endsWith('\n') ? head : `${head}\n`) + body };
}
