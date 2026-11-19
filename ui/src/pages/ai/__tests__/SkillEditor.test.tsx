// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SkillFile } from '@/features/skills/skillIssues';
import { SkillEditor } from '../SkillEditor';

const mocks = vi.hoisted(() => ({
  detail: undefined as unknown,
  client: {},
  readSkill: vi.fn(),
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
  readSkill: mocks.readSkill,
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

// The editor of the open file. It is absent while the file loads.
const editorText = () =>
  screen.getByLabelText<HTMLTextAreaElement>('Markdown editor').value;

const written = (files: SkillFile[], hash: string) => ({
  ok: true,
  result: { hash, errors: [], warnings: [], files },
});

// Opens references/a.md, types a draft and saves it into a conflict.
async function conflictOnDraft() {
  load(VALID, [file('SKILL.md'), file('references/a.md')]);
  mocks.readSkillFileText.mockResolvedValue('saved\n');
  mocks.writeSkillFile.mockResolvedValue({
    ok: false,
    kind: 'conflict',
    hash: 'h9',
    message: 'changed',
  });
  renderEditor();
  fireEvent.click(screen.getByRole('button', { name: /a\.md/ }));
  await waitFor(() => expect(editorText()).toBe('saved\n'));
  fireEvent.change(await editor(), { target: { value: 'draft\n' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await screen.findByTestId('skill-conflict');
}

const reloadButton = () =>
  screen.getByRole('button', { name: 'Discard my changes and reload' });

beforeEach(() => {
  mocks.readSkill.mockReset();
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

  it('moves a draft with a renamed file and saves nothing of it', async () => {
    const before = [file('SKILL.md'), file('references/a.md')];
    load(VALID, before);
    mocks.readSkillFileText.mockResolvedValue('saved\n');
    mocks.writeSkillFile.mockResolvedValue(
      written([...before, file('references/b.md')], 'h2')
    );
    mocks.removeSkillFile.mockResolvedValue(
      written([file('SKILL.md'), file('references/b.md')], 'h3')
    );
    renderEditor();

    fireEvent.click(screen.getByRole('button', { name: /a\.md/ }));
    await waitFor(() => expect(editorText()).toBe('saved\n'));
    fireEvent.change(await editor(), { target: { value: 'draft\n' } });

    fireEvent.click(screen.getByRole('button', { name: 'Rename the file' }));
    fireEvent.change(screen.getByLabelText('Path in the skill'), {
      target: { value: 'references/b.md' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));

    await waitFor(() =>
      expect(screen.getByTestId('skill-active-file')).toHaveTextContent(
        'references/b.md'
      )
    );
    expect(mocks.writeSkillFile).toHaveBeenCalledTimes(1);
    expect(mocks.writeSkillFile.mock.calls[0]?.slice(2, 5)).toEqual([
      'references/b.md',
      'saved\n',
      'h1',
    ]);
    await waitFor(() => expect(editorText()).toBe('draft\n'));
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  });

  // The text that the editor saved belongs to the deleted file. A file
  // uploaded later at the same path is read again.
  it('reads a file uploaded at the path of a deleted file', async () => {
    const before = [file('SKILL.md'), file('references/a.md')];
    load(VALID, before);
    mocks.readSkillFileText
      .mockResolvedValueOnce('saved\n')
      .mockResolvedValueOnce('uploaded\n');
    mocks.writeSkillFile
      .mockResolvedValueOnce(written(before, 'h2'))
      .mockResolvedValueOnce(
        written(
          [file('SKILL.md'), { ...file('references/a.md'), sha256: 'new' }],
          'h4'
        )
      );
    mocks.removeSkillFile.mockResolvedValue(written([file('SKILL.md')], 'h3'));
    renderEditor();

    fireEvent.click(screen.getByRole('button', { name: /a\.md/ }));
    await waitFor(() => expect(editorText()).toBe('saved\n'));
    fireEvent.change(await editor(), { target: { value: 'edited\n' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    );

    fireEvent.click(screen.getByRole('button', { name: 'Delete the file' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() =>
      expect(screen.getByTestId('skill-active-file')).toHaveTextContent(
        'SKILL.md'
      )
    );

    fireEvent.click(screen.getByRole('button', { name: 'Upload a file' }));
    fireEvent.change(screen.getByLabelText('File'), {
      target: { files: [new File(['uploaded\n'], 'a.md')] },
    });
    fireEvent.change(screen.getByLabelText('Path in the skill'), {
      target: { value: 'references/a.md' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Upload' }));

    await waitFor(() => expect(editorText()).toBe('uploaded\n'));
    expect(mocks.readSkillFileText).toHaveBeenCalledTimes(2);
  });

  it('reads a file again after a failed read', async () => {
    load(VALID, [file('SKILL.md'), file('references/a.md')]);
    mocks.readSkillFileText
      .mockRejectedValueOnce(new Error('The server is busy.'))
      .mockResolvedValueOnce('saved\n');
    renderEditor();

    fireEvent.click(screen.getByRole('button', { name: /a\.md/ }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The server is busy.'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));

    await waitFor(() => expect(editorText()).toBe('saved\n'));
  });

  // Nothing was reloaded, so nothing of the draft or the conflict goes.
  it('keeps the draft and the conflict when the reload fails', async () => {
    mocks.readSkill.mockRejectedValue(new Error('The server is down.'));
    await conflictOnDraft();

    fireEvent.click(reloadButton());

    expect(await screen.findByText('The server is down.')).toBeInTheDocument();
    expect(screen.getByTestId('skill-conflict')).toBeInTheDocument();
    expect(editorText()).toBe('draft\n');
  });

  // The reload drops every draft: text typed meanwhile would go with them.
  it('locks the editor while the reload runs', async () => {
    let finish: (detail: unknown) => void = () => {};
    mocks.readSkill.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      })
    );
    await conflictOnDraft();

    fireEvent.click(reloadButton());
    await waitFor(() =>
      expect(screen.getByLabelText('Markdown editor')).toHaveAttribute(
        'readonly'
      )
    );

    finish(mocks.detail);
    await waitFor(() =>
      expect(screen.queryByTestId('skill-conflict')).not.toBeInTheDocument()
    );
    await waitFor(() => expect(editorText()).toBe('saved\n'));
    expect(screen.getByLabelText('Markdown editor')).not.toHaveAttribute(
      'readonly'
    );
  });

  // A skill can come from anyone: the preview loads no image, runs no block
  // and opens a linked file of the skill in the editor.
  it('previews Markdown without loading or running anything', async () => {
    load(
      `---\nname: triage\ndescription: ${DESCRIPTION}\n---\n` +
        'Read [the guide](references/a.md) and [the docs](https://example.com).\n\n' +
        '![chart](https://example.com/chart.png)\n\n' +
        '```dagu-run\nname: deploy\n```\n',
      [file('SKILL.md'), file('references/a.md')]
    );
    mocks.readSkillFileText.mockResolvedValue('# Guide\n');
    renderEditor();

    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    expect(document.querySelector('img')).toBeNull();
    expect(screen.getByText('chart')).toBeInTheDocument();
    expect(screen.getByText('name: deploy')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'the docs' })).toHaveAttribute(
      'target',
      '_blank'
    );

    fireEvent.click(screen.getByRole('button', { name: 'the guide' }));
    expect(screen.getByTestId('skill-active-file')).toHaveTextContent(
      'references/a.md'
    );
  });
});
