// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ValidationIssueSeverity, type components } from '@/api/v1/schema';

type ValidationIssue = components['schemas']['ValidationIssue'];

/** One line of the list of issues. */
export interface IssueRow {
  message: string;
  /** The structured issue of the message, when there is one. */
  issue?: ValidationIssue;
}

const POSITION_PREFIX = /^\[\d+:\d+\]\s*/;

function plain(message: string): string {
  return message.replace(POSITION_PREFIX, '');
}

// Two messages with positions match only at the same position.
function sameText(a: string, b: string): boolean {
  if (POSITION_PREFIX.test(a) && POSITION_PREFIX.test(b)) return a === b;
  return plain(a) === plain(b);
}

function rowsOf(
  severity: ValidationIssueSeverity,
  messages: string[],
  issues: ValidationIssue[]
): IssueRow[] {
  const own = issues.filter((issue) => issue.severity === severity);
  const used = new Set<ValidationIssue>();
  const rows: IssueRow[] = messages.map((message) => {
    const issue = own.find(
      (item) => !used.has(item) && sameText(item.message, message)
    );
    if (issue) used.add(issue);
    return issue ? { message, issue } : { message };
  });
  // An issue that no message says (the client found it) has a row too.
  for (const issue of own) {
    if (!used.has(issue)) rows.push({ message: issue.message, issue });
  }
  return rows;
}

/**
 * The rows of the errors and of the warnings. The messages of the server
 * come first, each with its structured issue when one has the same text;
 * the structured issues that no message says come after them. A notice has
 * no row.
 */
export function issueRows(input: {
  warnings: string[];
  errors: string[];
  issues?: ValidationIssue[];
}): { errors: IssueRow[]; warnings: IssueRow[] } {
  const issues = input.issues ?? [];
  return {
    errors: rowsOf(ValidationIssueSeverity.error, input.errors, issues),
    warnings: rowsOf(ValidationIssueSeverity.warning, input.warnings, issues),
  };
}
