// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { EditResult } from '../../contracts';
import { applyTextEdits } from '../../doc/patch';
import { editStepSkills, type SkillsChange } from '../edit';
import { readStepSkills } from '../skills';

function run(text: string, change: SkillsChange): string {
  const result: EditResult = editStepSkills(text, 'steps[0]', change);
  if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
  expect(applyTextEdits(text, result.edits)).toBe(result.text);
  return result.text;
}

const BASE = `# Support DAG
steps:
  - id: triage
    action: chat.completion
    with:
      prompt: Handle it
      skills:
        # first skill
        - refund-policy
        - tone # voice
  - id: other
    run: echo done
`;

describe('readStepSkills', () => {
  it('reads the names with their place', () => {
    const entries = readStepSkills({ with: { skills: ['a', ' b '] } });
    expect(entries).toEqual([
      { index: 0, name: 'a' },
      { index: 1, name: 'b' },
    ]);
  });

  it('marks an item that is not a name, and never throws', () => {
    const entries = readStepSkills({
      with: { skills: [{ skill: 'a' }, 4] },
    });
    expect(entries.map((item) => item.name)).toEqual(['', '']);
    expect(entries.map((item) => item.problem !== undefined)).toEqual([
      true,
      true,
    ]);
    expect(readStepSkills({})).toEqual([]);
    expect(readStepSkills({ with: { skills: 'a' } })).toEqual([]);
  });
});

describe('editStepSkills', () => {
  it('appends a name and keeps the comments', () => {
    const out = run(BASE, { kind: 'add', name: 'extra' });
    expect(out).toContain('# first skill');
    expect(out).toContain('# voice');
    expect(out).toContain('        - extra\n');
  });

  it('creates with.skills when it is absent', () => {
    const out = run(
      'steps:\n  - action: chat.completion\n    with:\n      prompt: x\n',
      {
        kind: 'add',
        name: 'a',
      }
    );
    expect(out).toContain('skills:');
    expect(out).toContain('- a');
  });

  it('removes an item, and the key with the last one', () => {
    const one = run(BASE, { kind: 'remove', index: 0 });
    expect(one).not.toContain('refund-policy');
    expect(one).toContain('- tone');
    const none = run(
      'steps:\n  - action: chat.completion\n    with:\n      skills:\n        - a\n',
      {
        kind: 'remove',
        index: 0,
      }
    );
    expect(none).not.toContain('skills');
  });

  it('fails for an index out of range', () => {
    const result = editStepSkills(BASE, 'steps[0]', {
      kind: 'remove',
      index: 9,
    });
    expect(result.ok).toBe(false);
  });

  it('fails when the skills are not a list', () => {
    const result = editStepSkills(
      'steps:\n  - action: chat.completion\n    with:\n      skills: a\n',
      'steps[0]',
      { kind: 'add', name: 'b' }
    );
    expect(result.ok).toBe(false);
  });
});
