// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SkillIssueSeverity, type components } from '@/api/v1/schema';
import { SKILL_NAME_MAX } from './skill';
import { SKILL_KEYS, measuredBody, readSkillParts } from './skillForm';

export type SkillIssue = components['schemas']['SkillIssue'];
export type SkillSummary = components['schemas']['SkillSummary'];
export type SkillDetail = components['schemas']['SkillDetail'];
export type SkillFile = components['schemas']['SkillFile'];

/** The file of the skill that the front matter and the body belong to. */
export const SKILL_FILE = 'SKILL.md';

/** The limits of the Agent Skills format that the editor shows. */
export const SKILL_LIMITS = {
  /** The recommended number of tokens of the body. */
  tokens: 5000,
  /** The recommended number of lines of the body. */
  lines: 500,
  /** The largest body, in bytes. */
  bodyBytes: 64 * 1024,
  descriptionMax: 1024,
  descriptionMin: 20,
  compatibilityMax: 500,
  licenseMax: 128,
} as const;

/** The state of a skill: an error makes the server refuse it. */
export type SkillState = 'valid' | 'warning' | 'error';

/**
 * The state of a skill. A skill with an error is refused, with or without
 * the `valid` flag.
 */
export function skillState(skill: {
  valid?: boolean;
  errors?: readonly SkillIssue[];
  warnings?: readonly SkillIssue[];
}): SkillState {
  if (skill.valid === false || (skill.errors?.length ?? 0) > 0) return 'error';
  return (skill.warnings?.length ?? 0) > 0 ? 'warning' : 'valid';
}

/**
 * The reason that a skill cannot be used, for a picker: the first error. It is
 * null for a skill without errors.
 */
export function skillUnusableReason(skill: {
  valid?: boolean;
  errors?: readonly SkillIssue[];
}): string | null {
  if (skillState(skill) !== 'error') return null;
  const first = skill.errors?.[0];
  return first ? first.message : null;
}

/** One issue as a line of text: where, what, and the rule. */
export function issueLine(issue: SkillIssue): string {
  const where = issue.file ? `${issue.file}: ` : '';
  return `${where}${issue.message} (${issue.rule})`;
}

export interface IssueGroup {
  /** The file of the issues. Empty for the folder. */
  file: string;
  errors: SkillIssue[];
  warnings: SkillIssue[];
}

/**
 * Groups the issues by file. The folder comes first, then SKILL.md, then the
 * other files by path. In a group, the errors and the warnings keep the
 * order that the server gave.
 */
export function groupIssues(issues: readonly SkillIssue[]): IssueGroup[] {
  const groups = new Map<string, IssueGroup>();
  for (const issue of issues) {
    const file = issue.file ?? '';
    let group = groups.get(file);
    if (!group) {
      group = { file, errors: [], warnings: [] };
      groups.set(file, group);
    }
    (issue.severity === SkillIssueSeverity.error
      ? group.errors
      : group.warnings
    ).push(issue);
  }
  const rank = (file: string) =>
    file === '' ? 0 : file === SKILL_FILE ? 1 : 2;
  return [...groups.values()].sort(
    (a, b) => rank(a.file) - rank(b.file) || a.file.localeCompare(b.file)
  );
}

/** The errors first, then the warnings, each in the order of the server. */
export function sortIssues(issues: readonly SkillIssue[]): SkillIssue[] {
  return [
    ...issues.filter((issue) => issue.severity === SkillIssueSeverity.error),
    ...issues.filter((issue) => issue.severity !== SkillIssueSeverity.error),
  ];
}

/** The issues of one file. */
export function issuesOf(
  issues: readonly SkillIssue[],
  file: string
): SkillIssue[] {
  return issues.filter((issue) => (issue.file ?? '') === file);
}

/** The worst state of the issues of one file, or null when it has none. */
export function fileIssueState(
  issues: readonly SkillIssue[],
  file: string
): 'error' | 'warning' | null {
  const own = issuesOf(issues, file);
  if (own.some((issue) => issue.severity === SkillIssueSeverity.error)) {
    return 'error';
  }
  return own.length > 0 ? 'warning' : null;
}

// --- the size of the body ---

const encoder = new TextEncoder();

/** Estimate of the tokens of `bytes` bytes of text: bytes / 3.7, rounded up. */
export function estimateTokens(bytes: number): number {
  return bytes <= 0 ? 0 : Math.ceil((bytes * 10) / 37);
}

/** The number of lines of a body, as the server counts them. */
export function countLines(body: string): number {
  if (body === '') return 0;
  const breaks = body.split('\n').length - 1;
  return body.endsWith('\n') ? breaks : breaks + 1;
}

export interface BodyStats {
  bytes: number;
  lines: number;
  tokens: number;
}

/**
 * The size of the Markdown body of the text of a SKILL.md, measured as the
 * server measures it.
 */
export function bodyStats(content: string): BodyStats {
  return statsOf(measuredBody(content));
}

function statsOf(body: string): BodyStats {
  const bytes = encoder.encode(body).length;
  return { bytes, lines: countLines(body), tokens: estimateTokens(bytes) };
}

export interface MeterBar {
  /** The value, and the limit that the marker shows. */
  value: number;
  limit: number;
  /** Where the bar ends, from 0 to 1, and where the marker is. */
  fill: number;
  marker: number;
  over: boolean;
}

// The bar is a little longer than the limit, so that the marker stays visible
// and an excess shows as a bar past the marker.
const METER_SCALE = 1.2;

/** The geometry of a bar for a value against a limit. */
export function meterBar(value: number, limit: number): MeterBar {
  return {
    value,
    limit,
    fill: Math.min(1, value / (limit * METER_SCALE)),
    marker: 1 / METER_SCALE,
    over: value > limit,
  };
}

/** The two bars of the body: the tokens and the lines. */
export function bodyMeter(stats: BodyStats): {
  tokens: MeterBar;
  lines: MeterBar;
  over: boolean;
} {
  const tokens = meterBar(stats.tokens, SKILL_LIMITS.tokens);
  const lines = meterBar(stats.lines, SKILL_LIMITS.lines);
  return { tokens, lines, over: tokens.over || lines.over };
}

// --- the rules of SKILL.md, checked on the draft ---

const XML_TAG = /<\/?[A-Za-z][^<>]*>/;

// One character of a name: a lower case letter or a digit of any script, or -.
const NAME_CHAR = /^[\p{Ll}\p{Nd}-]$/u;

function issue(
  severity: 'error' | 'warning',
  rule: string,
  message: string
): SkillIssue {
  return {
    severity:
      severity === 'error'
        ? SkillIssueSeverity.error
        : SkillIssueSeverity.warning,
    rule,
    file: SKILL_FILE,
    message,
  };
}

/** The number of characters of a text, as the server counts runes. */
function characters(text: string): number {
  return [...text].length;
}

/** The issues of a name: one for each rule that it breaks, as on the server. */
function nameIssues(name: string): SkillIssue[] {
  const found: SkillIssue[] = [];
  const count = characters(name);
  if (count < 1 || count > SKILL_NAME_MAX) {
    found.push(
      issue(
        'error',
        'name-length',
        `The name "${name}" has ${count} characters. It must have 1 to ${SKILL_NAME_MAX}.`
      )
    );
  }
  const bad = [...name].find((char) => !NAME_CHAR.test(char));
  if (bad !== undefined) {
    found.push(
      issue(
        'error',
        'name-charset',
        `The name "${name}" holds the character "${bad}". Only lower case letters, digits and - are allowed.`
      )
    );
  }
  if (name.startsWith('-') || name.endsWith('-')) {
    found.push(
      issue(
        'error',
        'name-hyphen-edge',
        `The name "${name}" starts or ends with a hyphen.`
      )
    );
  }
  if (name.includes('--')) {
    found.push(
      issue(
        'error',
        'name-hyphen-double',
        `The name "${name}" holds two hyphens in a row.`
      )
    );
  }
  return found;
}

/** Checks a key whose text must have 1 to `max` characters once trimmed. */
function boundedText(
  found: SkillIssue[],
  value: unknown,
  key: 'license' | 'compatibility',
  max: number
): void {
  if (value === undefined) return;
  if (typeof value !== 'string') {
    found.push(
      issue('error', key, `The front matter key ${key} must be a string.`)
    );
    return;
  }
  const count = characters(value.trim());
  if (count === 0 || count > max) {
    found.push(
      issue('error', key, `The ${key} must have 1 to ${max} characters.`)
    );
  }
}

function metadataIssues(found: SkillIssue[], value: unknown): void {
  if (value === undefined) return;
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    found.push(
      issue(
        'error',
        'metadata',
        'The front matter key metadata must be a map of strings to strings.'
      )
    );
    return;
  }
  const entries = value as Record<string, unknown>;
  for (const key of Object.keys(entries).sort()) {
    if (typeof entries[key] === 'string') continue;
    found.push(
      issue(
        'error',
        'metadata',
        `metadata.${key} must be a string. Quote the value.`
      )
    );
  }
}

/**
 * The rules of SKILL.md, checked in the browser while the user types. They
 * are the rules of the server, with its rule names, so that a draft shows the
 * state that a save gets. The server checks again on a save.
 */
export function checkSkillText(name: string, content: string): SkillIssue[] {
  const parsed = readSkillParts(content);
  if (!parsed.ok) return [issue('error', parsed.rule, parsed.error)];
  const { values, body } = parsed;
  const found: SkillIssue[] = [];

  const allowed: readonly string[] = SKILL_KEYS;
  for (const key of Object.keys(values).sort()) {
    if (allowed.includes(key)) continue;
    found.push(
      issue(
        'error',
        'front-matter-key',
        `The front matter key "${key}" is not allowed. The keys are ${allowed.join(', ')}. Put custom data in metadata.`
      )
    );
  }

  const named = values.name;
  if (named === undefined) {
    found.push(
      issue('error', 'name-missing', 'The front matter key name is required.')
    );
  } else if (typeof named !== 'string') {
    found.push(
      issue('error', 'name-type', 'The front matter key name must be a string.')
    );
  } else {
    if (named !== name) {
      found.push(
        issue(
          'error',
          'name-folder',
          `The name "${named}" must be the name of the folder, "${name}".`
        )
      );
    }
    found.push(...nameIssues(named));
  }

  const described = values.description;
  if (described === undefined) {
    found.push(
      issue(
        'error',
        'description-missing',
        'The front matter key description is required.'
      )
    );
  } else if (typeof described !== 'string') {
    found.push(
      issue(
        'error',
        'description-type',
        'The front matter key description must be a string.'
      )
    );
  } else {
    const description = described.split(/\s+/).filter(Boolean).join(' ');
    const count = characters(description);
    if (count === 0) {
      found.push(
        issue('error', 'description-missing', 'The description is empty.')
      );
    } else if (count > SKILL_LIMITS.descriptionMax) {
      found.push(
        issue(
          'error',
          'description-length',
          `The description has ${count} characters. The limit is ${SKILL_LIMITS.descriptionMax}.`
        )
      );
    }
    if (XML_TAG.test(description)) {
      found.push(
        issue('error', 'description-xml', 'The description holds an XML tag.')
      );
    }
    if (count > 0 && count < SKILL_LIMITS.descriptionMin) {
      found.push(
        issue(
          'warning',
          'description-short',
          `The description has ${count} characters. Say what the skill does and when to use it.`
        )
      );
    }
  }

  boundedText(found, values.license, 'license', SKILL_LIMITS.licenseMax);
  boundedText(
    found,
    values.compatibility,
    'compatibility',
    SKILL_LIMITS.compatibilityMax
  );

  const tools = values['allowed-tools'];
  if (tools !== undefined && typeof tools !== 'string') {
    found.push(
      issue(
        'error',
        'allowed-tools',
        'The front matter key allowed-tools must be a string of tool names separated by spaces.'
      )
    );
  } else if (typeof tools === 'string' && tools.trim() === '') {
    found.push(
      issue('error', 'allowed-tools', 'allowed-tools must not be empty.')
    );
  }

  metadataIssues(found, values.metadata);

  const stats = statsOf(body);
  if (body.trim() === '') {
    found.push(issue('error', 'body-empty', 'The Markdown body is empty.'));
  } else if (stats.bytes > SKILL_LIMITS.bodyBytes) {
    found.push(
      issue(
        'error',
        'body-too-large',
        `The body has ${stats.bytes} bytes. The limit is ${SKILL_LIMITS.bodyBytes}.`
      )
    );
  }
  if (stats.lines > SKILL_LIMITS.lines) {
    found.push(
      issue(
        'warning',
        'body-lines',
        `The body has ${stats.lines} lines. The format recommends ${SKILL_LIMITS.lines} or fewer. Move detail to references/.`
      )
    );
  }
  if (stats.tokens > SKILL_LIMITS.tokens) {
    found.push(
      issue(
        'warning',
        'body-tokens',
        `The body is about ${stats.tokens} tokens. The format recommends ${SKILL_LIMITS.tokens} or fewer. Move detail to references/.`
      )
    );
  }
  return found;
}

/**
 * The rules that the text of SKILL.md decides, which checkSkillText replaces
 * on a draft. A draft is always UTF-8, and its save creates a missing
 * SKILL.md.
 */
const DRAFT_RULES: ReadonlySet<string> = new Set([
  'front-matter-start',
  'front-matter-end',
  'front-matter-yaml',
  'front-matter-key',
  'front-matter-duplicate-key',
  'name-missing',
  'name-type',
  'name-length',
  'name-charset',
  'name-hyphen-edge',
  'name-hyphen-double',
  'name-folder',
  'description-missing',
  'description-type',
  'description-length',
  'description-xml',
  'description-short',
  'license',
  'compatibility',
  'metadata',
  'allowed-tools',
  'body-empty',
  'body-too-large',
  'body-lines',
  'body-tokens',
  'not-utf8',
  'skill-md-missing',
]);

/**
 * The issues to show. While the draft of SKILL.md differs from the saved
 * text, the checks of the draft replace the server issues of its text. The
 * other issues of SKILL.md, such as a link, stay until the server answers.
 */
export function visibleIssues(
  server: readonly SkillIssue[],
  draft: { name: string; text: string } | null
): SkillIssue[] {
  if (draft === null) return sortIssues(server);
  return sortIssues([
    ...checkSkillText(draft.name, draft.text),
    ...server.filter(
      (item) => item.file !== SKILL_FILE || !DRAFT_RULES.has(item.rule)
    ),
  ]);
}
