// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { shortRunId } from '../shortRunId';

describe('shortRunId', () => {
  it('keeps the first 8 characters of an ordinary ID', () => {
    expect(shortRunId('20260115T020000Z-abc')).toBe('20260115');
    expect(shortRunId('run-1')).toBe('run-1');
  });

  it('tells the restart IDs of the scheduler apart', () => {
    const a = shortRunId('restart-1a2b3c4d5e6f-20261108T150405');
    const b = shortRunId('restart-9f8e7d6c5b4a-20261108T150411');
    expect(a).toBe('1108T150405');
    expect(b).toBe('1108T150411');
  });

  // The hash of a scheduled run comes from the DAG, so every run of one DAG
  // shares it: only the date tells the runs of a daily schedule apart.
  it('tells the runs of a daily schedule apart', () => {
    const a = shortRunId('sched-0123456789ab-20261108T090000');
    const b = shortRunId('sched-0123456789ab-20261109T090000');
    expect(a).toBe('1108T090000');
    expect(b).toBe('1109T090000');
  });

  it('reads the catchup and one-off IDs the same way', () => {
    expect(shortRunId('catchup-0123456789ab-20260101T000000')).toBe(
      '0101T000000'
    );
    expect(shortRunId('oneoff-0123456789ab-20260101T235959')).toBe(
      '0101T235959'
    );
  });

  // The first restart of a DAG has no time part.
  it('shows the hash of a restart ID without a time', () => {
    expect(shortRunId('restart-1a2b3c4d5e6f')).toBe('1a2b3c4d');
  });

  it('leaves another ID with the scheme word alone', () => {
    expect(shortRunId('restart-the-api-now')).toBe('restart-');
  });
});
