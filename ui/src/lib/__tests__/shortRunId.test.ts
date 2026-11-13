// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { shortRunId } from '../shortRunId';

describe('shortRunId', () => {
  it('keeps the first 8 characters of an ordinary ID', () => {
    expect(shortRunId('20260115T020000Z-abc')).toBe('20260115');
    expect(shortRunId('run-1')).toBe('run-1');
  });

  // The scheme is the same for every run: the hash and the time tell them apart.
  it('tells the restart IDs of the scheduler apart', () => {
    const a = shortRunId('restart-1a2b3c4d5e6f-20261108T150405');
    const b = shortRunId('restart-9f8e7d6c5b4a-20261108T150411');
    expect(a).toBe('1a2b-150405');
    expect(b).toBe('9f8e-150411');
    expect(a).not.toContain('restart');
  });

  it('reads the catchup and one-off IDs the same way', () => {
    expect(shortRunId('catchup-0123456789ab-20260101T000000')).toBe(
      '0123-000000'
    );
    expect(shortRunId('oneoff-0123456789ab-20260101T235959')).toBe(
      '0123-235959'
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
