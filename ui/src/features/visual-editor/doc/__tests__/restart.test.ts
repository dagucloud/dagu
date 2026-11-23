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

describe('readRestart', () => {
  it('reads no schedule and no retry policy', () => {
    expect(readRestart(BASE)).toEqual({
      schedule: false,
      retryPolicy: false,
    });
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
    // An entry can be an object, in a list or in the map form.
    expect(
      readRestart('schedule:\n  start:\n    expression: "0 8 * * *"\n').schedule
    ).toBe(true);
    expect(
      readRestart('schedule:\n  - expression: "0 8 * * *"\n').schedule
    ).toBe(true);
    // The map form takes only start, stop and restart: the server reads any
    // other map as a broken schedule, not as an entry.
    expect(readRestart('schedule:\n  expression: "0 8 * * *"\n').schedule).toBe(
      false
    );
    expect(readRestart('retry_policy:\n  limit: 2\n').retryPolicy).toBe(true);
    // A key with no value: the server reads no retry policy.
    expect(readRestart('retry_policy:\n').retryPolicy).toBe(false);
  });

  it('reads document 0 and does not throw on a broken text', () => {
    expect(
      readRestart(`${BASE}---\nname: sub\nschedule: "* * * * *"\n`).schedule
    ).toBe(false);
    expect(readRestart('schedule: "* * * * *"\nsteps: [\n').schedule).toBe(
      false
    );
  });
});
