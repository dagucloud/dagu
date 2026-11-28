// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { WorkerAccessReason } from '@/api/v1/schema';
import { WorkerRequestError, describeWorkerError } from '../useWorkerAdmin';

// Fills placeholders the way the page's translation does.
function ts(source: string, values?: Record<string, string | number>) {
  return source.replace(/\{(\w+)\}/g, (_, name) =>
    String(values?.[name] ?? `{${name}}`)
  );
}

const notFound = { cause: 'not found', change: 'look elsewhere' };
const statusCauses = [
  'The request failed.',
  'The coordinator refused the request.',
  'Only admins can manage workers and invites.',
];

describe('describeWorkerError', () => {
  // Walks the generated enum, so that a reason the API adds without page
  // text fails here.
  it.each(Object.values(WorkerAccessReason))(
    'gives the reason %s its own text',
    (reason) => {
      const advice = describeWorkerError(
        new WorkerRequestError(409, reason, 'server words'),
        { notFound },
        ts
      );

      expect(statusCauses).not.toContain(advice.cause);
      expect(advice.cause).not.toContain('server words');
      expect(advice.change).not.toContain('server words');
      // Only a refusal whose cause the server alone names quotes it.
      expect(advice.detail).toBe(
        reason === WorkerAccessReason.coordinator_refused
          ? 'server words'
          : undefined
      );
    }
  );

  it('gives every reason a cause of its own', () => {
    const causes = Object.values(WorkerAccessReason).map(
      (reason) =>
        describeWorkerError(
          new WorkerRequestError(409, reason),
          { notFound },
          ts
        ).cause
    );

    expect(new Set(causes).size).toBe(causes.length);
  });
});
