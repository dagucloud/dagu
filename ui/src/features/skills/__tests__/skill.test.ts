// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  editorNotices,
  filterSkills,
  NEW_SKILL_PATH,
  newSkillText,
  SKILL_TEMPLATE,
  skillNameProblem,
  stepSnippet,
} from '../skill';
import {
  readSkillForm,
  setSkillBody,
  setSkillKey,
  type SkillEditOutcome,
} from '../skillForm';

function text(outcome: SkillEditOutcome): string {
  if (!outcome.ok) throw new Error(outcome.error);
  return outcome.text;
}

function form(source: string) {
  const result = readSkillForm(source);
  if (!result.ok) throw new Error(result.error);
  return result.form;
}

const FILE =
  '---\nname: triage\n# why\ndescription: Sort the tickets\n---\n\n\n# Steps\n\n1. Read.\n';

describe('readSkillForm', () => {
  it('reads the front matter and the body as written', () => {
    expect(form(FILE)).toEqual({
      name: 'triage',
      description: 'Sort the tickets',
      body: '\n\n# Steps\n\n1. Read.\n',
      unknown: [],
    });
  });

  it('lists the keys that the format does not allow', () => {
    expect(
      form('---\nname: a\ndescription: d\nlicense: x\n---\nBody\n')
    ).toMatchObject({ unknown: [] });
    expect(
      form(
        '---\nname: a\ndescription: d\nallowed-tools: Read\nmetadata:\n  k: v\ncompatibility: c\nargument-hint: x\nmodel: y\n---\nBody\n'
      )
    ).toMatchObject({ unknown: ['argument-hint', 'model'] });
  });

  it('keeps the keys that the form does not edit when it sets one', () => {
    const source =
      '---\nname: a\ndescription: d\nlicense: MIT\nmetadata:\n  k: v\nallowed-tools: Read Bash\n---\nBody\n';
    const next = text(setSkillKey(source, 'description', 'Longer text'));
    expect(next).toBe(
      source.replace('description: d', 'description: Longer text')
    );
  });

  it('refuses a text without a front matter', () => {
    expect(readSkillForm('Body only\n')).toMatchObject({ ok: false });
    expect(readSkillForm('---\nname: a\n')).toMatchObject({ ok: false });
    expect(readSkillForm('---\nname: [\n---\nBody')).toMatchObject({
      ok: false,
    });
  });

  it('reads the template of a new skill', () => {
    expect(form(SKILL_TEMPLATE)).toMatchObject({
      name: '',
      description: expect.stringContaining('Use when'),
    });
    expect(form(SKILL_TEMPLATE).description.length).toBeGreaterThanOrEqual(20);
  });
});

describe('writing the skill text', () => {
  it('sets a key and keeps the comment, the other key and the body', () => {
    const next = text(setSkillKey(FILE, 'description', 'Sort: urgent first'));
    expect(next).toBe(
      '---\nname: triage\n# why\ndescription: "Sort: urgent first"\n---\n\n\n# Steps\n\n1. Read.\n'
    );
    expect(form(next).description).toBe('Sort: urgent first');
  });

  it('keeps a long description on one line', () => {
    const long = 'word '.repeat(60).trim();
    const next = text(setSkillKey(FILE, 'description', long));
    expect(next).toContain(`description: ${long}\n---`);
  });

  it('replaces the body and keeps the blank lines that start it', () => {
    const next = text(setSkillBody(FILE, '\n\nNew text'));
    expect(next).toBe(
      '---\nname: triage\n# why\ndescription: Sort the tickets\n---\n\n\nNew text'
    );
    expect(form(next).body).toBe('\n\nNew text');
  });

  it('round-trips a file unchanged when the form sends the same body', () => {
    const body = form(FILE).body;
    expect(text(setSkillBody(FILE, body))).toBe(FILE);
  });

  it('sets the name of the template and adds a key to an empty front matter', () => {
    expect(text(setSkillKey(SKILL_TEMPLATE, 'name', 'triage'))).toBe(
      SKILL_TEMPLATE.replace('name:', 'name: triage')
    );
    expect(text(setSkillKey('---\n---\nBody\n', 'name', 'a'))).toBe(
      '---\nname: a\n---\nBody\n'
    );
  });

  it('starts the body on its own line when the closing line has no break', () => {
    expect(text(setSkillBody('---\nname: a\n---', 'Hi'))).toBe(
      '---\nname: a\n---\nHi'
    );
  });

  it('reports a text that has no front matter', () => {
    expect(setSkillBody('no front matter', 'x')).toMatchObject({ ok: false });
    expect(setSkillKey('no front matter', 'name', 'x')).toMatchObject({
      ok: false,
    });
  });
});

describe('skill names and lists', () => {
  it('follows the name rule of the API', () => {
    expect(skillNameProblem('')).toBeNull();
    expect(skillNameProblem('triage-2')).toBeNull();
    expect(skillNameProblem('Triage')).toBe('pattern');
    expect(skillNameProblem('2fast')).toBeNull();
    expect(skillNameProblem('a--b')).toBe('pattern');
    expect(skillNameProblem('-a')).toBe('pattern');
    expect(skillNameProblem('a-')).toBe('pattern');
    expect(skillNameProblem('a'.repeat(64))).toBeNull();
    expect(skillNameProblem('a'.repeat(65))).toBe('length');
    expect(skillNameProblem('a_b')).toBe('pattern');
    expect(skillNameProblem('new')).toBe('reserved');
    expect(skillNameProblem('triage', ['triage'])).toBe('exists');
    for (const name of ['-abc', 'abc-', 'a b', 'a.b', 'a/b', 'é', 'Abc']) {
      expect(skillNameProblem(name)).toBe('pattern');
    }
  });

  it('filters by name or description', () => {
    const skills = [
      { name: 'triage', description: 'Sort tickets' },
      { name: 'refund', description: 'Pay back' },
    ];
    expect(filterSkills(skills, ' TICK ').map((s) => s.name)).toEqual([
      'triage',
    ]);
    expect(filterSkills(skills, '')).toHaveLength(2);
  });

  it('writes the usage snippet and the create path', () => {
    expect(stepSnippet('triage')).toBe('with:\n  skills: [triage]\n');
    expect(NEW_SKILL_PATH).toBe('/ai/skills/new');
  });

  it('writes the skeleton of a new skill, and quotes a name that YAML reads as a value', () => {
    expect(newSkillText('sales')).toBe(
      SKILL_TEMPLATE.replace('name:', 'name: sales')
    );
    expect(form(newSkillText('true')).name).toBe('true');
    expect(form(newSkillText('null')).name).toBe('null');
  });
});

describe('editorNotices', () => {
  it('shows the message of a failed save as an alert', () => {
    const notices = editorNotices({ saveError: 'The skill was not saved.' });
    expect(notices.map((n) => n.id)).toEqual(['save-error']);
    expect(notices[0]).toMatchObject({ tone: 'error', role: 'alert' });
  });

  it('has no notice for a clean save', () => {
    expect(editorNotices({ saveError: null })).toEqual([]);
  });
});
