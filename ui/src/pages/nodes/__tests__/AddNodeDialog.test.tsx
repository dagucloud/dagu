// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AddNodeDialog } from '../AddNodeDialog';

const client = vi.hoisted(() => ({ POST: vi.fn() }));

vi.mock('@/hooks/api', () => ({ useClient: () => client }));
vi.mock('@/contexts/ConfigContext', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/contexts/ConfigContext')>()),
  useConfig: () => ({ apiURL: '/api/v1' }),
}));

const FLEET = {
  id: 'inv_fleet00001',
  name: 'fleet',
  nodeCount: 1,
  version: 4,
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-02T00:00:00Z',
};

/** A finished event stream of results, then done. */
function stream(...results: unknown[]): Response {
  const body = results
    .map((result) => `event: result\ndata: ${JSON.stringify(result)}\n\n`)
    .join('');
  return new Response(`${body}event: done\ndata: {}\n\n`, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

const UNKNOWN = {
  node: 'web01',
  ok: false,
  durationMs: 3,
  error: 'host key of node "web01" is unknown',
  hostKey: {
    hop: 'node',
    node: 'web01',
    status: 'unknown',
    presented: 'SHA256:web01',
    expected: [],
  },
};
const PASSED = {
  node: 'web01',
  ok: true,
  fingerprint: 'SHA256:web01',
  durationMs: 80,
};

function renderDialog() {
  const onClose = vi.fn();
  const onAdded = vi.fn();
  render(
    <AddNodeDialog inventories={[FLEET]} onClose={onClose} onAdded={onAdded} />
  );
  return { onClose, onAdded };
}

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

async function click(name: string) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

const TESTS = `/inventories/${FLEET.id}/node-tests`;

/** The parsed body of each request to path. */
function bodiesTo(fetchMock: ReturnType<typeof vi.fn>, path: string) {
  return fetchMock.mock.calls
    .filter(([url]) => url === `/api/v1${path}`)
    .map(([, init]) => JSON.parse(init.body));
}

describe('AddNodeDialog', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    client.POST.mockReset();
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('tests the node, accepts an unknown key on request, and adds the node with that key', async () => {
    fetchMock
      .mockResolvedValueOnce(stream(UNKNOWN))
      .mockResolvedValueOnce(stream(PASSED));
    client.POST.mockResolvedValue({ data: {} });
    const { onClose, onAdded } = renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    fill('Port', '2222');
    fill('Key secret', 'ssh/web');
    fill('Labels', 'role=web, canary');
    const add = screen.getByRole('button', { name: 'Add node' });
    expect(add).toBeDisabled();

    await click('Test connection');
    expect(screen.getByText('SHA256:web01')).toBeInTheDocument();
    expect(add).toBeDisabled();
    await click('Trust this key and test again');
    expect(add).toBeEnabled();
    await click('Add node');

    const node = {
      name: 'web01',
      host: '10.0.1.11',
      port: 2222,
      key: { secret: 'ssh/web' },
      labels: ['role=web', 'canary'],
    };
    const accept = [{ node: 'web01', fingerprint: 'SHA256:web01' }];
    expect(bodiesTo(fetchMock, TESTS)).toEqual([{ node }, { node, accept }]);
    expect(client.POST).toHaveBeenCalledWith(
      '/inventories/{inventoryId}/nodes',
      {
        params: { path: { inventoryId: FLEET.id }, query: { version: 4 } },
        body: { node, trust: accept },
      }
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(onAdded).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('needs a new test after loading the current inventory', async () => {
    fetchMock.mockResolvedValueOnce(stream(PASSED));
    client.POST.mockResolvedValue({
      error: {
        code: 'conflict',
        message: 'inventory changed',
        inventory: { ...FLEET, version: 5, defaults: {}, nodes: [] },
      },
    });
    renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Test connection');
    await click('Add node');
    await click('Load current version');

    expect(screen.getByRole('button', { name: 'Add node' })).toBeDisabled();
  });

  it('needs a new test once a field changes', async () => {
    fetchMock.mockResolvedValueOnce(stream(PASSED));
    renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Test connection');
    const add = screen.getByRole('button', { name: 'Add node' });
    expect(add).toBeEnabled();

    fill('Host', '10.0.1.12');
    expect(add).toBeDisabled();
  });

  // Changing a field back makes the earlier test current again, together
  // with the keys accepted for it.
  it('adds with the accepted key after a field is changed back', async () => {
    fetchMock
      .mockResolvedValueOnce(stream(UNKNOWN))
      .mockResolvedValueOnce(stream(PASSED));
    client.POST.mockResolvedValue({ data: {} });
    renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Test connection');
    await click('Trust this key and test again');
    fill('Description', 'x');
    fill('Description', '');
    await click('Add node');

    expect(client.POST.mock.calls[0][1].body.trust).toEqual([
      { node: 'web01', fingerprint: 'SHA256:web01' },
    ]);
  });

  // A host may present another key by the time it is tested again; a new
  // test then reports that key instead of sending the old one.
  it('starts a new test with no key accepted', async () => {
    fetchMock
      .mockResolvedValueOnce(stream(UNKNOWN))
      .mockResolvedValueOnce(
        stream({ ...PASSED, ok: false, error: 'presents SHA256:other' })
      )
      .mockResolvedValueOnce(stream(UNKNOWN));
    renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Test connection');
    await click('Trust this key and test again');
    await click('Test connection');

    expect(bodiesTo(fetchMock, TESTS).map((body) => body.accept)).toEqual([
      undefined,
      [{ node: 'web01', fingerprint: 'SHA256:web01' }],
      undefined,
    ]);
  });

  it('refuses a key file and a key secret together, before testing', async () => {
    renderDialog();

    fill('Name', 'web01');
    fill('Key file', '/k');
    fill('Key secret', 'ssh/k');
    await click('Test connection');

    expect(screen.getByRole('alert')).toHaveTextContent(
      'Give a key file or a key secret, not both.'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('adds without testing, and retries at the current version after a stale one', async () => {
    client.POST.mockResolvedValueOnce({
      error: {
        code: 'conflict',
        message: 'inventory changed',
        inventory: { ...FLEET, version: 5, defaults: {}, nodes: [] },
      },
    }).mockResolvedValueOnce({ data: {} });
    const { onClose } = renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Add without testing');
    await click('Load current version');
    await click('Add without testing');

    expect(client.POST.mock.calls.map(([, init]) => init.params.query)).toEqual(
      [{ version: 4 }, { version: 5 }]
    );
    expect(client.POST.mock.calls[1][1].body.trust).toBeUndefined();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('keeps the dialog open with the reason when the add is refused', async () => {
    client.POST.mockResolvedValue({
      error: {
        code: 'bad_request',
        message: 'inventory "fleet" already has a node "web01"',
        errors: [],
      },
    });
    const { onClose, onAdded } = renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Add without testing');

    expect(screen.getByRole('alert')).toHaveTextContent(
      'already has a node "web01"'
    );
    expect(onAdded).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('says the node is being added, not tested, while it is added', async () => {
    let finish: (value: { data: object }) => void = () => {};
    client.POST.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      })
    );
    renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Add without testing');

    expect(screen.getByText('Adding...')).toBeInTheDocument();
    expect(screen.queryByText('Testing...')).not.toBeInTheDocument();
    await act(async () => finish({ data: {} }));
  });
});
