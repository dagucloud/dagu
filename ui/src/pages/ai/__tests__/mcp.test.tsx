// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import AIPage from '..';

// The forms type long values with a real keyboard, which is slow on a busy machine.
vi.setConfig({ testTimeout: 30000 });

const mocks = vi.hoisted(() => ({
  isAdmin: true,
  client: {
    POST: vi.fn(),
    PATCH: vi.fn(),
    PUT: vi.fn(),
    DELETE: vi.fn(),
  },
  queries: {} as Record<string, unknown>,
  mutate: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: (path: string, init: unknown) =>
    (init === null ? undefined : mocks.queries[path]) ?? {
      data: undefined,
      error: undefined,
      mutate: vi.fn(),
    },
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageSkills: () => true,
  useIsAdmin: () => mocks.isAdmin,
}));

const TOKEN = 'tok-0123456789abcdefghij';
const HEADER_VALUE = 'hdr-value-ZYXWVUTSRQPONMLK';
const OTHER = 'tok-other-9876543210zyxwvu';

const server = {
  name: 'docs',
  url: 'https://docs.example.com/mcp?workspace=main',
  scope: 'global',
  allow: { workspaces: ['*'], dags: ['fix-bug', 'review'] },
  token: {
    keyPrefix: 'tok-01',
    keyFingerprint: '0a1b2c3d4e5f',
    keyUpdatedAt: '2026-11-04T10:00:00Z',
  },
  createdAt: '2026-11-04T10:00:00Z',
  updatedAt: '2026-11-04T10:00:00Z',
};

const headerServer = {
  name: 'wiki',
  url: 'https://wiki.example.com/mcp',
  scope: 'workspace:team',
  allow: { dags: ['*'] },
  headers: [
    {
      name: 'x-api-key',
      keyPrefix: 'hdr-va',
      keyFingerprint: 'aaaaaaaaaaaa',
      keyUpdatedAt: '2026-11-04T09:00:00Z',
    },
    {
      name: 'x-team',
      keyFingerprint: 'bbbbbbbbbbbb',
      keyUpdatedAt: '2026-11-04T11:00:00Z',
    },
  ],
  createdAt: '2026-11-04T09:00:00Z',
  updatedAt: '2026-11-04T11:00:00Z',
};

const bare = {
  name: 'open',
  url: 'http://localhost:9000/mcp',
  scope: 'global',
  allow: { workspaces: ['team'], dags: ['*'] },
  createdAt: '2026-11-04T09:00:00Z',
  updatedAt: '2026-11-04T09:00:00Z',
};

function setQuery(path: string, data: unknown, error?: unknown) {
  mocks.queries[path] = { data, error, mutate: mocks.mutate };
}

function Where() {
  const { pathname } = useLocation();
  return <output data-testid="where">{pathname}</output>;
}

function renderAt(path: string) {
  return render(
    <AppBarContext.Provider
      value={
        {
          setTitle: vi.fn(),
          selectedRemoteNode: 'edge',
          workspaces: [{ name: 'team' }],
        } as never
      }
    >
      <MemoryRouter initialEntries={[path]}>
        <Where />
        <Routes>
          <Route path="/ai/mcp" element={<AIPage />} />
          <Route path="/ai/endpoints" element={<AIPage />} />
          <Route path="/ai/skills" element={<AIPage />} />
        </Routes>
      </MemoryRouter>
    </AppBarContext.Provider>
  );
}

/** Everything that the user or the page can keep of a value, except the field. */
function expectValueNowhere(...values: string[]) {
  for (const value of values) {
    expect(document.body.textContent).not.toContain(value);
    expect(document.body.innerHTML).not.toContain(value);
    expect(JSON.stringify(Object.entries(localStorage))).not.toContain(value);
    expect(JSON.stringify(Object.entries(sessionStorage))).not.toContain(value);
  }
}

beforeEach(() => {
  mocks.isAdmin = true;
  mocks.queries = {};
  mocks.mutate.mockReset().mockResolvedValue(undefined);
  for (const fn of Object.values(mocks.client)) {
    fn.mockReset().mockResolvedValue({
      data: {},
      response: { status: 200 },
    });
  }
  localStorage.clear();
  sessionStorage.clear();
  setQuery('/skills', { skills: [] });
});

describe('the MCP tab', () => {
  it('opens at /ai/mcp for an administrator, and not for another role', () => {
    setQuery('/ai/mcp-servers', { servers: [] });
    renderAt('/ai/mcp');
    expect(screen.getByRole('tab', { name: 'MCP' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByText('No MCP servers yet')).toBeVisible();
  });

  it('shows the skills only to a user who is not an administrator', () => {
    mocks.isAdmin = false;
    setQuery('/ai/mcp-servers', { servers: [server] });
    renderAt('/ai/mcp');
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.queryByText('docs')).toBeNull();
    expect(screen.getByText('No skills yet')).toBeVisible();
  });

  it('lists the host, the credentials as prefix and fingerprint, the scope and who may use the entry', () => {
    setQuery('/ai/mcp-servers', { servers: [server, headerServer, bare] });
    renderAt('/ai/mcp');

    const rows = screen.getAllByRole('row').slice(1);
    expect(rows).toHaveLength(3);
    const first = within(rows[0]!);
    expect(first.getByText('docs')).toBeVisible();
    // The host only: no path and no query of the URL.
    expect(first.getByText('docs.example.com')).toBeVisible();
    expect(first.getByText('Global')).toBeVisible();
    expect(first.getByText(/tok-01… · 0a1b2c3d4e5f/)).toBeVisible();
    expect(first.getByText('All workspaces · 2 DAGs')).toBeVisible();

    const second = within(rows[1]!);
    expect(second.getByText('Workspace team')).toBeVisible();
    expect(second.getByText(/hdr-va… · aaaaaaaaaaaa/)).toBeVisible();
    expect(second.getByText(/bbbbbbbbbbbb/)).toBeVisible();
    expect(second.getByText('x-api-key')).toBeVisible();
    expect(second.getByText('All DAGs')).toBeVisible();

    const third = within(rows[2]!);
    expect(third.getByText('No credential')).toBeVisible();
    expect(third.getByText('1 workspaces · All DAGs')).toBeVisible();
    expectValueNowhere(TOKEN, HEADER_VALUE);
  });
});

describe('add form', () => {
  async function openForm() {
    setQuery('/ai/mcp-servers', { servers: [server] });
    const user = userEvent.setup();
    renderAt('/ai/mcp');
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    const dialog = await screen.findByRole('dialog');
    return { user, dialog };
  }

  async function allowAll(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole('checkbox', { name: 'All workspaces' }));
    await user.click(screen.getByRole('checkbox', { name: 'All DAGs' }));
  }

  it('shows the name rule at once and sends nothing for an invalid form', async () => {
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'Bad Name');
    expect(
      screen.getByText(/The name must start with a lowercase letter/)
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    expect(screen.getByText('Give the URL of the MCP server.')).toBeVisible();
    expect(
      screen.getAllByText('Choose at least one name, or All.')
    ).toHaveLength(2);
    expect(mocks.client.POST).not.toHaveBeenCalled();
  });

  it('refuses the name of the tool bridge and a name that exists', async () => {
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'dagu');
    expect(
      screen.getByText(
        'The name dagu is the name of the tool bridge. Use another name.'
      )
    ).toBeVisible();
    await user.clear(screen.getByLabelText('Name'));
    await user.type(screen.getByLabelText('Name'), 'docs');
    expect(
      screen.getByText('A MCP server with this name exists in this scope.')
    ).toBeVisible();
  });

  it('checks the URL as the server does, and warns about http', async () => {
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await allowAll(user);
    const url = screen.getByLabelText('URL');
    for (const [value, text] of [
      ['ftp://x.example.com', 'The URL must start with https or http.'],
      [
        'https://u:p@x.example.com/mcp',
        'The URL must not have user information or a fragment.',
      ],
      [
        'https://x.example.com/mcp#frag',
        'The URL must not have user information or a fragment.',
      ],
      ['not a url', 'The URL is not valid.'],
    ] as const) {
      await user.clear(url);
      await user.type(url, value);
      await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
      expect(await screen.findByText(text)).toBeVisible();
    }
    expect(mocks.client.POST).not.toHaveBeenCalled();

    await user.clear(url);
    await user.type(url, 'http://localhost:9000/mcp');
    expect(
      screen.getByText(
        'This URL uses http. The credentials go over the network with no encryption.'
      )
    ).toBeVisible();
  });

  it('sends a token once, clears the field, and keeps the token nowhere', async () => {
    mocks.client.POST.mockResolvedValue({
      data: { ...server, name: 'fresh' },
      response: { status: 201 },
    });
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await user.type(
      screen.getByLabelText('URL'),
      'https://docs.example.com/mcp'
    );
    await user.selectOptions(
      screen.getByLabelText('Credentials'),
      'Bearer token'
    );
    const field = screen.getByLabelText('Token', { selector: 'input' });
    expect(field).toHaveAttribute('type', 'password');
    expect(field).toHaveAttribute('autocomplete', 'new-password');
    await user.type(field, TOKEN);
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));

    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.POST.mock.calls[0]!;
    expect(path).toBe('/ai/mcp-servers');
    expect(init.params.query).toEqual({ remoteNode: 'edge' });
    expect(JSON.stringify(init.params)).not.toContain(TOKEN);
    expect(init.body).toEqual({
      name: 'fresh',
      url: 'https://docs.example.com/mcp',
      scope: 'global',
      allow: { workspaces: ['*'], dags: ['*'] },
      token: TOKEN,
    });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(mocks.mutate).toHaveBeenCalled();
    expectValueNowhere(TOKEN);
  });

  it('sends no credential when the form is None', async () => {
    mocks.client.POST.mockResolvedValue({
      data: { ...bare, name: 'fresh' },
      response: { status: 201 },
    });
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await user.type(
      screen.getByLabelText('URL'),
      'https://open.example.com/mcp'
    );
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalledTimes(1));
    const body = mocks.client.POST.mock.calls[0]![1].body;
    expect(body).not.toHaveProperty('token');
    expect(body).not.toHaveProperty('headers');
  });

  it('sends headers with a write-only value each, and checks the names', async () => {
    mocks.client.POST.mockResolvedValue({
      data: { ...headerServer, name: 'fresh', scope: 'global' },
      response: { status: 201 },
    });
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await user.type(
      screen.getByLabelText('URL'),
      'https://docs.example.com/mcp'
    );
    await user.selectOptions(screen.getByLabelText('Credentials'), 'Headers');
    await allowAll(user);

    // One row opens with the form of headers.
    await user.type(screen.getByLabelText('Header name 1'), 'Content-Type');
    const first = screen.getByLabelText('Header value 1');
    expect(first).toHaveAttribute('type', 'password');
    await user.type(first, HEADER_VALUE);
    await user.click(screen.getByRole('button', { name: 'Add header' }));
    await user.type(screen.getByLabelText('Header name 2'), 'X-Api-Key');
    await user.type(screen.getByLabelText('Header value 2'), OTHER);
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    expect(
      await screen.findByText('Dagu sets this header. Use another name.')
    ).toBeVisible();
    expect(mocks.client.POST).not.toHaveBeenCalled();

    await user.clear(screen.getByLabelText('Header name 1'));
    await user.type(screen.getByLabelText('Header name 1'), 'X-Team');
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalledTimes(1));
    const body = mocks.client.POST.mock.calls[0]![1].body;
    expect(body.headers).toEqual({
      'x-team': HEADER_VALUE,
      'x-api-key': OTHER,
    });
    expect(body).not.toHaveProperty('token');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expectValueNowhere(HEADER_VALUE, OTHER);
  });

  it('asks for a value in each header row, and for a token in the token form', async () => {
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await user.type(
      screen.getByLabelText('URL'),
      'https://docs.example.com/mcp'
    );
    await allowAll(user);
    await user.selectOptions(
      screen.getByLabelText('Credentials'),
      'Bearer token'
    );
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    expect(await screen.findByText('Give the token.')).toBeVisible();

    await user.selectOptions(screen.getByLabelText('Credentials'), 'Headers');
    await user.type(screen.getByLabelText('Header name 1'), 'X-Key');
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    expect(
      await screen.findByText('Give the value of the header.')
    ).toBeVisible();
    expect(mocks.client.POST).not.toHaveBeenCalled();
  });

  it('clears the fields after a refusal, and shows the refusal as a sentence with no value', async () => {
    mocks.client.POST.mockResolvedValue({
      error: {
        code: 'bad_request',
        message:
          'invalid AI endpoint: header name "Host" is not valid: the coordinator sets it',
      },
      response: { status: 400 },
    });
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await user.type(
      screen.getByLabelText('URL'),
      'https://docs.example.com/mcp'
    );
    await user.selectOptions(
      screen.getByLabelText('Credentials'),
      'Bearer token'
    );
    await user.type(
      screen.getByLabelText('Token', { selector: 'input' }),
      TOKEN
    );
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));

    expect(
      await screen.findByText('Dagu sets this header. Use another name.')
    ).toBeVisible();
    expect(screen.getByLabelText('Token', { selector: 'input' })).toHaveValue(
      ''
    );
    expectValueNowhere(TOKEN);
  });

  it('maps a conflict to a sentence', async () => {
    mocks.client.POST.mockResolvedValue({
      error: { code: 'already_exists', message: 'x' },
      response: { status: 409 },
    });
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'fresh');
    await user.type(
      screen.getByLabelText('URL'),
      'https://docs.example.com/mcp'
    );
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add MCP server' }));
    expect(
      await screen.findAllByText(
        'A MCP server with this name exists in this scope.'
      )
    ).not.toHaveLength(0);
  });
});

describe('edit, rotate and delete', () => {
  async function open(name: string) {
    setQuery('/ai/mcp-servers', { servers: [server, headerServer] });
    const user = userEvent.setup();
    renderAt('/ai/mcp');
    return { user, name };
  }

  it('changes the URL and the use rule, and sends the fields that differ', async () => {
    mocks.client.PATCH.mockResolvedValue({
      data: server,
      response: { status: 200 },
    });
    const { user } = await open('docs');
    await user.click(screen.getByRole('button', { name: 'Edit docs' }));
    const dialog = await screen.findByRole('dialog');
    // The name and the scope are fixed, and the credentials have their own dialog.
    expect(within(dialog).getByLabelText('Name')).toHaveAttribute('readonly');
    expect(within(dialog).queryByLabelText('Credentials')).toBeNull();
    const url = within(dialog).getByLabelText('URL');
    await user.clear(url);
    await user.type(url, 'https://docs2.example.com/mcp');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(mocks.client.PATCH).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.PATCH.mock.calls[0]!;
    expect(path).toBe('/ai/mcp-servers/{name}');
    expect(init.params.path).toEqual({ name: 'docs' });
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      workspace: 'global',
    });
    expect(init.body).toEqual({ url: 'https://docs2.example.com/mcp' });
  });

  it('rotates a token: one field, sent once, cleared', async () => {
    mocks.client.PUT.mockResolvedValue({
      data: server,
      response: { status: 200 },
    });
    const { user } = await open('docs');
    await user.click(
      screen.getByRole('button', { name: 'Rotate the credentials of docs' })
    );
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('Credentials')).toHaveValue('token');
    const field = within(dialog).getByLabelText('Token', { selector: 'input' });
    expect(field).toHaveValue('');
    await user.type(field, OTHER);
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate credentials' })
    );

    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.PUT.mock.calls[0]!;
    expect(path).toBe('/ai/mcp-servers/{name}/credentials');
    expect(init.params.path).toEqual({ name: 'docs' });
    expect(JSON.stringify(init.params)).not.toContain(OTHER);
    expect(init.body).toEqual({ token: OTHER });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expectValueNowhere(OTHER, TOKEN);
  });

  it('opens the header form with the names that exist and asks for every value again', async () => {
    mocks.client.PUT.mockResolvedValue({
      data: headerServer,
      response: { status: 200 },
    });
    const { user } = await open('wiki');
    await user.click(
      screen.getByRole('button', { name: 'Rotate the credentials of wiki' })
    );
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('Header name 1')).toHaveValue(
      'x-api-key'
    );
    expect(within(dialog).getByLabelText('Header name 2')).toHaveValue(
      'x-team'
    );
    expect(within(dialog).getByLabelText('Header value 1')).toHaveValue('');
    await user.type(within(dialog).getByLabelText('Header value 1'), OTHER);
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate credentials' })
    );
    expect(
      await within(dialog).findByText('Give the value of the header.')
    ).toBeVisible();
    expect(mocks.client.PUT).not.toHaveBeenCalled();

    await user.type(
      within(dialog).getByLabelText('Header value 2'),
      HEADER_VALUE
    );
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate credentials' })
    );
    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(1));
    expect(mocks.client.PUT.mock.calls[0]![1].body).toEqual({
      headers: { 'x-api-key': OTHER, 'x-team': HEADER_VALUE },
    });
    expect(mocks.client.PUT.mock.calls[0]![1].params.query).toEqual({
      remoteNode: 'edge',
      workspace: 'team',
    });
  });

  it('says that the form None removes the credentials', async () => {
    const { user } = await open('docs');
    await user.click(
      screen.getByRole('button', { name: 'Rotate the credentials of docs' })
    );
    const dialog = await screen.findByRole('dialog');
    await user.selectOptions(
      within(dialog).getByLabelText('Credentials'),
      'None'
    );
    expect(
      within(dialog).getByText('The entry will have no credential.')
    ).toBeVisible();
  });

  it('clears the field after a refusal of the rotation', async () => {
    mocks.client.PUT.mockResolvedValue({
      error: {
        code: 'bad_request',
        message:
          'invalid AI endpoint: token has 9000 bytes, above the limit of 8192',
      },
      response: { status: 400 },
    });
    const { user } = await open('docs');
    await user.click(
      screen.getByRole('button', { name: 'Rotate the credentials of docs' })
    );
    const dialog = await screen.findByRole('dialog');
    const field = within(dialog).getByLabelText('Token', { selector: 'input' });
    await user.type(field, OTHER);
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate credentials' })
    );
    expect(
      await within(dialog).findByText(
        'The value is too long. The limit is 8192 bytes.'
      )
    ).toBeVisible();
    expect(field).toHaveValue('');
    expectValueNowhere(OTHER);
  });

  it('asks for a confirmation that names the entry before it deletes', async () => {
    mocks.client.DELETE.mockResolvedValue({ response: { status: 204 } });
    const { user } = await open('docs');
    await user.click(
      screen.getByRole('button', { name: 'Delete MCP server docs' })
    );
    expect(
      await screen.findByText(
        /Delete "docs"\? A step that names this MCP server fails until you create it again\./
      )
    ).toBeVisible();
    expect(mocks.client.DELETE).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mocks.client.DELETE).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.DELETE.mock.calls[0]!;
    expect(path).toBe('/ai/mcp-servers/{name}');
    expect(init.params.path).toEqual({ name: 'docs' });
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      workspace: 'global',
    });
  });
});
