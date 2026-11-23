// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AddNodeDialog } from '../AddNodeDialog';

const client = vi.hoisted(() => ({ PUT: vi.fn() }));

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

/** The parsed body of each request to path. */
function bodiesTo(fetchMock: ReturnType<typeof vi.fn>, path: string) {
  return fetchMock.mock.calls
    .filter(([url]) => url === `/api/v1${path}`)
    .map(([, init]) => JSON.parse(init.body));
}

describe('AddNodeDialog', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    client.PUT.mockReset();
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('tests the node, accepts an unknown key on request, adds the node, and then trusts the key', async () => {
    fetchMock
      .mockResolvedValueOnce(stream(UNKNOWN))
      .mockResolvedValueOnce(stream(PASSED))
      .mockResolvedValueOnce(
        stream({
          node: 'web01',
          fingerprint: 'SHA256:web01',
          outcome: 'trusted',
        })
      );
    client.PUT.mockResolvedValue({ data: {} });
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
    expect(bodiesTo(fetchMock, '/node-connection-tests')).toEqual([
      { node, inventory: FLEET.id },
      {
        node,
        inventory: FLEET.id,
        accept: [{ node: 'web01', fingerprint: 'SHA256:web01' }],
      },
    ]);
    expect(client.PUT).toHaveBeenCalledWith(
      '/inventories/{inventoryId}/nodes/{nodeName}',
      {
        params: {
          path: { inventoryId: FLEET.id, nodeName: 'web01' },
          query: { version: 4 },
        },
        body: node,
      }
    );
    expect(bodiesTo(fetchMock, '/node-host-keys')).toEqual([
      { keys: [{ node: 'web01', fingerprint: 'SHA256:web01' }] },
    ]);
    expect(onAdded).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
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
  it('trusts the accepted key after a field is changed back', async () => {
    fetchMock
      .mockResolvedValueOnce(stream(UNKNOWN))
      .mockResolvedValueOnce(stream(PASSED))
      .mockResolvedValueOnce(
        stream({
          node: 'web01',
          fingerprint: 'SHA256:web01',
          outcome: 'trusted',
        })
      );
    client.PUT.mockResolvedValue({ data: {} });
    renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Test connection');
    await click('Trust this key and test again');
    fill('Description', 'x');
    fill('Description', '');
    await click('Add node');

    expect(bodiesTo(fetchMock, '/node-host-keys')).toEqual([
      { keys: [{ node: 'web01', fingerprint: 'SHA256:web01' }] },
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
    client.PUT.mockResolvedValueOnce({
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

    expect(client.PUT.mock.calls.map(([, init]) => init.params.query)).toEqual([
      { version: 4 },
      { version: 5 },
    ]);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('keeps the dialog open when the node is added but its key is not trusted', async () => {
    fetchMock
      .mockResolvedValueOnce(stream(UNKNOWN))
      .mockResolvedValueOnce(stream(PASSED))
      .mockResolvedValueOnce(
        stream({
          node: 'web01',
          fingerprint: 'SHA256:web01',
          error: 'node "web01" presents SHA256:other',
        })
      );
    client.PUT.mockResolvedValue({ data: {} });
    const { onClose, onAdded } = renderDialog();

    fill('Name', 'web01');
    fill('Host', '10.0.1.11');
    await click('Test connection');
    await click('Trust this key and test again');
    await click('Add node');

    expect(onAdded).toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The node was added, but a host key was not trusted'
    );
  });
});
