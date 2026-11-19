// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import type { DagEditOp } from '../../contracts';
import { parseDagDoc } from '../../doc';
import { SkillsDock, type SkillsDockProps } from '../SkillsDock';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const SKILLS = [
  {
    name: 'code-style',
    description: 'Follow the code style of the project.',
    valid: true,
    errors: [],
    warnings: [],
  },
  {
    name: 'tone',
    description: 'Answer in a calm tone.',
    valid: true,
    errors: [],
    warnings: [],
  },
  {
    name: 'broken',
    description: '',
    valid: false,
    errors: [{ message: 'The name does not match the folder.', rule: 'name' }],
    warnings: [],
  },
];

function serve(skills: typeof SKILLS | null = SKILLS) {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (path: string) =>
      path === '/skills' && skills !== null ? { data: { skills } } : {}
  );
}

afterEach(() => vi.clearAllMocks());

const AGENT = `steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: fix it
      skills:
        - code-style
        - missing
`;

function dock(
  over: Partial<SkillsDockProps> & { skills?: typeof SKILLS | null } = {}
) {
  serve(over.skills);
  const model = parseDagDoc(AGENT);
  const step = model.steps[0]!;
  const onEdit = vi.fn<(op: DagEditOp) => void>();
  const onClose = vi.fn();
  render(
    <SkillsDock
      step={step}
      stepLabel="code"
      fieldIssues={{}}
      locked={false}
      adding={false}
      onEdit={onEdit}
      onClose={onClose}
      {...over}
    />
  );
  return { onEdit, onClose, step };
}

const search = () => screen.getByRole('combobox', { name: 'Search skills' });
const rows = () => screen.queryAllByTestId('ve-skill-row');
const key = (name: string, init: Record<string, unknown> = {}) =>
  fireEvent.keyDown(search(), { key: name, ...init });

describe('the Skills dock', () => {
  it('lists the skills of the step, then the skills that it can add', () => {
    dock();
    expect(rows().map((row) => row.dataset.row)).toEqual([
      'step:0',
      'step:1',
      'catalog:tone',
      'catalog:broken',
    ]);
    expect(rows()[0]).toHaveTextContent('Follow the code style');
    // A skill that the folder does not have is flagged.
    expect(rows()[1]).toHaveTextContent('Not in the skills folder');
    // A skill with an error shows its reason and cannot be added.
    expect(rows()[3]).toHaveTextContent('Error: The name does not match');
    expect(search()).toHaveFocus();
  });

  it('puts the skills that it can add first when the dock opens to add', () => {
    dock({ adding: true });
    expect(rows().map((row) => row.dataset.row)).toEqual([
      'catalog:tone',
      'catalog:broken',
      'step:0',
      'step:1',
    ]);
    expect(screen.getByTestId('ve-dock-detail-body')).toHaveTextContent('tone');
  });

  it('filters by name and description, and offers the typed name', () => {
    dock();
    fireEvent.change(search(), { target: { value: 'calm' } });
    expect(rows().map((row) => row.dataset.row)).toEqual(['catalog:tone']);
    fireEvent.change(search(), { target: { value: 'unknown-skill' } });
    // The list can be missing a skill: the typed name is the item.
    expect(rows().map((row) => row.dataset.row)).toEqual([
      'typed:unknown-skill',
    ]);
    expect(rows()[0]).toHaveTextContent('Add "unknown-skill"');
    // A name that the step lists is not offered again.
    fireEvent.change(search(), { target: { value: 'missing' } });
    expect(rows().map((row) => row.dataset.row)).toEqual(['step:1']);
    fireEvent.change(search(), { target: { value: 'zzz yyy' } });
    expect(screen.getByRole('listbox')).toHaveTextContent(
      'No skill matches the search.'
    );
  });

  it('adds with Enter, from the row and from the detail, with the editor operation', () => {
    const { onEdit, step } = dock({ adding: true });
    key('Enter');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepSkills',
      step: step.key,
      change: { kind: 'add', name: 'tone' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add to code' }));
    expect(onEdit).toHaveBeenCalledTimes(2);
  });

  it('refuses a skill that has an error', () => {
    const { onEdit } = dock({ adding: true });
    key('ArrowDown');
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The server refuses this skill: The name does not match the folder.'
    );
    expect(screen.getByRole('button', { name: 'Add to code' })).toBeDisabled();
    key('Enter');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('removes a skill with Delete and from the detail', () => {
    const { onEdit, step } = dock();
    key('Delete');
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'editStepSkills',
      step: step.key,
      change: { kind: 'remove', index: 0 },
    });
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove skill code-style' })
    );
    expect(onEdit).toHaveBeenCalledTimes(2);
  });

  it('shows the description and a link to the page of the skill in the detail', () => {
    dock();
    const detail = within(screen.getByTestId('ve-dock-detail'));
    expect(
      detail.getByText('Follow the code style of the project.')
    ).toBeVisible();
    expect(
      detail.getByRole('link', { name: /Open the skill/ })
    ).toHaveAttribute('href', '/ai/skills/code-style');
    expect(
      detail.getByText('The CLI reads a skill with its tool Skill.')
    ).toBeVisible();
  });

  it('keeps a free name field when the list cannot be loaded', () => {
    dock({ skills: null });
    expect(screen.getByTestId('ve-add-skill-note')).toHaveTextContent(
      'The list of skills could not be loaded. Type a name instead.'
    );
    fireEvent.change(search(), { target: { value: 'tone' } });
    expect(rows().map((row) => row.dataset.row)).toEqual(['typed:tone']);
  });

  it('only reads in a view that only reads', () => {
    const { onEdit } = dock({ locked: true });
    expect(screen.queryByRole('button', { name: /^Remove/ })).toBeNull();
    key('Delete');
    fireEvent.change(search(), { target: { value: 'tone' } });
    key('Enter');
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('clears the search with Escape, then closes', () => {
    const { onClose } = dock();
    fireEvent.change(search(), { target: { value: 'tone' } });
    key('Escape');
    expect(search()).toHaveValue('');
    expect(onClose).not.toHaveBeenCalled();
    key('Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
