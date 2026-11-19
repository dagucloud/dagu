// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import { buildToolCalls, buildToolSubRuns } from '../toolCalls';

type DAGRunDetails = components['schemas']['DAGRunDetails'];

// A run of a step that called its tool DAGs: each call is a sub-run with the
// name of the tool DAG, as the chat and agent steps record it.
const DETAILS = {
  nodes: [
    { step: { name: 'fetch' }, subRuns: [] },
    {
      step: { name: 'triage' },
      subRuns: [
        { dagRunId: 'r1', dagName: 'lookup', params: '{"id":"1"}' },
        { dagRunId: 'r2', dagName: 'refund' },
      ],
      subRunsRepeated: [{ dagRunId: 'r3', dagName: 'lookup' }],
    },
    // A sub-run with no DAG name is not a tool call.
    { step: { name: 'other' }, subRuns: [{ dagRunId: 'r4' }] },
  ],
} as unknown as DAGRunDetails;

describe('buildToolSubRuns', () => {
  it('lists the sub-runs of each tool DAG of a step, in the order of the run', () => {
    expect(buildToolSubRuns(DETAILS)).toEqual({
      triage: {
        lookup: [{ dagRunId: 'r1', params: '{"id":"1"}' }, { dagRunId: 'r3' }],
        refund: [{ dagRunId: 'r2' }],
      },
    });
  });

  it('is empty without a run', () => {
    expect(buildToolSubRuns(null)).toEqual({});
    expect(buildToolSubRuns(undefined)).toEqual({});
  });
});

describe('buildToolCalls', () => {
  it('counts the sub-runs of each tool DAG of a step', () => {
    expect(buildToolCalls(DETAILS)).toEqual({
      triage: { lookup: 2, refund: 1 },
    });
  });
});
