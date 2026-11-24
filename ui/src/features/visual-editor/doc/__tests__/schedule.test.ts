// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { applyEdit, readSchedule } from '..';
import { yamlUnindentedQuote } from '../../testing/fixtures';

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
    expect(readSchedule(BASE)).toMatchObject({
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
    expect(view).toMatchObject({
      present: true,
      expressions: ['0 * * * *'],
      others: 1,
      entries: [
        { expressionIndex: 0, params: {} },
        { schedule: { kind: 'at', at: '2030-01-01T00:00:00Z' }, params: {} },
      ],
    });
  });

  it('counts a start that is one object', () => {
    expect(
      readSchedule('schedule:\n  start:\n    at: 2030-01-01T00:00:00Z\n')
    ).toMatchObject({
      present: true,
      expressions: [],
      others: 1,
      entries: [{ schedule: { kind: 'at' }, params: {} }],
    });
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

  it('removes the selected duplicate while keeping the override in place', () => {
    const text = `schedule:
  - "0 6 * * *"
  - expression: "0 6 * * *"
    params:
      mode: z
  - "0 6 * * *"
`;
    const result = applyEdit(text, {
      kind: 'setSchedule',
      expressions: ['0 6 * * *', '0 6 * * *'],
      removedIndex: 0,
    });
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.text).toBe(`schedule:
  - expression: "0 6 * * *"
    params:
      mode: z
  - "0 6 * * *"
`);
    }
  });

  // A removedIndex from a stale render can point past the list.
  it('writes no null entry for a stale removed index', () => {
    const text = `schedule:
  - "0 5 * * *"
  - expression: "0 6 * * *"
    params:
      mode: z
  - "0 7 * * *"
`;
    const result = applyEdit(text, {
      kind: 'setSchedule',
      expressions: ['0 5 * * *'],
      removedIndex: 5,
    });
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.text).not.toContain('null');
      expect(readSchedule(result.text).expressions).toEqual(['0 5 * * *']);
    }
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

  it('adds to the schedule of document 0 in a multi-document file', () => {
    const text = `schedule: "0 9 * * *"
steps:
  - name: a
    run: echo a
---
name: sub
steps:
  - name: b
    run: echo b
`;
    const next = set(text, ['0 9 * * *', '0 12 * * *']);
    expect(readSchedule(next).expressions).toEqual(['0 9 * * *', '0 12 * * *']);
    expect(next.slice(next.indexOf('---'))).toBe(
      text.slice(text.indexOf('---'))
    );
  });

  it('removes the key when the map form has only start', () => {
    const next = set(
      'schedule:\n  start: "0 8 * * *"\nsteps:\n  - echo a\n',
      []
    );
    expect(next).toBe('steps:\n  - echo a\n');
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

    const both = set(
      'schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n  restart: "0 12 * * *"\nsteps:\n  - echo a\n',
      []
    );
    expect(both).toBe(
      'schedule:\n  stop: "0 18 * * *"\n  restart: "0 12 * * *"\nsteps:\n  - echo a\n'
    );
  });

  // The start of the map form can be one object, such as a one-off run.
  it('keeps a start that is one object', () => {
    const text = 'schedule:\n  start:\n    at: 2030-01-01T00:00:00Z\n';
    const next = set(text, ['0 9 * * *']);
    expect(next).toContain('at: 2030-01-01T00:00:00Z');
    expect(readSchedule(next)).toMatchObject({
      present: true,
      expressions: ['0 9 * * *'],
      others: 1,
      entries: [{ schedule: { kind: 'at' } }, { expressionIndex: 0 }],
    });

    const result = applyEdit(text, { kind: 'setSchedule', expressions: [] });
    expect(result.ok).toBe(false);
  });

  it('refuses a change that changes nothing', () => {
    const result = applyEdit(BASE, { kind: 'setSchedule', expressions: [] });
    expect(result.ok).toBe(false);
  });
});

// The server reads a quoted string whatever the indent of its later lines.
describe('schedule of a file with a quoted string with unindented lines', () => {
  const text = `schedule: '0 1 * * *'\n${yamlUnindentedQuote}`;

  it('reads the schedule', () => {
    expect(readSchedule(text)).toMatchObject({
      present: true,
      expressions: ['0 1 * * *'],
      others: 0,
      entries: [{ expressionIndex: 0, params: {} }],
    });
  });

  it('sets the schedule and keeps the quoted string', () => {
    expect(set(text, ['5 4 * * *'])).toBe(
      `schedule: 5 4 * * *\n${yamlUnindentedQuote}`
    );
  });
});

function setParam(
  text: string,
  index: number,
  name: string,
  value: string | number | boolean | null
): string {
  const result = applyEdit(text, {
    kind: 'setScheduleParam',
    index,
    name,
    value,
  });
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  return result.text;
}

/** The overrides of each start entry, in the YAML order. */
function overrides(text: string): Array<Record<string, string> | null> {
  return readSchedule(text).entries.map((entry) => entry.params);
}

describe('schedule parameter overrides', () => {
  const PARAMETERIZED = `schedule:
  - expression: "0 5 * * *"
    params:
      mode: y
  - "0 6 * * *"
`;

  it('reads the overrides next to their expressions', () => {
    const view = readSchedule(PARAMETERIZED);
    expect(view.expressions).toEqual(['0 5 * * *', '0 6 * * *']);
    expect(view.entries.map((entry) => entry.params)).toEqual([
      { mode: 'y' },
      {},
    ]);
    expect(view.others).toBe(0);
  });

  it('turns a plain string into an object on its first override', () => {
    const next = setParam(PARAMETERIZED, 1, 'mode', 'z');
    expect(next).toBe(`schedule:
  - expression: "0 5 * * *"
    params:
      mode: y
  - expression: 0 6 * * *
    params:
      mode: z
`);
  });

  it('turns a lone string into a list entry', () => {
    const next = setParam('schedule: "0 6 * * *"\n', 0, 'mode', 'z');
    expect(readSchedule(next).expressions).toEqual(['0 6 * * *']);
    expect(overrides(next)).toEqual([{ mode: 'z' }]);
  });

  it('keeps an override that equals the default as a pinned value', () => {
    const next = setParam(PARAMETERIZED, 1, 'mode', 'manual');
    expect(overrides(next)[1]).toEqual({ mode: 'manual' });
  });

  it('clears one override and keeps the others and the order', () => {
    const text = `schedule:
  - expression: "0 5 * * *"
    params:
      mode: y
      level: "3"
  - "0 6 * * *"
`;
    const next = setParam(text, 0, 'mode', null);
    expect(overrides(next)).toEqual([{ level: '3' }, {}]);
    expect(readSchedule(next).expressions).toEqual(['0 5 * * *', '0 6 * * *']);
  });

  it('writes a plain string again when the last override goes', () => {
    const next = setParam(PARAMETERIZED, 0, 'mode', null);
    expect(next).toBe(`schedule:
  - 0 5 * * *
  - "0 6 * * *"
`);
  });

  // The index counts every start entry, as the server numbers schedules.
  it('sets and clears overrides of entries that are not plain cron', () => {
    const text = `schedule:
  - at: 2030-01-01T00:00:00Z
  - expression: "0 5 * * *"
    calendar: jp
    business_days: [1, -1]
  - expression: "0 6 * * *"
    profile: prod
  - "0 7 * * *"
`;
    let next = setParam(text, 0, 'mode', 'a');
    next = setParam(next, 1, 'mode', 'b');
    next = setParam(next, 2, 'mode', 'c');
    next = setParam(next, 3, 'mode', 'd');
    expect(next).toBe(`schedule:
  - at: 2030-01-01T00:00:00Z
    params:
      mode: a
  - expression: "0 5 * * *"
    calendar: jp
    business_days: [1, -1]
    params:
      mode: b
  - expression: "0 6 * * *"
    profile: prod
    params:
      mode: c
  - expression: 0 7 * * *
    params:
      mode: d
`);
    expect(overrides(next)).toEqual([
      { mode: 'a' },
      { mode: 'b' },
      { mode: 'c' },
      { mode: 'd' },
    ]);
    expect(setParam(next, 0, 'mode', null)).toContain(
      '  - at: 2030-01-01T00:00:00Z\n  - expression: "0 5 * * *"'
    );
  });

  // The backend coerces a quoted value too, but YAML written by hand
  // leaves numbers and booleans unquoted.
  it('writes numbers and booleans as YAML scalars', () => {
    let next = setParam('schedule: "0 6 * * *"\n', 0, 'count', 2);
    next = setParam(next, 0, 'dry_run', true);
    next = setParam(next, 0, 'label', '3');
    expect(next).toBe(`schedule:
  - expression: 0 6 * * *
    params:
      count: 2
      dry_run: true
      label: "3"
`);
  });

  it('reads overrides that are not plain values as not editable', () => {
    const text = `schedule:
  - at: 2030-01-01T00:00:00Z
    params:
      tags: [a, b]
`;
    expect(overrides(text)).toEqual([null]);
  });

  it('refuses an index that has no entry', () => {
    const result = applyEdit(PARAMETERIZED, {
      kind: 'setScheduleParam',
      index: 5,
      name: 'mode',
      value: 'z',
    });
    expect(result.ok).toBe(false);
  });

  it('keeps the overrides of the other entries when one is removed', () => {
    const next = set(PARAMETERIZED, ['0 6 * * *']);
    expect(next).toBe(`schedule:
  - "0 6 * * *"
`);
    const kept = applyEdit(PARAMETERIZED, {
      kind: 'setSchedule',
      expressions: ['0 5 * * *'],
      removedIndex: 1,
    });
    expect(kept.ok && overrides(kept.text)).toEqual([{ mode: 'y' }]);
  });

  // An entry with keys other than expression and params is not edited as
  // cron, but it is listed with its overrides.
  it('edits only entries with an expression and params as cron', () => {
    const text = `schedule:
  - expression: "0 5 * * *"
    calendar: none
  - expression: "0 5 * * *"
    profile: ""
  - kind: cron
    expression: "0 5 * * *"
  - expression: "0 5 * * *"
    params:
      mode: b
`;
    expect(readSchedule(text)).toEqual({
      present: true,
      expressions: ['0 5 * * *'],
      others: 3,
      entries: [
        {
          schedule: { kind: 'cron', expression: '0 5 * * *', calendar: 'none' },
          params: {},
        },
        {
          schedule: { kind: 'cron', expression: '0 5 * * *', profile: '' },
          params: {},
        },
        { schedule: { kind: 'cron', expression: '0 5 * * *' }, params: {} },
        {
          expressionIndex: 0,
          schedule: { kind: 'cron', expression: '0 5 * * *' },
          params: { mode: 'b' },
        },
      ],
    });
  });
});
