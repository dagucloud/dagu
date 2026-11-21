// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import AIPage from '..';

const mocks = vi.hoisted(() => ({
  client: { POST: vi.fn(), PATCH: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() },
  queries: {} as Record<string, unknown>,
  inits: {} as Record<string, unknown>,
  mutate: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => mocks.client,
  useQuery: (path: string, init: unknown) => {
    mocks.inits[path] = init;
    return (
      (init === null ? undefined : mocks.queries[path]) ?? {
        data: undefined,
        error: undefined,
        mutate: vi.fn(),
      }
    );
  },
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageSkills: () => true,
  useIsAdmin: () => true,
}));

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
];

const base = {
  description: '',
  preset: 'anthropic',
  protocol: 'anthropic',
  baseUrl: 'https://api.anthropic.com',
  auth: 'x-api-key',
  scope: 'global',
  allow: { workspaces: ['*'], dags: ['*'] },
  keyPrefix: 'sk-tes',
  keyFingerprint: '0a1b2c3d4e5f',
  keyUpdatedAt: '2026-11-04T10:00:00Z',
  createdAt: '2026-11-04T10:00:00Z',
  updatedAt: '2026-11-04T10:00:00Z',
};

const withList = {
  ...base,
  name: 'cle1',
  models: {
    supported: true,
    count: 3,
    refreshedAt: '2026-11-04T10:00:00Z',
  },
};

function setQuery(path: string, data: unknown) {
  mocks.queries[path] = { data, error: undefined, mutate: mocks.mutate };
}

function renderPage() {
  return render(
    <AppBarContext.Provider
      value={
        {
          setTitle: vi.fn(),
          selectedRemoteNode: 'edge',
          workspaces: [],
        } as never
      }
    >
      <MemoryRouter initialEntries={['/ai/endpoints']}>
        <Routes>
          <Route path="/ai/endpoints" element={<AIPage />} />
        </Routes>
      </MemoryRouter>
    </AppBarContext.Provider>
  );
}

const rowOf = (name: string) =>
  within(screen.getByText(name).closest('tr') as HTMLElement);

beforeEach(() => {
  mocks.queries = {};
  mocks.inits = {};
  mocks.mutate.mockReset().mockResolvedValue(undefined);
  for (const fn of Object.values(mocks.client)) {
    fn.mockReset().mockResolvedValue({ data: {}, response: { status: 200 } });
  }
  setQuery('/ai/endpoint-presets', { presets });
  setQuery('/skills', { skills: [] });
});

describe('models of an endpoint in the list', () => {
  it('shows the number of models, the date of the last good read and a Reload button', () => {
    setQuery('/ai/endpoints', { endpoints: [withList] });
    renderPage();

    const row = rowOf('cle1');
    expect(row.getByRole('button', { name: '3 models' })).toBeEnabled();
    expect(row.getByText(/Last good read/)).toBeVisible();
    expect(
      row.getByRole('button', { name: 'Reload the models of cle1' })
    ).toBeEnabled();
    expect(row.queryByTestId('models-error')).toBeNull();
  });

  it('says so for an endpoint with no models URL, and gives it no Reload button', () => {
    setQuery('/ai/endpoints', {
      endpoints: [{ ...withList, models: { supported: false, count: 0 } }],
    });
    renderPage();

    const row = rowOf('cle1');
    expect(row.getByTestId('models-none')).toHaveTextContent('No model list');
    expect(row.queryByRole('button', { name: /Reload the models/ })).toBeNull();
  });

  it('shows the error beside the last good list, which stays', () => {
    setQuery('/ai/endpoints', {
      endpoints: [
        {
          ...withList,
          models: {
            ...withList.models,
            error:
              'the provider answered with the status 500 Internal Server Error',
            errorAt: '2026-11-04T11:00:00Z',
          },
        },
      ],
    });
    renderPage();

    const row = rowOf('cle1');
    expect(row.getByRole('button', { name: '3 models' })).toBeEnabled();
    expect(row.getByText(/Last good read/)).toBeVisible();
    expect(row.getByTestId('models-error')).toHaveTextContent(
      'The last read failed: the provider answered with the status 500 Internal Server Error'
    );
    // No hint about a subscription for a key of the API.
    expect(row.getByTestId('models-error')).not.toHaveTextContent(
      'subscription'
    );
  });

  it('adds a hint for a token of a subscription that cannot list models', () => {
    setQuery('/ai/endpoints', {
      endpoints: [
        {
          ...withList,
          auth: 'anthropic-oauth',
          models: {
            supported: true,
            count: 0,
            error: 'the provider answered with the status 403 Forbidden',
            errorAt: '2026-11-04T11:00:00Z',
          },
        },
      ],
    });
    renderPage();

    const row = rowOf('cle1');
    expect(row.getByTestId('models-error')).toHaveTextContent(
      'A token of a subscription can lack the right to list models. Type the model name in the step.'
    );
    // With no model there is nothing to open. The read failed, so the page does not say
    // that nothing was read: it says that no read was good.
    expect(row.getByRole('button', { name: '0 models' })).toBeDisabled();
    expect(row.getByText('No good read yet')).toBeVisible();
    expect(row.queryByText('Not read yet')).toBeNull();
  });

  it('reloads one endpoint, shows a busy state, then asks the list again', async () => {
    let finish: (value: unknown) => void = () => {};
    mocks.client.POST.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      })
    );
    setQuery('/ai/endpoints', { endpoints: [withList] });
    const user = userEvent.setup();
    renderPage();

    await user.click(
      screen.getByRole('button', { name: 'Reload the models of cle1' })
    );

    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Reload the models of cle1' })
      ).toBeDisabled()
    );
    expect(rowOf('cle1').getByText('Reading the models…')).toBeVisible();
    expect(mocks.client.POST).toHaveBeenCalledTimes(1);
    const [path, init] = mocks.client.POST.mock.calls[0]!;
    expect(path).toBe('/ai/endpoints/{name}/models/reload');
    expect(init.params.path).toEqual({ name: 'cle1' });
    expect(init.params.query).toEqual({
      remoteNode: 'edge',
      workspace: 'global',
    });

    finish({ data: {}, response: { status: 200 } });
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Reload the models of cle1' })
      ).toBeEnabled()
    );
    expect(mocks.mutate).toHaveBeenCalled();
  });

  it('shows a refusal of the reload under the endpoint', async () => {
    mocks.client.POST.mockResolvedValue({
      error: { code: 'forbidden', message: 'denied' },
      response: { status: 403 },
    });
    setQuery('/ai/endpoints', { endpoints: [withList] });
    const user = userEvent.setup();
    renderPage();

    await user.click(
      screen.getByRole('button', { name: 'Reload the models of cle1' })
    );

    expect(
      await rowOf('cle1').findByText(
        'Only an administrator can manage the AI endpoints.'
      )
    ).toBeVisible();
  });

  it('reloads a workspace endpoint in its own scope', async () => {
    setQuery('/ai/endpoints', {
      endpoints: [
        { ...withList, scope: 'workspace:team', allow: { dags: ['*'] } },
      ],
    });
    const user = userEvent.setup();
    renderPage();

    await user.click(
      screen.getByRole('button', { name: 'Reload the models of cle1' })
    );

    await waitFor(() => expect(mocks.client.POST).toHaveBeenCalled());
    expect(mocks.client.POST.mock.calls[0]![1].params.query.workspace).toBe(
      'team'
    );
  });
});

describe('the list of models of an endpoint', () => {
  const names = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5'];

  it('lists the models with a count, and narrows them with the search box', async () => {
    setQuery('/ai/endpoints', { endpoints: [withList] });
    setQuery('/ai/endpoints/{name}/models', {
      ...withList.models,
      models: names,
    });
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole('button', { name: '3 models' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('The models of cle1')).toBeVisible();
    expect(
      within(dialog)
        .getAllByRole('listitem')
        .map((item) => item.textContent)
    ).toEqual(names);
    expect(within(dialog).getByRole('status')).toHaveTextContent(
      '3 of 3 models'
    );
    // The dialog asks for the models of this endpoint in its scope.
    expect(mocks.inits['/ai/endpoints/{name}/models']).toEqual({
      params: {
        path: { name: 'cle1' },
        query: { remoteNode: 'edge', workspace: 'global' },
      },
    });

    await user.type(
      within(dialog).getByLabelText('Search the models'),
      'SONNET'
    );
    expect(
      within(dialog)
        .getAllByRole('listitem')
        .map((item) => item.textContent)
    ).toEqual(['claude-sonnet-5-5']);
    expect(within(dialog).getByRole('status')).toHaveTextContent(
      '1 of 3 models'
    );

    await user.clear(within(dialog).getByLabelText('Search the models'));
    await user.type(within(dialog).getByLabelText('Search the models'), 'zzz');
    expect(within(dialog).getByText('No model matches.')).toBeVisible();
  });

  // The dialog claims nothing about the reads before the list arrives.
  it('says nothing about the last read while the list loads', async () => {
    setQuery('/ai/endpoints', { endpoints: [withList] });
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole('button', { name: '3 models' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Loading…')).toBeVisible();
    expect(within(dialog).queryByText('Not read yet')).toBeNull();
  });
});
