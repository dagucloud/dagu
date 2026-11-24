// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { DagEditOp } from '../../contracts';
import { applyEdit, readRestartSettings } from '..';

type Change = Omit<Extract<DagEditOp, { kind: 'setRestartPolicy' }>, 'kind'>;

function set(text: string, change: Change): string {
  const result = applyEdit(text, { kind: 'setRestartPolicy', ...change });
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  return result.text;
}

function refused(text: string, change: Change): string {
  const result = applyEdit(text, { kind: 'setRestartPolicy', ...change });
  if (result.ok) throw new Error('the edit must fail');
  return result.code;
}

const BASE = `name: demo
description: A demo # keep
steps:
  - name: a
    run: echo a
`;

const SERVICE = `name: demo
restart_policy: always
restart_wait_sec: 5 # seconds
hist_retention_runs: 20
steps:
  - name: a
    run: echo a
`;

describe('readRestartSettings', () => {
  it('reads the three keys', () => {
    expect(readRestartSettings(SERVICE)).toMatchObject({
      policy: 'always',
      waitSec: 5,
      histRetentionRuns: 20,
    });
  });

  it('reads the retention in days', () => {
    expect(
      readRestartSettings('hist_retention_days: 7\n').histRetentionDays
    ).toBe(7);
    expect(readRestartSettings(SERVICE).histRetentionDays).toBeNull();
  });

  it('reads a wait that is not an integer as none', () => {
    expect(readRestartSettings('restart_wait_sec: "5"\n').waitSec).toBeNull();
    expect(readRestartSettings('restart_wait_sec: 1.5\n').waitSec).toBeNull();
  });

  // The server reads an alias as the value of its anchor.
  it('reads a key written as an alias', () => {
    const text = `x-policy: &p always\ndelay_sec: &d 5\nrestart_policy: *p\nrestart_wait_sec: *d\n${BASE}`;
    expect(readRestartSettings(text)).toMatchObject({
      policy: 'always',
      waitSec: 5,
    });
  });
});

describe('setRestartPolicy', () => {
  it('adds the keys in the order of the schema, as one hunk', () => {
    const next = set(BASE, { policy: 'always', waitSec: 5 });
    expect(next).toBe(`name: demo
description: A demo # keep
restart_policy: always
restart_wait_sec: 5
steps:
  - name: a
    run: echo a
`);
  });

  it('puts the retention after the wait', () => {
    const next = set(BASE, {
      policy: 'unless-stopped',
      waitSec: 10,
      histRetentionRuns: 50,
    });
    expect(next.indexOf('restart_wait_sec')).toBeLessThan(
      next.indexOf('hist_retention_runs')
    );
    expect(readRestartSettings(next)).toMatchObject({
      policy: 'unless-stopped',
      waitSec: 10,
      histRetentionRuns: 50,
    });
  });

  it('changes the wait and keeps the comments and the other keys', () => {
    expect(set(SERVICE, { waitSec: 30 })).toBe(
      SERVICE.replace('restart_wait_sec: 5', 'restart_wait_sec: 30')
    );
  });

  it('changes the policy and leaves the other keys', () => {
    expect(set(SERVICE, { policy: 'unless-stopped' })).toBe(
      SERVICE.replace('always', 'unless-stopped')
    );
  });

  it('removes the policy and keeps the wait and the retention', () => {
    expect(set(SERVICE, { policy: 'no' })).toBe(
      SERVICE.replace('restart_policy: always\n', '')
    );
  });

  it('removes the retention with null', () => {
    expect(set(SERVICE, { histRetentionRuns: null })).toBe(
      SERVICE.replace('hist_retention_runs: 20\n', '')
    );
  });

  it('sets the keys of document 0 only', () => {
    const text = `${BASE}---\nname: sub\nsteps:\n  - echo sub\n`;
    const next = set(text, { policy: 'always', waitSec: 5 });
    expect(next.slice(next.indexOf('---'))).toBe(
      text.slice(text.indexOf('---'))
    );
    expect(readRestartSettings(next).policy).toBe('always');
  });

  it('refuses a policy while the file has a schedule', () => {
    expect(
      refused(`schedule: "0 * * * *"\n${BASE}`, {
        policy: 'always',
        waitSec: 5,
      })
    ).toBe('restartConflict');
    expect(
      refused(`schedule:\n  stop: "0 18 * * *"\n${BASE}`, {
        policy: 'unless-stopped',
      })
    ).toBe('restartConflict');
  });

  it('refuses a policy while the file has a retry_policy', () => {
    expect(
      refused(`retry_policy:\n  limit: 2\n${BASE}`, { policy: 'always' })
    ).toBe('restartConflict');
  });

  // The file is already a service: the author can fix it, not only remove it.
  it('lets a service with a schedule change or drop the policy', () => {
    const text = `schedule: "0 * * * *"\n${SERVICE}`;
    expect(
      readRestartSettings(set(text, { policy: 'unless-stopped' })).policy
    ).toBe('unless-stopped');
    expect(readRestartSettings(set(text, { policy: 'no' })).policy).toBe('no');
  });

  it('accepts an empty schedule', () => {
    expect(
      readRestartSettings(set(`schedule: ""\n${BASE}`, { policy: 'always' }))
    ).toMatchObject({
      policy: 'always',
    });
  });

  it('refuses a wait or a retention that is not a positive integer', () => {
    for (const value of [0, -1, 1.5]) {
      expect(refused(SERVICE, { waitSec: value })).toBe('invalidValue');
      expect(refused(SERVICE, { histRetentionRuns: value })).toBe(
        'invalidValue'
      );
    }
  });

  // The server refuses hist_retention_days next to hist_retention_runs.
  it('replaces a retention in days with the runs kept', () => {
    const text = `${BASE}hist_retention_days: 7\n`;
    const next = set(text, { policy: 'always', histRetentionRuns: 20 });
    expect(next).not.toContain('hist_retention_days');
    expect(readRestartSettings(next).histRetentionRuns).toBe(20);
  });

  // A new value of an anchored key would change its aliases too.
  it('refuses a key with an anchor', () => {
    const text = `restart_wait_sec: &w 5\ndelay_sec: *w\n${BASE}`;
    expect(refused(text, { waitSec: 10 })).toBe('unsupportedForm');
  });

  // A count of runs removes the days, which an alias may still read.
  it('refuses a count of runs while the days have an anchor', () => {
    const text = `hist_retention_days: &d 7\ndelay_sec: *d\n${BASE}`;
    expect(refused(text, { histRetentionRuns: 20 })).toBe('unsupportedForm');
  });

  it('refuses a text that does not parse', () => {
    expect(refused('a: [\n', { policy: 'always' })).toBe('parseError');
  });
});
