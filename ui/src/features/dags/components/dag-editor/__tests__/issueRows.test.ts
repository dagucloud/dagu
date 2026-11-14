// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
  type components,
} from '@/api/v1/schema';
import { issueRows } from '../issueRows';

type ValidationIssue = components['schemas']['ValidationIssue'];

const issue = (extra: Partial<ValidationIssue>): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message: 'm',
  ...extra,
});

describe('issueRows', () => {
  it('gives a row for each message, with its issue when one has the same text', () => {
    const own = issue({ message: 'invalid keys: x' });
    const rows = issueRows({
      warnings: [],
      errors: ['[3:1] invalid keys: x', 'another'],
      issues: [own],
    });
    expect(rows.errors).toEqual([
      { message: '[3:1] invalid keys: x', issue: own },
      { message: 'another' },
    ]);
  });

  it('adds the issues that no message says, after the messages', () => {
    const extra = issue({ message: 'found by the client' });
    const rows = issueRows({
      warnings: [],
      errors: ['server'],
      issues: [extra],
    });
    expect(rows.errors.map((row) => row.message)).toEqual([
      'server',
      'found by the client',
    ]);
  });

  it('keeps the severities apart and gives a notice no row', () => {
    const warning = issue({ severity: ValidationIssueSeverity.warning });
    const notice = issue({ severity: ValidationIssueSeverity.notice });
    const rows = issueRows({
      warnings: [],
      errors: [],
      issues: [warning, notice],
    });
    expect(rows.errors).toEqual([]);
    expect(rows.warnings).toEqual([{ message: 'm', issue: warning }]);
  });

  it('keeps the issue of each position on its own message', () => {
    const second = issue({ message: '[2:1] invalid key', line: 2 });
    const first = issue({ message: '[1:1] invalid key', line: 1 });
    const rows = issueRows({
      warnings: [],
      errors: ['[1:1] invalid key', '[2:1] invalid key'],
      issues: [second, first],
    });
    expect(rows.errors).toEqual([
      { message: '[1:1] invalid key', issue: first },
      { message: '[2:1] invalid key', issue: second },
    ]);
  });

  it('gives the same issue to one message only', () => {
    const rows = issueRows({
      warnings: [],
      errors: ['dup', 'dup'],
      issues: [issue({ message: 'dup' })],
    });
    expect(rows.errors).toHaveLength(2);
    expect(rows.errors.filter((row) => row.issue)).toHaveLength(1);
  });
});
