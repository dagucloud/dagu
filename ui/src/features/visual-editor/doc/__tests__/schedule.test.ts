// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { applyEdit, readSchedule } from '..';

function set(text: string, expressions: string[]): string {
  const result = applyEdit(text, { kind: 'setSchedule', expressions });
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  return result.text;
}

const BASE = `name: demo
description: A demo # keep
steps:
  - name: a
    run: echo a
`;

describe('readSchedule', () => {
  it('reads no schedule', () => {
    expect(readSchedule(BASE)).toEqual({
      present: false,
      expressions: [],
      others: 0,
    });
  });

  it('reads a string, a list and the start of the map form', () => {
    expect(readSchedule('schedule: "0 * * * *"\n').expressions).toEqual([
      '0 * * * *',
    ]);
    expect(
      readSchedule('schedule:\n  - "0 * * * *"\n  - "@daily"\n').expressions
    ).toEqual(['0 * * * *', '@daily']);
    expect(
      readSchedule('schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n')
        .expressions
    ).toEqual(['0 8 * * *']);
  });

  it('counts entries that are not plain strings', () => {
    const view = readSchedule(
      'schedule:\n  - "0 * * * *"\n  - at: 2030-01-01T00:00:00Z\n'
    );
    expect(view).toEqual({
      present: true,
      expressions: ['0 * * * *'],
      others: 1,
    });
  });

  it('counts a start that is one object', () => {
    expect(
      readSchedule('schedule:\n  start:\n    at: 2030-01-01T00:00:00Z\n')
    ).toEqual({ present: true, expressions: [], others: 1 });
  });

  it('does not throw on a broken text', () => {
    expect(readSchedule('a: [\n').present).toBe(false);
  });

  it('reads document 0 of a file with a sub-DAG', () => {
    const text = `schedule: "0 9 * * *"\n${BASE}---\nname: sub\nsteps:\n  - echo sub\n`;
    expect(readSchedule(text).expressions).toEqual(['0 9 * * *']);
  });
});

describe('setSchedule', () => {
  it('adds the key after the header, as one hunk', () => {
    const next = set(BASE, ['0 * * * *']);
    expect(next).toBe(`name: demo
description: A demo # keep
schedule: 0 * * * *
steps:
  - name: a
    run: echo a
`);
  });

  it('writes several expressions as a list', () => {
    const next = set(BASE, ['0 * * * *', '@daily']);
    expect(readSchedule(next).expressions).toEqual(['0 * * * *', '@daily']);
    expect(next).toContain('steps:\n  - name: a');
  });

  it('replaces the value and keeps the other lines', () => {
    const text = `${BASE}\nschedule: "5 * * * *"\n`;
    const next = set(text, ['10 * * * *']);
    expect(next.startsWith(BASE)).toBe(true);
    expect(readSchedule(next).expressions).toEqual(['10 * * * *']);
  });

  it('keeps entries that are not plain strings', () => {
    const text =
      'schedule:\n  - "0 * * * *"\n  - at: 2030-01-01T00:00:00Z\nsteps:\n  - echo a\n';
    const next = set(text, ['5 * * * *']);
    const view = readSchedule(next);
    expect(view.expressions).toEqual(['5 * * * *']);
    expect(view.others).toBe(1);
  });

  it('changes only start in the map form', () => {
    const text = 'schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n';
    const next = set(text, ['0 9 * * *']);
    expect(next).toContain('stop: "0 18 * * *"');
    expect(readSchedule(next).expressions).toEqual(['0 9 * * *']);
  });

  it('removes the key with an empty list', () => {
    const next = set(`${BASE}schedule: "5 * * * *"\n`, []);
    expect(next).toBe(BASE);
  });

  it('keeps the other entries with an empty list', () => {
    const list = set(
      'schedule:\n  - "0 * * * *"\n  - at: 2030-01-01T00:00:00Z\n',
      []
    );
    expect(list).toBe('schedule:\n  - at: 2030-01-01T00:00:00Z\n');

    const map = set(
      'schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n',
      []
    );
    expect(map).toBe('schedule:\n  stop: "0 18 * * *"\n');
  });

  // The start of the map form can be one object, such as a one-off run.
  it('keeps a start that is one object', () => {
    const text = 'schedule:\n  start:\n    at: 2030-01-01T00:00:00Z\n';
    const next = set(text, ['0 9 * * *']);
    expect(next).toContain('at: 2030-01-01T00:00:00Z');
    expect(readSchedule(next)).toEqual({
      present: true,
      expressions: ['0 9 * * *'],
      others: 1,
    });

    const result = applyEdit(text, { kind: 'setSchedule', expressions: [] });
    expect(result.ok).toBe(false);
  });

  it('refuses a change that changes nothing', () => {
    const result = applyEdit(BASE, { kind: 'setSchedule', expressions: [] });
    expect(result.ok).toBe(false);
  });
});
