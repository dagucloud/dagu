// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { StepView, ValidationIssue } from '../../contracts';
import { issuesSample, modelArraySteps } from '../../testing/fixtures';
import {
  issuesUnder,
  plainIssueMessage,
  refineIssue,
  relativeFieldPath,
  requiredFieldOf,
  stepIssues,
} from '../issues';

function issue(fields: Partial<ValidationIssue>): ValidationIssue {
  return {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message: 'm',
    ...fields,
  };
}

describe('plainIssueMessage', () => {
  it.each([
    [
      'field steps: field with: with.url must be a non-empty string (value: map[method:GET url:])',
      'with.url must be a non-empty string',
    ],
    [
      "field 'steps': field 'with': with.filter is required (value: map[a:b])",
      'with.filter is required',
    ],
    ['step command is empty (value: )', 'step command is empty'],
    ['an ordinary message', 'an ordinary message'],
    ['field steps: ', 'field steps: '],
  ])('turns %j into %j', (raw, plain) => {
    expect(plainIssueMessage(raw)).toBe(plain);
  });
});

describe('relativeFieldPath', () => {
  it.each([
    ['steps[3]', 'steps[3].with.url', 'with.url'],
    ['steps[3]', 'steps[3].depends', 'depends'],
    ['steps[3]', 'steps[30].run', null],
    ['steps[3]', 'steps[3]', null],
    ['steps[3]', undefined, null],
    ['handler_on.failure', 'handler_on.failure.run', 'run'],
  ])('%s + %s gives %s', (step, field, expected) => {
    expect(relativeFieldPath(step, field)).toBe(expected);
  });
});

describe('stepIssues', () => {
  const transform = modelArraySteps.steps[1]!;

  it('groups the sample issues of a step by field', () => {
    const groups = stepIssues(issuesSample, transform);
    expect(Object.keys(groups.byField)).toEqual(['depends']);
    expect(groups.byField.depends?.[0]?.message).toContain(
      'unknown dependency'
    );
    // The DAG-level warning has no step; the caller filters such issues,
    // so it stays in the general list here.
    expect(groups.general.map((i) => i.fieldPath)).toEqual(['description']);
  });

  it('drops issues of other steps', () => {
    const groups = stepIssues(issuesSample, modelArraySteps.steps[0]!);
    expect(groups.byField).toEqual({});
    expect(groups.general).toHaveLength(1);
  });

  it('keeps a step issue without a field in general', () => {
    const groups = stepIssues([issue({ stepPath: 'steps[1]' })], transform);
    expect(groups.general).toHaveLength(1);
  });

  it('groups the issues of a handler', () => {
    const handler: StepView = {
      ...transform,
      key: 'handler_on.failure',
      index: null,
    };
    const groups = stepIssues(
      [
        issue({
          stepPath: 'handler_on.failure',
          fieldPath: 'handler_on.failure.with.url',
        }),
        issue({
          stepPath: 'handler_on.exit',
          fieldPath: 'handler_on.exit.run',
        }),
      ],
      handler
    );
    expect(Object.keys(groups.byField)).toEqual(['with.url']);
    expect(groups.general).toEqual([]);
  });

  it('collects several issues of one field', () => {
    const groups = stepIssues(
      [
        issue({ fieldPath: 'steps[1].run', message: 'a' }),
        issue({ fieldPath: 'steps[1].run', message: 'b' }),
      ],
      transform
    );
    expect(groups.byField.run?.map((i) => i.message)).toEqual(['a', 'b']);
  });

  it('groups a field named as a property of an object', () => {
    const groups = stepIssues(
      [issue({ fieldPath: 'steps[1].constructor', message: 'a' })],
      transform
    );
    expect(groups.byField['constructor']?.map((i) => i.message)).toEqual(['a']);
  });
});

describe('issuesUnder', () => {
  const fieldIssues = {
    with: [issue({ message: 'with' })],
    'with.url': [issue({ message: 'url' })],
    'with.headers.X': [issue({ message: 'header' })],
    without: [issue({ message: 'other' })],
    run: [issue({ message: 'run' })],
  };

  it('collects the issues of a field and of its children', () => {
    expect(issuesUnder(fieldIssues, 'with').map((i) => i.message)).toEqual([
      'with',
      'url',
      'header',
    ]);
    expect(issuesUnder(fieldIssues, 'env')).toEqual([]);
  });
});

describe('required field issues', () => {
  const raw = issue({
    stepPath: 'steps[1]',
    stepName: 'extract',
    fieldPath: 'steps[1].with',
    message:
      "field 'steps': field 'with': with.filter is required (value: map[data:${steps.fetch.outputs.body} raw:true])",
  });
  const translate = (key: string, params?: Record<string, string | number>) =>
    key.replace('{field}', String(params?.field));

  it('finds the field behind a build error and drops the Go dump', () => {
    expect(requiredFieldOf(raw)).toEqual({
      fieldPath: 'steps[1].with.filter',
      name: 'filter',
    });
    expect(refineIssue(raw, translate)).toMatchObject({
      fieldPath: 'steps[1].with.filter',
      message: 'filter is required',
    });
  });

  it('leaves other issues alone', () => {
    const other = issue({
      stepPath: 'steps[1]',
      fieldPath: 'steps[1].with',
      message: 'url is invalid',
    });
    expect(requiredFieldOf(other)).toBeNull();
    expect(refineIssue(other, translate)).toBe(other);
    // A token that the path of the issue does not lead to stays unmoved.
    expect(requiredFieldOf({ ...raw, fieldPath: 'steps[1].env' })).toBeNull();
  });

  it('puts the issue under its field in the step', () => {
    const transform = modelArraySteps.steps[1]!;
    const groups = stepIssues([raw], transform, translate);
    expect(Object.keys(groups.byField)).toEqual(['with.filter']);
    expect(groups.byField['with.filter']?.[0]?.message).toBe(
      'filter is required'
    );
    expect(groups.general).toEqual([]);
  });
});
