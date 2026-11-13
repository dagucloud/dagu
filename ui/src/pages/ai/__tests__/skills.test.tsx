// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import AIPage from '..';

const mocks = vi.hoisted(() => ({
  canWrite: true,
  isAdmin: false,
  client: { PUT: vi.fn(), DELETE: vi.fn(), GET: vi.fn(), POST: vi.fn() },
  saved: [] as Array<{ blob: unknown; name: string }>,
  list: {} as Record<string, unknown>,
  details: {} as Record<string, Record<string, unknown>>,
  mutate: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  // A query that a test does not set stays loading.
  useQuery: (
    path: string,
    init: { params?: { path?: { name?: string } } } | null
  ) => {
    const found =
      path === '/skills'
        ? mocks.list
        : init === null
          ? undefined
          : mocks.details[init.params?.path?.name ?? ''];
    return {
      data: undefined,
      error: undefined,
      mutate: mocks.mutate,
      ...found,
    };
  },
}));

vi.mock('@/lib/download', () => ({
  downloadBlob: (blob: unknown, name: string) =>
    mocks.saved.push({ blob, name }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageSkills: () => mocks.canWrite,
  useIsAdmin: () => mocks.isAdmin,
}));

// The real editor loads Monaco. The stand-in keeps the props that matter.
vi.mock('@/components/editors/MarkdownEditor', () => ({
  default: ({
    value,
    onChange,
    readOnly,
  }: {
    value: string;
    onChange?: (value?: string) => void;
    readOnly?: boolean;
  }) => (
    <textarea
      aria-label="SKILL.md"
      value={value}
      readOnly={readOnly}
      onChange={(event) => onChange?.(event.target.value)}
    />
  ),
}));

const CONTENT =
  '---\nname: triage\ndescription: Sort the tickets\nargument-hint: x\n---\n\n\n# Steps\n\n1. Read.\n';
const CLEAN =
  '---\nname: triage\ndescription: Sort the tickets\n---\n\n\n# Steps\n\n1. Read.\n';
const REFUND =
  '---\nname: refund\ndescription: Pay a customer back\n---\n# Refund\n';

const issue = (
  severity: 'error' | 'warning',
  rule: string,
  message: string,
  file = 'SKILL.md'
) => ({ severity, rule, file, message });

const EMPTY = issue('error', 'description-length', 'the description is empty');
const UNKNOWN = issue(
  'error',
  'front-matter-key',
  'the front matter key "model" is not allowed',
  'SKILL.md'
);
const ROOT = issue(
  'error',
  'root-entry',
  'README.md is not allowed at the root',
  'README.md'
);
const SHORT = issue(
  'warning',
  'description-short',
  'the description has 5 characters'
);

const skills = [
  {
    name: 'refund',
    description: 'Pay a customer back',
    hash: 'r1',
    valid: true,
    errors: [],
    warnings: [],
  },
  {
    name: 'triage',
    description: 'Sort the tickets',
    hash: 'h1',
    valid: false,
    errors: [EMPTY, UNKNOWN, ROOT],
    warnings: [],
  },
  {
    name: 'short',
    description: 'Short',
    hash: 's1',
    valid: true,
    errors: [],
    warnings: [SHORT],
  },
];

const FILES = [
  { path: 'SKILL.md', size: 90, sha256: 'a', text: true },
  { path: 'references/guide.md', size: 12, sha256: 'b', text: true },
  { path: 'assets/logo.png', size: 2048, sha256: 'c', text: false },
];

function detail(
  name: string,
  content: string,
  errors: unknown[] = [],
  hash = 'h1'
) {
  return {
    data: {
      name,
      content,
      hash,
      errors,
      warnings: [],
      files: FILES,
      summary: { name, description: '' },
    },
  };
}

const ok = (hash = 'h2', errors: unknown[] = []) => ({
  data: { hash, errors, warnings: [], files: FILES },
  response: { ok: true, status: 200 },
});
const failure = (status: number, error: Record<string, unknown>) => ({
  error,
  response: { ok: false, status },
});

/** A matchMedia that answers min-width queries for a viewport width. */
function setViewport(width: number) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => {
      const min = /min-width:\s*(\d+)px/.exec(query);
      return {
        matches: min ? width >= Number(min[1]) : false,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      };
    })
  );
}

/** A viewport whose width a test changes, with the change events of matchMedia. */
function resizableViewport(width: number) {
  const listeners = new Set<() => void>();
  let current = width;
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => {
      const min = /min-width:\s*(\d+)px/.exec(query);
      return {
        get matches() {
          return min ? current >= Number(min[1]) : false;
        },
        media: query,
        addEventListener: (_: string, listener: () => void) =>
          listeners.add(listener),
        removeEventListener: (_: string, listener: () => void) =>
          listeners.delete(listener),
      };
    })
  );
  return {
    resize(next: number) {
      current = next;
      for (const listener of [...listeners]) listener();
    },
  };
}

function Where() {
  const { pathname } = useLocation();
  return <output data-testid="where">{pathname}</output>;
}

function renderAt(path: string) {
  return render(
    <AppBarContext.Provider
      value={{ setTitle: vi.fn(), selectedRemoteNode: 'edge' } as never}
    >
      <MemoryRouter initialEntries={[path]}>
        <Where />
        <Routes>
          <Route path="/ai" element={<AIPage />} />
          <Route path="/ai/endpoints" element={<div>endpoints</div>} />
          <Route path="/ai/skills" element={<AIPage />} />
          <Route path="/ai/skills/:name" element={<AIPage />} />
          <Route path="*" element={<div>elsewhere</div>} />
        </Routes>
      </MemoryRouter>
    </AppBarContext.Provider>
  );
}

const where = () => screen.getByTestId('where').textContent;
const editor = async () =>
  (await screen.findByLabelText('SKILL.md')) as HTMLTextAreaElement;
const save = () => screen.getByRole('button', { name: 'Save' });
const descriptionField = () =>
  screen.getByRole('textbox', {
    name: 'What the skill is for',
  }) as HTMLInputElement;

beforeEach(() => {
  setViewport(1440);
  mocks.canWrite = true;
  mocks.isAdmin = false;
  mocks.list = { data: { skills } };
  mocks.details = {
    triage: detail('triage', CLEAN),
    refund: detail('refund', REFUND),
  };
  mocks.saved = [];
  mocks.mutate.mockReset().mockResolvedValue(undefined);
  mocks.client.PUT.mockReset().mockResolvedValue(ok());
  mocks.client.DELETE.mockReset().mockResolvedValue({
    response: { ok: true, status: 204 },
  });
  mocks.client.GET.mockReset().mockResolvedValue({
    data: '# Guide\n',
    response: { ok: true, status: 200 },
  });
  mocks.client.POST.mockReset().mockResolvedValue({
    data: new Blob(['zip']),
    response: {
      ok: true,
      status: 200,
      headers: new Headers({
        'Content-Disposition': 'attachment; filename="skills-2026-11-05.zip"',
      }),
    },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('layout', () => {
  it('puts the list and the selected skill side by side on a wide viewport', async () => {
    renderAt('/ai/skills/triage');

    const list = screen.getByRole('list', { name: 'Skills' });
    expect(within(list).getAllByRole('link')).toHaveLength(3);
    expect(within(list).getByRole('link', { name: /triage/ })).toHaveAttribute(
      'aria-current',
      'page'
    );
    expect(
      within(list).getByRole('link', { name: /refund/ })
    ).not.toHaveAttribute('aria-current');
    expect(
      screen.getByRole('heading', { level: 2, name: 'triage' })
    ).toBeVisible();
    expect(descriptionField()).toHaveValue('Sort the tickets');
    // The editor shows the body of the file, not the front matter.
    expect((await editor()).value).toBe('\n\n# Steps\n\n1. Read.\n');
    expect(
      screen.queryByRole('link', { name: 'Back to the skills' })
    ).toBeNull();
  });

  it('shows what a skill is, and a New skill button, when none is selected', () => {
    renderAt('/ai/skills');

    expect(screen.getByRole('list', { name: 'Skills' })).toBeVisible();
    expect(screen.getByText('No skill selected')).toBeVisible();
    expect(
      screen.getByText(
        'A skill is a set of instructions. The model reads it only when it needs it.'
      )
    ).toBeVisible();
    expect(screen.queryByLabelText('SKILL.md')).toBeNull();
    expect(screen.getAllByRole('link', { name: 'New skill' })).toHaveLength(2);
  });

  it('shows the list alone on a narrow viewport', () => {
    setViewport(390);
    renderAt('/ai/skills');

    expect(screen.getByRole('list', { name: 'Skills' })).toBeVisible();
    expect(screen.queryByText('No skill selected')).toBeNull();
    expect(screen.queryByLabelText('SKILL.md')).toBeNull();
  });

  it('shows the editor alone, with a Back link, on a narrow viewport', async () => {
    setViewport(390);
    renderAt('/ai/skills/triage');

    expect(await editor()).toBeVisible();
    expect(screen.queryByRole('list', { name: 'Skills' })).toBeNull();
    await userEvent.click(
      screen.getByRole('link', { name: 'Back to the skills' })
    );
    expect(where()).toBe('/ai/skills');
    expect(screen.getByRole('list', { name: 'Skills' })).toBeVisible();
  });

  it('selects a skill from the list and puts it in the address', async () => {
    renderAt('/ai/skills');

    await userEvent.click(screen.getByRole('link', { name: /refund/ }));
    expect(where()).toBe('/ai/skills/refund');
    expect((await editor()).value).toBe('# Refund\n');
    expect(descriptionField()).toHaveValue('Pay a customer back');
  });

  it('keeps the AI tabs for an administrator', () => {
    mocks.isAdmin = true;
    renderAt('/ai/skills/triage');

    expect(screen.getByRole('tab', { name: 'Skills' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Endpoints',
      'MCP',
      'Skills',
    ]);
  });
});

describe('list', () => {
  it('shows the ERROR state of an invalid skill with its violations in the tooltip', async () => {
    renderAt('/ai/skills');

    const badge = screen.getByText('Error', { selector: '[data-state-kind]' });
    expect(badge).toHaveAttribute('data-state-kind', 'error');
    await userEvent.hover(badge);
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('the description is empty');
    expect(tip).toHaveTextContent(
      'the front matter key "model" is not allowed'
    );
    expect(tip).toHaveTextContent('README.md: README.md is not allowed');
  });

  it('marks a skill that has only warnings, and shows nothing on a valid skill', async () => {
    renderAt('/ai/skills');

    const warning = screen.getByText('Warning', {
      selector: '[data-state-kind]',
    });
    expect(warning).toHaveAttribute('data-state-kind', 'warning');
    await userEvent.hover(warning);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'the description has 5 characters'
    );
    const refund = screen.getByRole('link', { name: /refund/ });
    expect(within(refund).queryByText(/Error|Warning|Valid/)).toBeNull();
  });

  it('filters by name or description', async () => {
    renderAt('/ai/skills');

    const search = screen.getByRole('searchbox');
    await userEvent.type(search, 'customer');
    const list = screen.getByRole('list', { name: 'Skills' });
    expect(within(list).getAllByRole('link')).toHaveLength(1);
    await userEvent.clear(search);
    await userEvent.type(search, 'zzz');
    expect(screen.getByText('No skill matches "zzz"')).toBeVisible();
  });

  it('selects rows and all rows, and downloads the ZIP with the name of the server', async () => {
    renderAt('/ai/skills');

    const download = screen.getByRole('button', { name: 'Download' });
    expect(download).toBeDisabled();
    await userEvent.click(
      screen.getByRole('checkbox', { name: 'Select skill refund' })
    );
    expect(screen.getByText('1 selected')).toBeVisible();
    expect(download).toBeEnabled();

    await userEvent.click(
      screen.getByRole('checkbox', { name: 'Select all skills' })
    );
    expect(screen.getByText('3 selected')).toBeVisible();
    await userEvent.click(download);

    await waitFor(() => expect(mocks.saved).toHaveLength(1));
    expect(mocks.saved[0]!.name).toBe('skills-2026-11-05.zip');
    const [path, init] = mocks.client.POST.mock.calls[0]!;
    expect(path).toBe('/skills/download');
    expect(init.body).toEqual({ names: ['refund', 'triage', 'short'] });
    expect(init.params.query).toEqual({ remoteNode: 'edge' });

    // A second click on the select-all box clears the selection.
    await userEvent.click(
      screen.getByRole('checkbox', { name: 'Select all skills' })
    );
    expect(download).toBeDisabled();
  });

  it('shows the message of a download that failed', async () => {
    mocks.client.POST.mockResolvedValue({
      error: { message: 'too large' },
      response: { ok: false, status: 400, headers: new Headers() },
    });
    renderAt('/ai/skills');

    await userEvent.click(
      screen.getByRole('checkbox', { name: 'Select skill refund' })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Download' }));

    expect(await screen.findByText('too large')).toBeVisible();
    expect(mocks.saved).toHaveLength(0);
  });

  it('shows the error of a list that did not load, and retries', async () => {
    mocks.list = { data: undefined, error: new Error('boom') };
    renderAt('/ai/skills');

    expect(await screen.findByText("Couldn't load the skills")).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(mocks.mutate).toHaveBeenCalled();
  });

  it('shows a skill that does not exist, and a skill that did not load', async () => {
    mocks.details = {
      gone: { data: undefined, error: { status: 404, message: 'no' } },
      broken: { data: undefined, error: new Error('boom') },
    };
    renderAt('/ai/skills/gone');
    expect(await screen.findByText('Skill not found')).toBeVisible();
    expect(screen.getByRole('list', { name: 'Skills' })).toBeVisible();
  });

  it('shows the error of a skill that did not load', async () => {
    mocks.details = {
      broken: { data: undefined, error: new Error('boom') },
    };
    renderAt('/ai/skills/broken');
    expect(await screen.findByText("Couldn't load the skill")).toBeVisible();
  });
});

describe('editor', () => {
  it('edits the body and the description, keeps the other lines, and saves', async () => {
    renderAt('/ai/skills/triage');
    expect(save()).toBeDisabled();
    expect(screen.queryByRole('img', { name: 'Unsaved changes' })).toBeNull();

    const field = descriptionField();
    await userEvent.clear(field);
    await userEvent.type(field, 'Sort: urgent first');
    await userEvent.type(await editor(), 'More.');

    expect(
      screen.getAllByRole('img', { name: 'Unsaved changes' }).length
    ).toBeGreaterThan(0);
    await userEvent.click(save());

    const expected =
      '---\nname: triage\ndescription: "Sort: urgent first"\n---\n\n\n# Steps\n\n1. Read.\nMore.';
    await waitFor(() => expect(mocks.mutate).toHaveBeenCalled());
    expect(mocks.client.PUT).toHaveBeenCalledWith('/skills/{name}', {
      params: {
        path: { name: 'triage' },
        query: { remoteNode: 'edge' },
        header: { 'If-Match': 'h1' },
      },
      body: { content: expected },
    });
    await waitFor(() => expect(save()).toBeDisabled());
  });

  it('saves with Ctrl+S', async () => {
    renderAt('/ai/skills/triage');

    await userEvent.type(await editor(), 'x');
    await userEvent.keyboard('{Control>}s{/Control}');

    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(1));
  });

  it('reverts the draft to the saved text', async () => {
    renderAt('/ai/skills/triage');

    await userEvent.type(await editor(), 'x');
    await userEvent.type(descriptionField(), 'y');
    await userEvent.click(
      screen.getByRole('button', { name: 'Revert changes' })
    );

    expect((await editor()).value).toBe('\n\n# Steps\n\n1. Read.\n');
    expect(descriptionField()).toHaveValue('Sort the tickets');
    expect(save()).toBeDisabled();
  });

  it('shows the rules of a refused save in the validation panel and keeps the draft', async () => {
    mocks.client.PUT.mockResolvedValue(
      failure(400, {
        message: 'skill "triage" breaks the format',
        issues: [EMPTY],
      })
    );
    renderAt('/ai/skills/triage');

    await userEvent.clear(descriptionField());
    expect(screen.getByText('A description is required.')).toBeVisible();
    await userEvent.click(save());

    const panel = await screen.findByRole('region', {
      name: 'The server refused the save',
    });
    expect(within(panel).getByText('the description is empty')).toBeVisible();
    expect(within(panel).getByText('description-length')).toBeVisible();
    expect(mocks.mutate).not.toHaveBeenCalled();
    expect(descriptionField()).toHaveValue('');
    expect(save()).toBeEnabled();
  });

  it('keeps the draft on a conflict, shows the banner, and keeps or reloads on request', async () => {
    mocks.client.PUT.mockResolvedValueOnce(
      failure(409, { message: 'changed', hash: 'abcdef123456' })
    );
    renderAt('/ai/skills/triage');

    await userEvent.type(await editor(), 'Mine.');
    await userEvent.click(save());

    const banner = await screen.findByTestId('skill-conflict');
    expect(banner).toHaveTextContent('The skill changed on the server.');
    expect(banner).toHaveTextContent('abcdef12');
    // The draft stays, and the save waits for a choice.
    expect((await editor()).value).toMatch(/Read\.\nMine\.$/);
    expect(save()).toBeDisabled();
    expect(mocks.mutate).not.toHaveBeenCalled();

    // Keep: the hash of the server becomes the precondition of the next save.
    await userEvent.click(
      within(banner).getByRole('button', { name: 'Keep my changes' })
    );
    expect(screen.queryByTestId('skill-conflict')).toBeNull();
    expect((await editor()).value).toMatch(/Mine\.$/);
    await userEvent.click(save());
    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(2));
    expect(mocks.client.PUT.mock.calls[1]![1].params.header).toEqual({
      'If-Match': 'abcdef123456',
    });
  });

  it('reloads the server version when the user discards the draft after a conflict', async () => {
    mocks.client.PUT.mockResolvedValueOnce(
      failure(409, { message: 'changed', hash: 'abcdef123456' })
    );
    renderAt('/ai/skills/triage');

    await userEvent.type(await editor(), 'Mine.');
    await userEvent.click(save());
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Discard my changes and reload',
      })
    );

    await waitFor(() => expect(mocks.mutate).toHaveBeenCalled());
    expect(screen.queryByTestId('skill-conflict')).toBeNull();
    expect((await editor()).value).toBe('\n\n# Steps\n\n1. Read.\n');
  });

  // The save waits for the server: the text that the user types meanwhile
  // stays a draft, and the next save sends it.
  it('keeps the text that the user types during a save', async () => {
    let finish: (value: unknown) => void = () => undefined;
    mocks.client.PUT.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      })
    );
    renderAt('/ai/skills/triage');

    await userEvent.type(await editor(), 'a');
    await userEvent.click(save());
    await userEvent.type(await editor(), 'b');
    await act(async () => finish(ok()));

    await waitFor(() => expect(mocks.mutate).toHaveBeenCalled());
    expect((await editor()).value).toMatch(/Read\.\nab$/);
    await waitFor(() => expect(save()).toBeEnabled());
    expect(
      screen.getAllByRole('img', { name: 'Unsaved changes' }).length
    ).toBeGreaterThan(0);
  });

  it('chains the new hash into the next save', async () => {
    renderAt('/ai/skills/triage');

    await userEvent.type(await editor(), 'a');
    await userEvent.click(save());
    await waitFor(() => expect(save()).toBeDisabled());
    await userEvent.type(await editor(), 'b');
    await userEvent.click(save());

    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(2));
    expect(mocks.client.PUT.mock.calls[0]![1].params.header).toEqual({
      'If-Match': 'h1',
    });
    expect(mocks.client.PUT.mock.calls[1]![1].params.header).toEqual({
      'If-Match': 'h2',
    });
  });

  it('lists the errors and the warnings of the whole skill in the validation panel', () => {
    mocks.details.triage = {
      data: {
        ...detail('triage', CLEAN, [EMPTY, ROOT]).data,
        warnings: [SHORT],
      },
    };
    renderAt('/ai/skills/triage');

    const panel = screen.getByRole('region', { name: 'Validation' });
    expect(within(panel).getByText('Errors: 2')).toBeVisible();
    expect(within(panel).getByText('Warnings: 1')).toBeVisible();
    expect(within(panel).getByText('the description is empty')).toBeVisible();
    expect(within(panel).getByText('description-short')).toBeVisible();
    // The errors come before the warnings, grouped by file.
    const files = within(panel)
      .getAllByRole('button')
      .map((button) => button.textContent);
    expect(files).toContain('README.md');
  });

  it('shows the whole file when it has a key that the server does not accept', async () => {
    mocks.details.triage = detail('triage', CONTENT, [UNKNOWN]);
    renderAt('/ai/skills/triage');

    expect(
      within(screen.getByRole('region', { name: 'Validation' })).getByText(
        'front-matter-key'
      )
    ).toBeVisible();
    expect((await editor()).value).toBe(CONTENT);

    // The user removes the key in the editor and saves the whole text.
    await userEvent.clear(await editor());
    await userEvent.type(await editor(), 'Body only');
    await userEvent.click(save());
    await waitFor(() =>
      expect(mocks.client.PUT).toHaveBeenCalledWith('/skills/{name}', {
        params: {
          path: { name: 'triage' },
          query: { remoteNode: 'edge' },
          header: { 'If-Match': 'h1' },
        },
        body: { content: 'Body only' },
      })
    );
  });

  it('shows the whole file when it has no front matter', async () => {
    mocks.details.triage = detail('triage', 'Body only\n');
    renderAt('/ai/skills/triage');

    expect((await editor()).value).toBe('Body only\n');
    expect(descriptionField()).toBeDisabled();
  });

  it('shows the Markdown of the body in a preview', async () => {
    renderAt('/ai/skills/triage');

    await userEvent.click(screen.getByRole('button', { name: 'Preview' }));
    expect(await screen.findByRole('heading', { name: 'Steps' })).toBeVisible();
    expect(screen.queryByLabelText('SKILL.md')).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(await editor()).toBeVisible();
  });

  it('gives the usage line of a step', () => {
    renderAt('/ai/skills/triage');

    expect(screen.getByLabelText('Use it in a chat step')).toHaveTextContent(
      'skills: [triage]'
    );
    expect(screen.getByRole('button', { name: 'Copy the step' })).toBeVisible();
  });

  it('gives a reader the text with nothing to change it with', async () => {
    mocks.canWrite = false;
    renderAt('/ai/skills/triage');

    expect(descriptionField()).toHaveAttribute('readonly');
    expect(await editor()).toHaveAttribute('readonly');
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete skill' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'New skill' })).toBeNull();
    await userEvent.keyboard('{Control>}s{/Control}');
    expect(mocks.client.PUT).not.toHaveBeenCalled();
  });
});

describe('unsaved changes', () => {
  it('asks before another skill replaces a draft', async () => {
    renderAt('/ai/skills/triage');
    await userEvent.type(await editor(), 'x');

    await userEvent.click(screen.getByRole('link', { name: /refund/ }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Leave without saving?')).toBeVisible();
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Cancel' })
    );
    expect(where()).toBe('/ai/skills/triage');
    expect((await editor()).value).toMatch(/Read\.\nx$/);

    await userEvent.click(screen.getByRole('link', { name: /refund/ }));
    await userEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Leave',
      })
    );
    await waitFor(() => expect(where()).toBe('/ai/skills/refund'));
    expect((await editor()).value).toBe('# Refund\n');
  });

  it('asks before the Back link and before another tab', async () => {
    mocks.isAdmin = true;
    setViewport(390);
    renderAt('/ai/skills/triage');
    await userEvent.type(await editor(), 'x');

    await userEvent.click(screen.getByRole('tab', { name: 'Endpoints' }));
    expect(await screen.findByRole('dialog')).toBeVisible();
    expect(where()).toBe('/ai/skills/triage');
  });

  // The list and the editor change places at the lg breakpoint, for example
  // when the user turns a tablet: the editor and its draft stay.
  it('keeps the draft when the viewport crosses the breakpoint', async () => {
    const viewport = resizableViewport(1440);
    renderAt('/ai/skills/triage');
    await userEvent.type(await editor(), 'x');

    act(() => viewport.resize(390));
    expect(screen.queryByRole('list', { name: 'Skills' })).toBeNull();
    expect((await editor()).value).toMatch(/Read\.\nx$/);

    act(() => viewport.resize(1440));
    expect(screen.getByRole('list', { name: 'Skills' })).toBeVisible();
    expect((await editor()).value).toMatch(/Read\.\nx$/);
  });

  it('does not ask when nothing changed', async () => {
    renderAt('/ai/skills/triage');
    await userEvent.click(screen.getByRole('link', { name: /refund/ }));

    expect(where()).toBe('/ai/skills/refund');
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('delete', () => {
  it('deletes after a confirmation and goes back to the list', async () => {
    renderAt('/ai/skills/refund');

    await userEvent.click(screen.getByRole('button', { name: 'Delete skill' }));
    expect(mocks.client.DELETE).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('dialog');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Delete' })
    );

    await waitFor(() => expect(where()).toBe('/ai/skills'));
    expect(mocks.client.DELETE).toHaveBeenCalledWith('/skills/{name}', {
      params: {
        path: { name: 'refund' },
        query: { remoteNode: 'edge' },
        header: { 'If-Match': 'h1' },
      },
    });
    expect(mocks.mutate).toHaveBeenCalled();
  });

  it('keeps the skill and shows the message of a refused delete', async () => {
    mocks.client.DELETE.mockResolvedValue(failure(500, { message: 'locked' }));
    renderAt('/ai/skills/refund');

    await userEvent.click(screen.getByRole('button', { name: 'Delete skill' }));
    await userEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Delete',
      })
    );

    expect(await screen.findByText('locked')).toBeVisible();
    expect(where()).toBe('/ai/skills/refund');
  });
});

describe('new skill', () => {
  it('opens the dialog from the button, validates the name, and creates the file', async () => {
    renderAt('/ai/skills/triage');
    await userEvent.click(
      screen.getAllByRole('link', { name: 'New skill' })[0]!
    );

    expect(where()).toBe('/ai/skills/new');
    const dialog = await screen.findByRole('dialog');
    const create = within(dialog).getByRole('button', { name: 'Create' });
    const name = within(dialog).getByRole('textbox', { name: 'Skill name' });
    expect(create).toBeDisabled();

    await userEvent.type(name, 'Bad_Name');
    expect(
      within(dialog).getByText(
        'Use lowercase letters and digits. Join words with one -. Do not start or end with -.'
      )
    ).toBeVisible();
    await userEvent.clear(name);
    await userEvent.type(name, 'a--b');
    expect(create).toBeDisabled();
    expect(create).toBeDisabled();

    await userEvent.clear(name);
    await userEvent.type(name, 'refund');
    expect(
      within(dialog).getByText('A skill named "refund" exists.')
    ).toBeVisible();
    expect(create).toBeDisabled();

    await userEvent.clear(name);
    await userEvent.type(name, 'new');
    expect(within(dialog).getByText(/is reserved/)).toBeVisible();

    await userEvent.clear(name);
    await userEvent.type(name, 'sales{Enter}');

    await waitFor(() => expect(where()).toBe('/ai/skills/sales'));
    const [, init] = mocks.client.PUT.mock.calls[0]!;
    expect(init.params).toEqual({
      path: { name: 'sales' },
      query: { remoteNode: 'edge' },
      header: { 'If-None-Match': '*' },
    });
    expect(init.body.content).toMatch(
      /^---\nname: sales\ndescription: .*Use when/
    );
    expect(mocks.mutate).toHaveBeenCalled();
  });

  it('shows the message of a refused create and keeps the dialog', async () => {
    mocks.client.PUT.mockResolvedValue(failure(500, { message: 'disk full' }));
    renderAt('/ai/skills/new');

    await userEvent.type(
      await screen.findByRole('textbox', { name: 'Skill name' }),
      'sales{Enter}'
    );

    expect(await screen.findByText('disk full')).toBeVisible();
    expect(where()).toBe('/ai/skills/new');
  });

  it('closes the dialog and goes back to the list', async () => {
    renderAt('/ai/skills/new');

    await userEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Cancel',
      })
    );

    expect(where()).toBe('/ai/skills');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('explains an empty list in the pane of the editor', () => {
    mocks.list = { data: { skills: [] } };
    renderAt('/ai/skills');

    expect(screen.getByText('No skills yet')).toBeVisible();
    expect(screen.getByText('No skill selected')).toBeVisible();
    expect(screen.queryByRole('searchbox')).toBeNull();
  });
});
