// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { DagEditOp } from '../../contracts';
import { applyEdit, readRestart } from '..';

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

describe('readRestart', () => {
  it('reads no policy', () => {
    expect(readRestart(BASE)).toEqual({
      policy: 'no',
      waitSec: null,
      histRetentionRuns: null,
      schedule: false,
      retryPolicy: false,
    });
  });

  it('reads the three keys', () => {
    expect(readRestart(SERVICE)).toMatchObject({
      policy: 'always',
      waitSec: 5,
      histRetentionRuns: 20,
    });
  });

  // Spec 080: the case and the spaces of the value do not count.
  it('ignores the case and the spaces of the value', () => {
    expect(readRestart('restart_policy: " Unless-Stopped "\n').policy).toBe(
      'unless-stopped'
    );
    expect(readRestart('restart_policy: ALWAYS\n').policy).toBe('always');
  });

  it('reads an empty, a null and an unknown value as no', () => {
    for (const value of ['""', 'null', '', 'sometimes', 'on-failure']) {
      expect(readRestart(`restart_policy: ${value}\n`).policy).toBe('no');
    }
  });

  it('reads a wait that is not an integer as none', () => {
    expect(readRestart('restart_wait_sec: "5"\n').waitSec).toBeNull();
    expect(readRestart('restart_wait_sec: 1.5\n').waitSec).toBeNull();
  });

  it('tells a schedule of any form and a retry_policy', () => {
    expect(readRestart('schedule: "* * * * *"\n').schedule).toBe(true);
    expect(
      readRestart('schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n')
        .schedule
    ).toBe(true);
    // An empty schedule defines none.
    expect(readRestart('schedule: ""\n').schedule).toBe(false);
    expect(readRestart('retry_policy:\n  limit: 2\n').retryPolicy).toBe(true);
  });

  it('reads document 0 and does not throw on a broken text', () => {
    expect(
      readRestart(`${SERVICE}---\nname: sub\nrestart_policy: no\n`).policy
    ).toBe('always');
    expect(readRestart('a: [\n').policy).toBe('no');
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
    expect(readRestart(next)).toMatchObject({
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
    expect(readRestart(next).policy).toBe('always');
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
    expect(readRestart(set(text, { policy: 'unless-stopped' })).policy).toBe(
      'unless-stopped'
    );
    expect(readRestart(set(text, { policy: 'no' })).policy).toBe('no');
  });

  it('accepts an empty schedule', () => {
    expect(
      readRestart(set(`schedule: ""\n${BASE}`, { policy: 'always' }))
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

  it('refuses a text that does not parse', () => {
    expect(refused('a: [\n', { policy: 'always' })).toBe('parseError');
  });
});
