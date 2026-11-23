// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { readRestart } from '../ops/restart';

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
      schedule: false,
      retryPolicy: false,
      waitGiven: false,
    });
  });

  it('reads the policy and that the file gives a wait', () => {
    expect(readRestart(SERVICE)).toMatchObject({
      policy: 'always',
      waitGiven: true,
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

  // The server merges the file over the base configuration: no value and 0
  // leave the wait of the base. An alias gives the value of its anchor.
  it('tells when the file gives a wait', () => {
    const given = (wait: string) =>
      readRestart(`restart_wait_sec: ${wait}\n`).waitGiven;
    expect(given('5')).toBe(true);
    expect(given('-1')).toBe(true);
    expect(given('0')).toBe(false);
    expect(given('')).toBe(false);
    expect(given('null')).toBe(false);
    expect(
      readRestart('delay_sec: &w 5\nrestart_wait_sec: *w\n').waitGiven
    ).toBe(true);
  });

  it('tells a schedule of any form and a retry_policy', () => {
    expect(readRestart('schedule: "* * * * *"\n').schedule).toBe(true);
    expect(
      readRestart('schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n')
        .schedule
    ).toBe(true);
    // An empty schedule defines none, as the server counts its entries.
    expect(readRestart('schedule: ""\n').schedule).toBe(false);
    expect(readRestart('schedule: {}\n').schedule).toBe(false);
    expect(readRestart('schedule:\n  start: []\n').schedule).toBe(false);
    expect(readRestart('retry_policy:\n  limit: 2\n').retryPolicy).toBe(true);
    // A key with no value: the server reads no retry policy.
    expect(readRestart('retry_policy:\n').retryPolicy).toBe(false);
  });

  it('reads document 0 and does not throw on a broken text', () => {
    expect(
      readRestart(`${SERVICE}---\nname: sub\nrestart_policy: no\n`).policy
    ).toBe('always');
    expect(readRestart('a: [\n').policy).toBe('no');
  });
});
