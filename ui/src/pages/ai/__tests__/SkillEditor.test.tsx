// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SkillFile } from '@/features/skills/skillIssues';
import { SkillEditor } from '../SkillEditor';

const mocks = vi.hoisted(() => ({
  detail: undefined as unknown,
  client: {},
  readSkillFileText: vi.fn(),
  writeSkillFile: vi.fn(),
  removeSkillFile: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: () => ({
    data: mocks.detail,
    error: undefined,
    mutate: async () => mocks.detail,
  }),
}));

vi.mock('@/features/skills/skillSync', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/skills/skillSync')>()),
  readSkillFileText: mocks.readSkillFileText,
  writeSkillFile: mocks.writeSkillFile,
  removeSkillFile: mocks.removeSkillFile,
}));

// Monaco does not run in jsdom: a text area stands in for it.
vi.mock('@/components/editors/MarkdownEditor', () => ({
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange?: (value: string) => void;
  }) => (
    <textarea
      aria-label="Markdown editor"
      value={value}
      readOnly={!onChange}
      onChange={(event) => onChange?.(event.target.value)}
    />
  ),
}));

const DESCRIPTION = 'Sort the tickets. Use when a ticket arrives.';
const VALID = `---\nname: triage\ndescription: ${DESCRIPTION}\n---\nBody\n`;

const file = (path: string, text = true): SkillFile => ({
  path,
  size: 10,
  sha256: `sha-${path}`,
  text,
});

function load(content: string, files: SkillFile[] = [file('SKILL.md')]) {
  mocks.detail = {
    name: 'triage',
    content,
    hash: 'h1',
    errors: [],
    warnings: [],
    files,
  };
}

function renderEditor() {
  render(
    <MemoryRouter>
      <SkillEditor
        name="triage"
        remoteNode="local"
        canWrite
        narrow={false}
        onChanged={() => {}}
      />
    </MemoryRouter>
  );
}

const editor = () =>
  screen.findByLabelText<HTMLTextAreaElement>('Markdown editor');

beforeEach(() => {
  mocks.readSkillFileText.mockReset();
  mocks.writeSkillFile.mockReset();
  mocks.removeSkillFile.mockReset();
});

describe('SkillEditor', () => {
  // A front matter with a key that the form cannot edit opens as the whole
  // file. Fixing it must not swap the editor to the body under the cursor.
  it('keeps the whole file while an edit makes the front matter valid', async () => {
    load(
      `---\nname: triage\nmodel: x\ndescription: ${DESCRIPTION}\n---\nBody\n`
    );
    renderEditor();
    expect((await editor()).value).toContain('model: x');

    fireEvent.change(await editor(), { target: { value: VALID } });
    expect((await editor()).value).toBe(VALID);

    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the front matter' })
    );
    expect((await editor()).value).toBe('Body\n');
  });

  // The server refuses a SKILL.md with an empty body, so the draft must not
  // read as valid.
  it('shows the errors that a save of the draft would get', async () => {
    load(VALID);
    renderEditor();
    expect(screen.getByText('Valid')).toBeInTheDocument();

    fireEvent.change(await editor(), { target: { value: '  \n' } });
    expect(screen.queryByText('Valid')).not.toBeInTheDocument();
    expect(screen.getByText('The Markdown body is empty.')).toBeInTheDocument();
    expect(
      screen.getByText('A step cannot use this skill until you fix its errors.')
    ).toBeInTheDocument();
  });
});
