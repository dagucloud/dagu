// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { StepKey, StepView, ValidationIssue } from '../contracts';
import type { StepIssues } from './types';

/**
 * Field path of an issue relative to a step: `steps[3].with.url` gives
 * `with.url` for the step `steps[3]`. Gives null when the path is not
 * inside the step.
 */
export function relativeFieldPath(
  step: StepKey,
  fieldPath: string | undefined
): string | null {
  if (!fieldPath) {
    return null;
  }
  const prefix = `${step}.`;
  return fieldPath.startsWith(prefix) ? fieldPath.slice(prefix.length) : null;
}

// The wrapping of a build error: `field 'steps': field with: ` before the
// message and a dump of the Go value after it.
const FIELD_PREFIX = /^(?:field '?[A-Za-z0-9_.[\]-]+'?: )+/;
const VALUE_DUMP = / \(value: [\s\S]*\)\s*$/;

/** The message of a build error without its field prefixes and Go value dump. */
export function plainIssueMessage(message: string): string {
  const plain = message.replace(FIELD_PREFIX, '').replace(VALUE_DUMP, '');
  return plain.trim() === '' ? message : plain;
}

/** Translates a message of the form with `{name}` parameters. */
export type IssueTranslate = (
  key: string,
  params?: Record<string, string | number>
) => string;

// The build error of a missing key: `field 'steps': field 'with': with.filter
// is required (value: map[...])`. The tail is a dump of the Go value.
const REQUIRED_FIELD =
  /(?:^|[\s:])([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+) is required(?: \(value: [\s\S]*\))?\s*$/;

/**
 * The field that a build error says is required, with its path in the file
 * (`steps[1].with.filter`) and its short name. Null for any other issue, and
 * when the path of the issue does not lead to the field.
 */
export function requiredFieldOf(
  issue: ValidationIssue
): { fieldPath: string; name: string } | null {
  if (!issue.stepPath || !issue.fieldPath) return null;
  const token = REQUIRED_FIELD.exec(issue.message)?.[1];
  if (!token) return null;
  const parts = token.split('.');
  const last = issue.fieldPath.split('.').pop();
  if (parts[0] !== last) return null;
  return {
    fieldPath: `${issue.fieldPath}.${parts.slice(1).join('.')}`,
    name: parts[parts.length - 1] as string,
  };
}

/**
 * An issue that names a required field moves to that field, with a short
 * message in the language of the page. Other issues stay as they are.
 */
export function refineIssue(
  issue: ValidationIssue,
  translate: IssueTranslate
): ValidationIssue {
  const required = requiredFieldOf(issue);
  if (!required) return issue;
  return {
    ...issue,
    fieldPath: required.fieldPath,
    message: translate('{field} is required', { field: required.name }),
  };
}

/**
 * Groups the issues of one step by field. The caller gives the issues of
 * this step; an issue whose `stepPath` names another step is dropped. An
 * issue without a field inside the step goes to `general`.
 */
export function stepIssues(
  issues: ValidationIssue[],
  step: StepView,
  translate?: IssueTranslate
): StepIssues {
  // No prototype: a field can be named `constructor`.
  const byField: Record<string, ValidationIssue[]> = Object.create(null);
  const general: ValidationIssue[] = [];
  for (const raw of issues) {
    const issue = translate ? refineIssue(raw, translate) : raw;
    const field = relativeFieldPath(step.key, issue.fieldPath);
    if (issue.stepPath && issue.stepPath !== step.key && field === null) {
      continue;
    }
    if (field) {
      (byField[field] ??= []).push(issue);
    } else {
      general.push(issue);
    }
  }
  return { byField, general };
}

/** The issues of a field and of every field below it (`with` gives `with.url`). */
export function issuesUnder(
  fieldIssues: Record<string, ValidationIssue[]>,
  field: string
): ValidationIssue[] {
  return Object.entries(fieldIssues).flatMap(([key, list]) =>
    key === field || key.startsWith(`${field}.`) || key.startsWith(`${field}[`)
      ? list
      : []
  );
}
