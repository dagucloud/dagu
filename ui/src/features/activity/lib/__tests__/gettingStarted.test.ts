// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import { isExampleDAGName, progressFromSample } from '../gettingStarted';

type DAGFile = components['schemas']['DAGFile'];

function dag(
  name: string,
  {
    ran = false,
    schedule = false,
    suspended = false,
  }: { ran?: boolean; schedule?: boolean; suspended?: boolean } = {}
): DAGFile {
  return {
    fileName: name,
    dag: {
      name,
      schedule: schedule ? [{ expression: '0 9 * * *' }] : [],
    },
    latestDAGRun: { dagRunId: ran ? `${name}-1` : '' },
    suspended,
    pinned: false,
    errors: [],
  } as unknown as DAGFile;
}

const examples = [
  'example-01-basic-sequential',
  'example-02-parallel',
  'example-03-scheduling',
  'example-04-nested',
  'example-05-container',
  'example-06-retry-on-failure',
].map((name) => dag(name));

function sample(dags: DAGFile[], totalRecords = dags.length) {
  return { dags, pagination: { totalRecords } };
}

describe('isExampleDAGName', () => {
  it('matches the first-launch examples only', () => {
    expect(isExampleDAGName('example-06-retry-on-failure')).toBe(true);
    expect(isExampleDAGName('nightly-example')).toBe(false);
  });
});

describe('progressFromSample', () => {
  it('reads a fresh install as nothing done', () => {
    expect(progressFromSample(sample(examples))).toEqual({
      ran: false,
      created: false,
      scheduled: false,
      hasRetryExample: true,
    });
  });

  it('counts a run of any workflow', () => {
    const [first, ...rest] = examples;
    const progress = progressFromSample(
      sample([dag(first!.dag.name, { ran: true }), ...rest])
    );

    expect(progress.ran).toBe(true);
    expect(progress.created).toBe(false);
  });

  it('counts a workflow that is not an example as created', () => {
    expect(progressFromSample(sample([...examples, dag('etl')])).created).toBe(
      true
    );
  });

  it('counts only an unsuspended schedule', () => {
    expect(
      progressFromSample(
        sample([dag('etl', { schedule: true, suspended: true })])
      ).scheduled
    ).toBe(false);
    expect(
      progressFromSample(sample([dag('etl', { schedule: true })])).scheduled
    ).toBe(true);
  });

  // More workflows than the page holds means someone added them, but the
  // rest may hold the only schedule.
  it('leaves the schedule unknown for a partial page', () => {
    const progress = progressFromSample(sample(examples, 12));

    expect(progress.created).toBe(true);
    expect(progress.scheduled).toBeUndefined();
  });

  it('notices a missing retry example', () => {
    expect(progressFromSample(sample([dag('etl')])).hasRetryExample).toBe(
      false
    );
  });
});
