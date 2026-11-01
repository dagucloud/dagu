// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
  type components,
} from '@/api/v1/schema';
import { buildIssueMarkers } from '../issueMarkers';

type ValidationIssue = components['schemas']['ValidationIssue'];

const text = `steps:
  - name: fetch
    run: curl https://example.com
  - name: load
    depends: [fetch]
    timeout_sec: soon
`;

const issue = (fields: Partial<ValidationIssue>): ValidationIssue => ({
  severity: ValidationIssueSeverity.error,
  source: ValidationIssueSource.build,
  message: 'problem',
  ...fields,
});

describe('buildIssueMarkers', () => {
  it('puts a field issue on the line of the field', () => {
    const markers = buildIssueMarkers(
      text,
      [
        issue({
          message: 'timeout_sec must be a number',
          stepPath: 'steps[1]',
          fieldPath: 'steps[1].timeout_sec',
        }),
      ],
      ['timeout_sec must be a number']
    );

    expect(markers).toHaveLength(1);
    expect(markers[0]).toMatchObject({
      startLineNumber: 6,
      endLineNumber: 6,
      message: 'timeout_sec must be a number',
      severity: 8,
    });
  });

  it('puts a step issue on the step and keeps the severity', () => {
    const markers = buildIssueMarkers(
      text,
      [
        issue({
          severity: ValidationIssueSeverity.warning,
          stepPath: 'steps[0]',
        }),
      ],
      []
    );

    expect(markers[0]).toMatchObject({ startLineNumber: 2, severity: 4 });
  });

  it('uses the position prefix of an issue without a location', () => {
    const markers = buildIssueMarkers(
      text,
      [issue({ message: '[3:5] mapping values are not allowed' })],
      ['[3:5] mapping values are not allowed']
    );

    expect(markers).toEqual([
      expect.objectContaining({
        startLineNumber: 3,
        startColumn: 5,
        message: 'mapping values are not allowed',
      }),
    ]);
  });

  it('keeps the severity of an issue without a location', () => {
    const markers = buildIssueMarkers(
      text,
      [
        issue({
          severity: ValidationIssueSeverity.warning,
          message: '[3:5] deprecated field',
        }),
      ],
      []
    );

    expect(markers[0]).toMatchObject({ startLineNumber: 3, severity: 4 });
  });

  it('marks the error strings when there are no issues', () => {
    const markers = buildIssueMarkers(text, [], ['[4:1] bad', 'no position']);

    expect(markers).toHaveLength(1);
    expect(markers[0]?.startLineNumber).toBe(4);
  });

  it('skips an issue that it cannot place', () => {
    expect(
      buildIssueMarkers(text, [issue({ message: 'dag problem' })], [])
    ).toEqual([]);
  });
});
