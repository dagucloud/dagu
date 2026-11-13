// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import AIPage from '..';

const mocks = vi.hoisted(() => ({
  client: { PUT: vi.fn(), DELETE: vi.fn(), GET: vi.fn(), POST: vi.fn() },
  detail: {} as Record<string, unknown>,
  mutate: vi.fn(),
  saved: [] as Array<{ blob: unknown; name: string }>,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: (path: string) => ({
    data: path === '/skills' ? { skills: [] } : mocks.detail,
    error: undefined,
    mutate: mocks.mutate,
  }),
}));

vi.mock('@/lib/download', () => ({
  downloadBlob: (blob: unknown, name: string) =>
    mocks.saved.push({ blob, name }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageSkills: () => true,
  useIsAdmin: () => false,
}));

// The stand-in of Monaco keeps the language that the editor chose.
vi.mock('@/components/editors/MarkdownEditor', () => ({
  default: ({
    value,
    onChange,
    language,
  }: {
    value: string;
    onChange?: (value?: string) => void;
    language?: string;
  }) => (
    <textarea
      aria-label="editor"
      data-language={language}
      value={value}
      onChange={(event) => onChange?.(event.target.value)}
    />
  ),
}));

const SKILL =
  '---\nname: triage\ndescription: Sort the tickets. Use when a ticket arrives.\n---\n# Steps\n';
const FILES = [
  { path: 'SKILL.md', size: SKILL.length, sha256: 'a', text: true },
  { path: 'references/guide.md', size: 9, sha256: 'b', text: true },
  { path: 'scripts/run.py', size: 14, sha256: 'd', text: true },
  { path: 'assets/logo.png', size: 2048, sha256: 'c', text: false },
];

const ok = (hash: string, files = FILES) => ({
  data: { hash, errors: [], warnings: [], files },
  response: { ok: true, status: 200 },
});
const fail = (status: number, error: Record<string, unknown>) => ({
  error,
  response: { ok: false, status },
});

function renderEditor() {
  return render(
    <AppBarContext.Provider
      value={{ setTitle: vi.fn(), selectedRemoteNode: 'edge' } as never}
    >
      <MemoryRouter initialEntries={['/ai/skills/triage']}>
        <Routes>
          <Route path="/ai/skills/:name" element={<AIPage />} />
          <Route path="*" element={<div>elsewhere</div>} />
        </Routes>
      </MemoryRouter>
    </AppBarContext.Provider>
  );
}

const editor = async () =>
  (await screen.findByLabelText('editor')) as HTMLTextAreaElement;
const save = () => screen.getByRole('button', { name: 'Save' });
const tree = () =>
  screen.getByRole('navigation', { name: 'Files of the skill' });
const open = (path: string) =>
  userEvent.click(
    within(tree())
      .getAllByRole('button')
      .find((button) => button.getAttribute('data-path') === path)!
  );

beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: /min-width/.test(query),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }))
  );
  mocks.saved = [];
  mocks.detail = {
    name: 'triage',
    content: SKILL,
    hash: 'h1',
    errors: [],
    warnings: [
      {
        severity: 'warning',
        rule: 'reference-depth',
        file: 'references/guide.md',
        message: 'a reference is deeper than one level',
      },
    ],
    files: FILES,
    summary: { name: 'triage', description: '' },
  };
  mocks.mutate.mockReset().mockResolvedValue(undefined);
  mocks.client.PUT.mockReset().mockResolvedValue(ok('h2'));
  mocks.client.DELETE.mockReset().mockResolvedValue(ok('h3'));
  mocks.client.POST.mockReset();
  mocks.client.GET.mockReset().mockImplementation(
    async (_path: string, init: { params: { query: { path: string } } }) => ({
      data:
        init.params.query.path === 'scripts/run.py'
          ? 'print("hi")\n'
          : '# Guide\n',
      response: { ok: true, status: 200 },
    })
  );
});

describe('file tree', () => {
  it('lists SKILL.md and the folders, marks a file that has a warning', () => {
    renderEditor();

    const files = within(tree());
    expect(files.getByText('Files (4)')).toBeVisible();
    for (const folder of ['references/', 'assets/', 'scripts/']) {
      expect(files.getByText(folder)).toBeVisible();
    }
    const guide = files
      .getAllByRole('button')
      .find((b) => b.getAttribute('data-path') === 'references/guide.md')!;
    expect(within(guide).getByRole('img', { name: 'Warning' })).toBeVisible();
    expect(screen.getByTestId('skill-active-file')).toHaveTextContent(
      'SKILL.md'
    );
  });

  it('opens a text file in the editor with the language of its extension', async () => {
    renderEditor();

    await open('scripts/run.py');
    const code = await editor();
    await waitFor(() => expect(code.value).toBe('print("hi")\n'));
    expect(code.dataset.language).toBe('python');
    expect(mocks.client.GET).toHaveBeenCalledWith(
      '/skills/{name}/files',
      expect.objectContaining({
        params: {
          path: { name: 'triage' },
          query: { remoteNode: 'edge', path: 'scripts/run.py' },
        },
      })
    );

    await open('references/guide.md');
    await waitFor(async () => expect((await editor()).value).toBe('# Guide\n'));
    expect((await editor()).dataset.language).toBe('markdown');
  });

  it('shows the name, the size and a download button for a binary file', async () => {
    mocks.client.GET.mockResolvedValue({
      data: new Blob(['png']),
      response: { ok: true, status: 200 },
    });
    renderEditor();

    await open('assets/logo.png');
    const view = await screen.findByTestId('skill-binary');
    expect(within(view).getByText('assets/logo.png')).toBeVisible();
    expect(within(view).getByText('2.0 KB')).toBeVisible();
    expect(screen.queryByLabelText('editor')).toBeNull();
    await userEvent.click(
      within(view).getByRole('button', { name: 'Download' })
    );
    await waitFor(() => expect(mocks.saved).toHaveLength(1));
    expect(mocks.saved[0]!.name).toBe('logo.png');
  });
});

describe('editing a file', () => {
  it('saves a reference with If-Match and the raw text, and chains the new hash', async () => {
    renderEditor();

    await open('references/guide.md');
    await waitFor(async () => expect((await editor()).value).toBe('# Guide\n'));
    await userEvent.type(await editor(), 'More.');
    expect(save()).toBeEnabled();
    await userEvent.click(save());

    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.PUT.mock.calls[0]!;
    expect(path).toBe('/skills/{name}/files');
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      path: 'references/guide.md',
    });
    expect(init.params.header).toEqual({ 'If-Match': 'h1' });
    expect(init.body).toBe('# Guide\nMore.');
    await waitFor(() => expect(save()).toBeDisabled());

    // The next save of another file carries the hash that the server gave.
    await open('scripts/run.py');
    await waitFor(async () =>
      expect((await editor()).value).toBe('print("hi")\n')
    );
    await userEvent.type(await editor(), '#');
    await userEvent.click(save());
    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(2));
    expect(mocks.client.PUT.mock.calls[1]![1].params.header).toEqual({
      'If-Match': 'h2',
    });
  });

  it('keeps the draft of a file on a conflict and tells the user', async () => {
    mocks.client.PUT.mockResolvedValue(
      fail(409, { message: 'changed', hash: 'ffff0000aaaa' })
    );
    renderEditor();

    await open('references/guide.md');
    await waitFor(async () => expect((await editor()).value).toBe('# Guide\n'));
    await userEvent.type(await editor(), 'Mine.');
    await userEvent.click(save());

    const banner = await screen.findByTestId('skill-conflict');
    expect(banner).toHaveTextContent('ffff0000');
    expect((await editor()).value).toBe('# Guide\nMine.');
    expect(
      within(banner).getByRole('button', { name: 'Copy my changes' })
    ).toBeVisible();
    // A file with a draft keeps its marker in the tree.
    expect(
      within(tree()).getByRole('img', { name: 'Unsaved changes' })
    ).toBeVisible();
  });

  it('shows a skill that was deleted meanwhile without a Keep button', async () => {
    mocks.client.PUT.mockResolvedValue(
      fail(409, { message: 'gone', hash: '' })
    );
    renderEditor();

    await userEvent.type(await editor(), 'x');
    await userEvent.click(save());

    const banner = await screen.findByTestId('skill-conflict');
    expect(banner).toHaveTextContent('no longer exists');
    expect(
      within(banner).queryByRole('button', { name: 'Keep my changes' })
    ).toBeNull();
  });
});

describe('adding, renaming and deleting files', () => {
  it('adds a text file with a checked path', async () => {
    mocks.client.PUT.mockResolvedValue(
      ok('h2', [
        ...FILES,
        { path: 'references/new.md', size: 0, sha256: 'n', text: true },
      ])
    );
    renderEditor();

    await userEvent.click(screen.getByRole('button', { name: 'Add a file' }));
    const dialog = await screen.findByRole('dialog');
    const path = within(dialog).getByRole('textbox', {
      name: 'Path in the skill',
    });
    const create = within(dialog).getByRole('button', { name: 'Create' });
    expect(create).toBeDisabled();

    await userEvent.clear(path);
    await userEvent.type(path, 'notes.md');
    expect(
      within(dialog).getByText(
        'The path must start with references/, assets/ or scripts/.'
      )
    ).toBeVisible();
    expect(create).toBeDisabled();
    await userEvent.clear(path);
    await userEvent.type(path, 'references/guide.md');
    expect(
      within(dialog).getByText('A file with this path exists.')
    ).toBeVisible();
    await userEvent.clear(path);
    await userEvent.type(path, 'references/new.md');
    await userEvent.click(create);

    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(1));
    const init = mocks.client.PUT.mock.calls[0]![1];
    expect(init.params.query.path).toBe('references/new.md');
    expect(init.params.header).toEqual({ 'If-Match': 'h1' });
    expect(init.body).toBe('');
    await waitFor(() =>
      expect(screen.getByTestId('skill-active-file')).toHaveTextContent(
        'references/new.md'
      )
    );
  });

  it('renames a file with a PUT of the new path and a DELETE of the old one on the new hash', async () => {
    renderEditor();

    await open('references/guide.md');
    await waitFor(async () => expect((await editor()).value).toBe('# Guide\n'));
    await userEvent.click(
      screen.getByRole('button', { name: 'Rename the file' })
    );
    const dialog = await screen.findByRole('dialog');
    const path = within(dialog).getByRole('textbox', {
      name: 'Path in the skill',
    });
    await userEvent.clear(path);
    await userEvent.type(path, 'references/manual.md');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Rename' })
    );

    await waitFor(() => expect(mocks.client.DELETE).toHaveBeenCalledTimes(1));
    const put = mocks.client.PUT.mock.calls[0]![1];
    expect(put.params.query.path).toBe('references/manual.md');
    expect(put.params.header).toEqual({ 'If-Match': 'h1' });
    expect(put.body).toBe('# Guide\n');
    const del = mocks.client.DELETE.mock.calls[0]![1];
    expect(del.params.query.path).toBe('references/guide.md');
    expect(del.params.header).toEqual({ 'If-Match': 'h2' });
  });

  it('says that the new file exists when the delete of the old one fails', async () => {
    mocks.client.DELETE.mockResolvedValue(fail(500, { message: 'disk error' }));
    renderEditor();

    await open('references/guide.md');
    await waitFor(async () => expect((await editor()).value).toBe('# Guide\n'));
    await userEvent.click(
      screen.getByRole('button', { name: 'Rename the file' })
    );
    const dialog = await screen.findByRole('dialog');
    const path = within(dialog).getByRole('textbox', {
      name: 'Path in the skill',
    });
    await userEvent.clear(path);
    await userEvent.type(path, 'references/manual.md');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Rename' })
    );

    expect(
      await screen.findByText(/The new file references\/manual\.md exists/)
    ).toBeVisible();
    expect(
      screen.getByText(/Delete references\/guide\.md by hand/)
    ).toBeVisible();
  });

  it('deletes a file after a confirmation, with the current hash', async () => {
    renderEditor();

    await open('assets/logo.png');
    await userEvent.click(
      screen.getByRole('button', { name: 'Delete the file' })
    );
    expect(mocks.client.DELETE).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/assets\/logo\.png/)).toBeVisible();
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Delete' })
    );

    await waitFor(() => expect(mocks.client.DELETE).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.DELETE.mock.calls[0]!;
    expect(path).toBe('/skills/{name}/files');
    expect(init.params.query.path).toBe('assets/logo.png');
    expect(init.params.header).toEqual({ 'If-Match': 'h1' });
  });

  it('downloads the open skill from the header', async () => {
    mocks.client.POST.mockResolvedValue({
      data: new Blob(['zip']),
      response: {
        ok: true,
        headers: new Headers({
          'Content-Disposition': 'attachment; filename="triage.zip"',
        }),
      },
    });
    renderEditor();

    await userEvent.click(
      screen.getByRole('button', { name: 'Download skill' })
    );
    await waitFor(() => expect(mocks.saved).toHaveLength(1));
    expect(mocks.saved[0]!.name).toBe('triage.zip');
    expect(mocks.client.POST.mock.calls[0]![1].body).toEqual({
      names: ['triage'],
    });
  });
});

describe('size of the body', () => {
  it('shows an estimate of the tokens and the lines with the markers', () => {
    mocks.detail = {
      ...mocks.detail,
      content: `---\nname: triage\ndescription: Sort the tickets. Use when needed.\n---\n${'x\n'.repeat(600)}`,
    };
    renderEditor();

    const items = screen.getAllByTestId('skill-meter-item');
    expect(items[0]).toHaveTextContent('about 325 tokens');
    expect(items[1]).toHaveTextContent('600 lines');
    expect(items[0]).not.toHaveAttribute('data-over');
    expect(items[1]).toHaveAttribute('data-over', 'true');
    expect(screen.getAllByTestId('skill-meter-marker')).toHaveLength(2);
  });
});
