// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import AIPage from '..';

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

const KEY = 'sk-test-0123456789abcdef';
const KEY_TWO = 'sk-test-ZYXWVUTSRQPONMLK';

const presets = [
  {
    id: 'anthropic',
    label: 'Anthropic',
    needsKey: true,
    protocols: [
      {
        protocol: 'anthropic',
        baseUrl: 'https://api.anthropic.com',
        auth: 'x-api-key',
        modelsUrl: 'https://api.anthropic.com/v1/models',
        chatType: 'anthropic',
        consumers: ['claude'],
      },
    ],
  },
  {
    id: 'mistral',
    label: 'Mistral',
    needsKey: true,
    protocols: [
      {
        protocol: 'openai',
        baseUrl: 'https://api.mistral.ai/v1',
        auth: 'bearer',
        chatType: 'openai',
        consumers: ['codex', 'chat.completion'],
      },
    ],
  },
  {
    id: 'ollama',
    label: 'Local model (Ollama)',
    needsKey: false,
    protocols: [
      {
        protocol: 'openai',
        baseUrl: 'http://localhost:11434/v1',
        auth: 'bearer',
        chatType: 'local',
        consumers: ['codex', 'chat.completion'],
      },
      {
        protocol: 'anthropic',
        baseUrl: 'http://localhost:11434',
        auth: 'bearer',
        chatType: 'anthropic',
        consumers: ['claude'],
      },
    ],
  },
  {
    id: 'custom',
    label: 'Custom',
    needsKey: true,
    protocols: [
      { protocol: 'anthropic', chatType: 'anthropic', consumers: ['claude'] },
      {
        protocol: 'openai',
        chatType: 'openai',
        consumers: ['chat.completion'],
      },
    ],
  },
];

const endpoint = {
  name: 'cle1',
  description: 'Team key',
  preset: 'anthropic',
  protocol: 'anthropic',
  baseUrl: 'https://api.anthropic.com',
  auth: 'x-api-key',
  scope: 'global',
  allow: { workspaces: ['*'], dags: ['fix-bug', 'review'] },
  keyPrefix: 'sk-tes',
  keyFingerprint: '0a1b2c3d4e5f',
  keyUpdatedAt: '2026-11-04T10:00:00Z',
  createdAt: '2026-11-04T10:00:00Z',
  updatedAt: '2026-11-04T10:00:00Z',
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
          <Route path="/ai" element={<AIPage />} />
          <Route path="/ai/endpoints" element={<AIPage />} />
          <Route path="/ai/mcp" element={<AIPage />} />
          <Route path="/ai/skills" element={<AIPage />} />
        </Routes>
      </MemoryRouter>
    </AppBarContext.Provider>
  );
}

/** Everything that the user or the page can keep of the key, except the field. */
function expectKeyNowhere(...keys: string[]) {
  for (const key of keys) {
    expect(document.body.textContent).not.toContain(key);
    expect(document.body.innerHTML).not.toContain(key);
    expect(JSON.stringify(Object.entries(localStorage))).not.toContain(key);
    expect(JSON.stringify(Object.entries(sessionStorage))).not.toContain(key);
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
  setQuery('/ai/endpoint-presets', { presets });
  setQuery('/skills', { skills: [] });
});

describe('AI page tabs', () => {
  it('shows Endpoints, MCP and Skills to an administrator', async () => {
    setQuery('/ai/endpoints', { endpoints: [] });
    const user = userEvent.setup();
    renderAt('/ai');

    const tabs = screen.getAllByRole('tab');
    expect(tabs.map((tab) => tab.textContent)).toEqual([
      'Endpoints',
      'MCP',
      'Skills',
    ]);
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true');

    await user.click(screen.getByRole('tab', { name: 'Skills' }));
    expect(screen.getByTestId('where')).toHaveTextContent('/ai/skills');
    expect(screen.getByRole('tab', { name: 'Skills' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('moves between the tabs with the arrow keys', async () => {
    setQuery('/ai/endpoints', { endpoints: [] });
    const user = userEvent.setup();
    renderAt('/ai/endpoints');

    screen.getByRole('tab', { name: 'Endpoints' }).focus();
    await user.keyboard('{ArrowRight}');
    expect(screen.getByTestId('where')).toHaveTextContent('/ai/mcp');
    await user.keyboard('{ArrowRight}');
    expect(screen.getByTestId('where')).toHaveTextContent('/ai/skills');
    await user.keyboard('{ArrowRight}');
    expect(screen.getByTestId('where')).toHaveTextContent('/ai/endpoints');
  });

  it('shows the skills only, with no tab row, to a user who is not an administrator', () => {
    mocks.isAdmin = false;
    setQuery('/ai/endpoints', { endpoints: [endpoint] });
    renderAt('/ai/endpoints');

    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.queryByText('cle1')).toBeNull();
    expect(screen.getByText('No skills yet')).toBeVisible();
  });
});

describe('endpoints list', () => {
  it('explains an endpoint in two sentences and offers Add', () => {
    setQuery('/ai/endpoints', { endpoints: [] });
    renderAt('/ai');

    expect(screen.getByText('No endpoints yet')).toBeVisible();
    // The sentences show once: the empty state holds them.
    expect(
      screen.getAllByText(
        'An endpoint holds the address and the key of a model API. A step names it, and the server keeps the key.'
      )
    ).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Add endpoint' })).toBeEnabled();
  });

  it('shows the prefix and the fingerprint of the key, never more', () => {
    setQuery('/ai/endpoints', {
      endpoints: [
        endpoint,
        {
          ...endpoint,
          name: 'local',
          preset: 'ollama',
          protocol: 'openai',
          scope: 'workspace:team',
          allow: { dags: ['*'] },
          keyPrefix: undefined,
          keyFingerprint: undefined,
          keyUpdatedAt: undefined,
        },
      ],
    });
    renderAt('/ai');

    // The column of the vendor names the preset of the endpoint.
    expect(
      screen.getAllByRole('columnheader').map((cell) => cell.textContent)
    ).toEqual([
      'Name',
      'Preset',
      'Scope',
      'Key',
      'Key updated',
      'Allowed',
      'Models',
      'Actions',
    ]);
    const rows = screen.getAllByRole('row').slice(1);
    expect(rows).toHaveLength(2);
    const first = within(rows[0]!);
    expect(first.getByText('cle1')).toBeVisible();
    expect(first.getByText('Anthropic')).toBeVisible();
    expect(first.getByText('Global')).toBeVisible();
    expect(first.getByText('sk-tes… · 0a1b2c3d4e5f')).toBeVisible();
    expect(first.getByText('All workspaces · 2 DAGs')).toBeVisible();
    const second = within(rows[1]!);
    expect(second.getByText('Local model (Ollama)')).toBeVisible();
    expect(second.getByText('Workspace team')).toBeVisible();
    expect(second.getByText('No key')).toBeVisible();
    expect(second.getByText('All DAGs')).toBeVisible();
    expectKeyNowhere(KEY);
  });
});

describe('add form', () => {
  async function openForm() {
    setQuery('/ai/endpoints', { endpoints: [endpoint] });
    const user = userEvent.setup();
    renderAt('/ai');
    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));
    const dialog = await screen.findByRole('dialog');
    return { user, dialog };
  }

  async function allowAll(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole('checkbox', { name: 'All workspaces' }));
    await user.click(screen.getByRole('checkbox', { name: 'All DAGs' }));
  }

  it('prefills the base URL and the form of authentication from the preset', async () => {
    const { user } = await openForm();

    expect(screen.getByLabelText('Base URL')).toHaveValue(
      'https://api.anthropic.com'
    );
    expect(screen.getByLabelText('Authentication')).toHaveValue('x-api-key');
    // The use rule of a DAG with no workspace belongs to the workspaces.
    expect(
      screen.getByText('A DAG with no workspace matches only All workspaces.')
    ).toBeVisible();
    expect(
      screen.getByText('A step of a DAG in this list can use the endpoint.')
    ).toBeVisible();
    // Anthropic has one protocol: there is nothing to choose.
    expect(screen.queryByLabelText('Protocol')).toBeNull();

    // The protocol stays when the new preset has it.
    await user.selectOptions(screen.getByLabelText('Preset'), 'ollama');
    expect(screen.getByLabelText('Base URL')).toHaveValue(
      'http://localhost:11434'
    );
    expect(screen.getByLabelText('Protocol')).toBeVisible();
    await user.selectOptions(screen.getByLabelText('Protocol'), 'openai');
    expect(screen.getByLabelText('Base URL')).toHaveValue(
      'http://localhost:11434/v1'
    );
  });

  it('shows the name rule at once and sends nothing for an invalid form', async () => {
    const { user } = await openForm();

    await user.type(screen.getByLabelText('Name'), 'Bad Name');
    expect(
      screen.getByText(/The name must start with a lowercase letter/)
    ).toBeVisible();

    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));
    expect(screen.getByText('Give the key.')).toBeVisible();
    // One message for the workspaces and one for the DAGs.
    expect(
      screen.getAllByText('Choose at least one name, or All.')
    ).toHaveLength(2);
    expect(mocks.client.POST).not.toHaveBeenCalled();
  });

  it('refuses a name that exists in the scope', async () => {
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'cle1');
    expect(
      screen.getByText('An endpoint with this name exists in this scope.')
    ).toBeVisible();
  });

  it('sends the key once, clears the field, and keeps the key nowhere', async () => {
    mocks.client.POST.mockResolvedValue({
      data: { ...endpoint, name: 'cle2' },
      response: { status: 201 },
    });
    const { user } = await openForm();

    await user.type(screen.getByLabelText('Name'), 'cle2');
    const field = screen.getByLabelText('Key', { selector: 'input' });
    expect(field).toHaveAttribute('type', 'password');
    expect(field).toHaveAttribute('autocomplete', 'new-password');
    await user.type(field, KEY);
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));

    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.POST.mock.calls[0]!;
    expect(path).toBe('/ai/endpoints');
    expect(init.params.query).toEqual({ remoteNode: 'edge' });
    // The key is in the body only: not in the path, not in the query.
    expect(JSON.stringify(init.params)).not.toContain(KEY);
    expect(init.body).toEqual({
      name: 'cle2',
      preset: 'anthropic',
      protocol: 'anthropic',
      baseUrl: 'https://api.anthropic.com',
      auth: 'x-api-key',
      scope: 'global',
      allow: { workspaces: ['*'], dags: ['*'] },
      key: KEY,
    });

    // The dialog closes, the list reloads, and the key is nowhere in the page.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(mocks.mutate).toHaveBeenCalled();
    expectKeyNowhere(KEY);
  });

  it('clears the key field after a refusal, and shows the refusal as a sentence', async () => {
    mocks.client.POST.mockResolvedValue({
      error: {
        code: 'bad_request',
        message:
          'invalid AI endpoint: base_url "http://x": the scheme http is valid only for a local model and for custom; use https',
      },
      response: { status: 400 },
    });
    const { user } = await openForm();

    await user.type(screen.getByLabelText('Name'), 'cle2');
    await user.type(screen.getByLabelText('Key', { selector: 'input' }), KEY);
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));

    expect(
      await screen.findByText(
        'Only a local model and Custom accept http. Use https.'
      )
    ).toBeVisible();
    expect(screen.getByLabelText('Key', { selector: 'input' })).toHaveValue('');
    expectKeyNowhere(KEY);
  });

  it('maps a conflict to a sentence', async () => {
    mocks.client.POST.mockResolvedValue({
      error: {
        code: 'already_exists',
        message: 'An AI endpoint with this name exists in the scope',
      },
      response: { status: 409 },
    });
    const { user } = await openForm();
    await user.type(screen.getByLabelText('Name'), 'cle2');
    await user.type(screen.getByLabelText('Key', { selector: 'input' }), KEY);
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));

    expect(
      await screen.findByRole('alert', {
        name: '',
      })
    ).toHaveTextContent('An endpoint with this name exists in this scope.');
  });

  it('needs no key for a local model, accepts http, and warns about it', async () => {
    mocks.client.POST.mockResolvedValue({
      data: { ...endpoint, name: 'local' },
      response: { status: 201 },
    });
    const { user } = await openForm();

    await user.selectOptions(screen.getByLabelText('Preset'), 'ollama');
    expect(screen.getByText('Key (optional)')).toBeVisible();
    expect(
      screen.getByText(/The name localhost is the host of the coordinator/)
    ).toBeVisible();
    expect(
      screen.getByText(
        'This URL uses http. The key goes over the network with no encryption.'
      )
    ).toBeVisible();

    await user.type(screen.getByLabelText('Name'), 'local');
    await allowAll(user);
    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));

    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalledTimes(1));
    const body = mocks.client.POST.mock.calls[0]![1].body;
    expect(body).not.toHaveProperty('key');
    expect(body.preset).toBe('ollama');
    expect(body.baseUrl).toBe('http://localhost:11434');
    expect(body.protocol).toBe('anthropic');
  });

  it('sets a workspace scope without a list of workspaces', async () => {
    mocks.client.POST.mockResolvedValue({
      data: { ...endpoint, name: 'cle2' },
      response: { status: 201 },
    });
    const { user } = await openForm();

    await user.selectOptions(screen.getByLabelText('Scope'), 'team');
    expect(
      screen.queryByRole('group', { name: 'Allowed workspaces' })
    ).toBeNull();
    await user.type(screen.getByLabelText('Name'), 'cle2');
    await user.type(screen.getByLabelText('Key', { selector: 'input' }), KEY);
    await user.click(screen.getByRole('checkbox', { name: 'All DAGs' }));
    await user.click(screen.getByRole('button', { name: 'Add endpoint' }));

    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalledTimes(1));
    const body = mocks.client.POST.mock.calls[0]![1].body;
    expect(body.scope).toBe('workspace:team');
    expect(body.allow).toEqual({ dags: ['*'] });
  });

  it('adds a DAG name to the allow list', async () => {
    const { user } = await openForm();

    await user.type(
      screen.getByLabelText('Allowed DAGs', { selector: 'input' }),
      'fix-bug{Enter}'
    );
    expect(
      screen.getByRole('button', { name: 'Remove fix-bug' })
    ).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Remove fix-bug' }));
    expect(screen.queryByRole('button', { name: 'Remove fix-bug' })).toBeNull();
  });
});

describe('change, rotate and delete', () => {
  it('changes only the fields that differ, and has no key field', async () => {
    mocks.client.PATCH.mockResolvedValue({
      data: endpoint,
      response: { status: 200 },
    });
    setQuery('/ai/endpoints', { endpoints: [endpoint] });
    const user = userEvent.setup();
    renderAt('/ai');

    await user.click(screen.getByRole('button', { name: 'Edit cle1' }));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).queryByLabelText('Key', { selector: 'input' })
    ).toBeNull();
    expect(within(dialog).getByLabelText('Name')).toHaveAttribute('readonly');

    const description = within(dialog).getByLabelText('Description');
    await user.clear(description);
    await user.type(description, 'Shared');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(mocks.client.PATCH).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.PATCH.mock.calls[0]!;
    expect(path).toBe('/ai/endpoints/{name}');
    expect(init.params.path).toEqual({ name: 'cle1' });
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      workspace: 'global',
    });
    expect(init.body).toEqual({ description: 'Shared' });
  });

  it('rotates the key, clears the field and keeps the key nowhere', async () => {
    mocks.client.PUT.mockResolvedValue({
      data: endpoint,
      response: { status: 200 },
    });
    setQuery('/ai/endpoints', { endpoints: [endpoint] });
    const user = userEvent.setup();
    renderAt('/ai');

    await user.click(
      screen.getByRole('button', { name: 'Rotate the key of cle1' })
    );
    const dialog = await screen.findByRole('dialog');
    const field = within(dialog).getByLabelText('New key');
    expect(field).toHaveAttribute('type', 'password');
    expect(field).toHaveValue('');
    await user.type(field, KEY_TWO);
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate key' })
    );

    await waitFor(() => expect(mocks.client.PUT).toHaveBeenCalledTimes(1));
    const [path, init] = mocks.client.PUT.mock.calls[0]!;
    expect(path).toBe('/ai/endpoints/{name}/key');
    expect(init.body).toEqual({ key: KEY_TWO });
    expect(JSON.stringify(init.params)).not.toContain(KEY_TWO);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expectKeyNowhere(KEY_TWO);
  });

  it('refuses an empty key and a failed rotation shows no key', async () => {
    mocks.client.PUT.mockResolvedValue({
      error: { code: 'not_found', message: 'AI endpoint not found' },
      response: { status: 404 },
    });
    setQuery('/ai/endpoints', { endpoints: [endpoint] });
    const user = userEvent.setup();
    renderAt('/ai');

    await user.click(
      screen.getByRole('button', { name: 'Rotate the key of cle1' })
    );
    const dialog = await screen.findByRole('dialog');
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate key' })
    );
    expect(within(dialog).getByText('Give the key.')).toBeVisible();
    expect(mocks.client.PUT).not.toHaveBeenCalled();

    await user.type(within(dialog).getByLabelText('New key'), KEY_TWO);
    await user.click(
      within(dialog).getByRole('button', { name: 'Rotate key' })
    );
    expect(
      await within(dialog).findByText(
        'The endpoint does not exist. Reload the page.'
      )
    ).toBeVisible();
    expect(within(dialog).getByLabelText('New key')).toHaveValue('');
    expectKeyNowhere(KEY_TWO);
  });

  it('asks a confirmation that names the endpoint and warns, then deletes', async () => {
    mocks.client.DELETE.mockResolvedValue({ response: { status: 204 } });
    setQuery('/ai/endpoints', {
      endpoints: [
        { ...endpoint, scope: 'workspace:team', allow: { dags: ['*'] } },
      ],
    });
    const user = userEvent.setup();
    renderAt('/ai');

    await user.click(
      screen.getByRole('button', { name: 'Delete endpoint cle1' })
    );
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText(
        'Delete "cle1"? A step that names this endpoint fails until you create it again. The key is deleted too.'
      )
    ).toBeVisible();
    expect(mocks.client.DELETE).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mocks.client.DELETE).toHaveBeenCalledTimes(1));
    const init = mocks.client.DELETE.mock.calls[0]![1];
    expect(init.params.path).toEqual({ name: 'cle1' });
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      workspace: 'team',
    });
    await waitFor(() => expect(mocks.mutate).toHaveBeenCalled());
  });
});
