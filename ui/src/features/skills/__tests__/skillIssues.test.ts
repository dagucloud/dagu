// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { SkillIssueSeverity } from '@/api/v1/schema';
import {
  bodyMeter,
  bodyStats,
  checkSkillText,
  countLines,
  estimateTokens,
  fileIssueState,
  groupIssues,
  issueLine,
  meterBar,
  skillState,
  skillUnusableReason,
  sortIssues,
  visibleIssues,
  type SkillIssue,
} from '../skillIssues';

const error = (rule: string, file?: string, message = rule): SkillIssue => ({
  severity: SkillIssueSeverity.error,
  rule,
  file,
  message,
});
const warning = (rule: string, file?: string, message = rule): SkillIssue => ({
  severity: SkillIssueSeverity.warning,
  rule,
  file,
  message,
});

const front = (name: string, description: string, extra = '') =>
  `---\nname: ${name}\ndescription: ${description}\n${extra}---\n`;

describe('skillState', () => {
  it('is error when the skill has an error or the valid flag is false', () => {
    expect(skillState({ valid: true, errors: [error('a')] })).toBe('error');
    expect(skillState({ valid: false, errors: [] })).toBe('error');
  });

  it('is warning with only warnings, and valid without issues', () => {
    expect(
      skillState({ valid: true, errors: [], warnings: [warning('w')] })
    ).toBe('warning');
    expect(skillState({ valid: true, errors: [], warnings: [] })).toBe('valid');
    expect(skillState({})).toBe('valid');
  });

  it('gives the first error as the reason a picker disables the skill', () => {
    expect(
      skillUnusableReason({ valid: false, errors: [error('a', 'x', 'bad')] })
    ).toBe('bad');
    expect(skillUnusableReason({ valid: true, errors: [] })).toBeNull();
    expect(skillUnusableReason({ valid: false, errors: [] })).toBeNull();
  });
});

describe('issue grouping', () => {
  const issues = [
    warning('body-lines', 'SKILL.md'),
    error('root-entry', 'README.md'),
    error('folder-link'),
    error('name-folder', 'SKILL.md'),
    warning('reference-depth', 'references/a/b.md'),
  ];

  it('groups by file: the folder, SKILL.md, then the other files by path', () => {
    const groups = groupIssues(issues);
    expect(groups.map((group) => group.file)).toEqual([
      '',
      'SKILL.md',
      'README.md',
      'references/a/b.md',
    ]);
    const skill = groups[1]!;
    expect(skill.errors.map((i) => i.rule)).toEqual(['name-folder']);
    expect(skill.warnings.map((i) => i.rule)).toEqual(['body-lines']);
  });

  it('puts the errors before the warnings', () => {
    expect(sortIssues(issues).map((i) => i.severity)).toEqual([
      'error',
      'error',
      'error',
      'warning',
      'warning',
    ]);
  });

  it('gives the worst state of one file', () => {
    expect(fileIssueState(issues, 'SKILL.md')).toBe('error');
    expect(fileIssueState(issues, 'references/a/b.md')).toBe('warning');
    expect(fileIssueState(issues, 'scripts/x.sh')).toBeNull();
  });

  it('writes an issue as one line', () => {
    expect(issueLine(error('r', 'SKILL.md', 'm'))).toBe('SKILL.md: m (r)');
    expect(issueLine(error('r', '', 'm'))).toBe('m (r)');
  });
});

describe('size of the body', () => {
  it('estimates tokens as bytes / 3.7 rounded up, as the server does', () => {
    expect(estimateTokens(0)).toBe(0);
    expect(estimateTokens(37)).toBe(10);
    expect(estimateTokens(38)).toBe(11);
    expect(estimateTokens(18500)).toBe(5000);
    expect(estimateTokens(18501)).toBe(5001);
  });

  it('counts lines as the server does', () => {
    expect(countLines('')).toBe(0);
    expect(countLines('a')).toBe(1);
    expect(countLines('a\n')).toBe(1);
    expect(countLines('a\nb')).toBe(2);
    expect(countLines('a\n\n')).toBe(2);
  });

  it('measures the body after the front matter, in bytes', () => {
    const stats = bodyStats(`${front('a', 'd')}é\nline\n`);
    expect(stats).toEqual({ bytes: 8, lines: 2, tokens: 3 });
    // A text without a front matter counts as a whole.
    expect(bodyStats('abc').bytes).toBe(3);
  });

  it('puts the marker at the limit and flags a value past it', () => {
    const at = meterBar(5000, 5000);
    expect(at.over).toBe(false);
    expect(meterBar(5001, 5000).over).toBe(true);
    expect(at.marker).toBeCloseTo(1 / 1.2);
    expect(at.fill).toBeCloseTo(at.marker);
    expect(meterBar(0, 500).fill).toBe(0);
    expect(meterBar(5000, 500).fill).toBe(1);
  });

  it('flags tokens and lines against 5,000 and 500', () => {
    expect(bodyMeter({ bytes: 0, lines: 500, tokens: 5000 }).over).toBe(false);
    const lines = bodyMeter({ bytes: 0, lines: 501, tokens: 10 });
    expect(lines.over).toBe(true);
    expect(lines.lines.over).toBe(true);
    expect(lines.tokens.over).toBe(false);
    const tokens = bodyMeter({ bytes: 0, lines: 10, tokens: 5001 });
    expect(tokens.tokens.over).toBe(true);
  });
});

describe('checkSkillText', () => {
  const rules = (text: string, name = 'triage') =>
    checkSkillText(name, text).map(
      (issue) => `${issue.severity}:${issue.rule}`
    );

  it('accepts a good skill', () => {
    expect(
      rules(
        `${front('triage', 'Sort the tickets. Use when a ticket arrives.')}Body\n`
      )
    ).toEqual([]);
  });

  it('accepts the six keys of the standard and refuses any other', () => {
    const ok = front(
      'triage',
      'Sort the tickets. Use when a ticket arrives.',
      'license: MIT\ncompatibility: any\nallowed-tools: Read\nmetadata:\n  a: b\n'
    );
    expect(rules(`${ok}Body\n`)).toEqual([]);
    const bad = front(
      'triage',
      'Sort the tickets. Use when a ticket arrives.',
      'model: x\n'
    );
    expect(rules(`${bad}Body\n`)).toEqual(['error:front-matter-key']);
  });

  it('refuses a name that is not the folder name', () => {
    expect(
      rules(`${front('other', 'Sort the tickets. Use when needed.')}B\n`)
    ).toEqual(['error:name-folder']);
  });

  it('checks the description: empty, long, XML and short', () => {
    expect(rules(`${front('triage', '')}B\n`)).toContain(
      'error:description-length'
    );
    expect(rules(`${front('triage', 'x'.repeat(1025))}B\n`)).toContain(
      'error:description-length'
    );
    expect(
      rules(`${front('triage', 'Sort <b>tickets</b> and more')}B\n`)
    ).toContain('error:description-xml');
    expect(rules(`${front('triage', 'Short')}B\n`)).toEqual([
      'warning:description-short',
    ]);
  });

  it('warns on a long body and refuses a body over 64 KiB', () => {
    const head = front('triage', 'Sort the tickets. Use when needed.');
    expect(rules(`${head}${'x\n'.repeat(501)}`)).toEqual([
      'warning:body-lines',
    ]);
    expect(rules(`${head}${'x'.repeat(19000)}\n`)).toEqual([
      'warning:body-tokens',
    ]);
    expect(rules(`${head}${'x'.repeat(65 * 1024)}\n`)).toEqual([
      'error:body-too-large',
      'warning:body-tokens',
    ]);
  });

  it('reports a text without a front matter', () => {
    expect(rules('Body only\n')).toEqual(['error:front-matter-yaml']);
  });
});

describe('visibleIssues', () => {
  const server = [
    error('name-folder', 'SKILL.md'),
    error('root-entry', 'README.md'),
    warning('reference-depth', 'references/a/b.md'),
  ];

  it('shows the server issues when SKILL.md has no draft', () => {
    expect(visibleIssues(server, null).map((i) => i.rule)).toEqual([
      'name-folder',
      'root-entry',
      'reference-depth',
    ]);
  });

  it('replaces the issues of SKILL.md with the checks of the draft, and keeps the others', () => {
    const text = `${front('triage', 'Sort the tickets. Use when needed.')}Body\n`;
    expect(
      visibleIssues(server, { name: 'triage', text }).map((i) => i.rule)
    ).toEqual(['root-entry', 'reference-depth']);
  });
});
