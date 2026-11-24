// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeEditor } from '../NodeEditor';

const client = vi.hoisted(() => ({
  GET: vi.fn(),
  PUT: vi.fn(),
  DELETE: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({ useClient: () => client }));

type Inventory = components['schemas']['Inventory'];
type InventoryNode = components['schemas']['InventoryNode'];

const SPEC = {
  name: 'web01',
  host: '10.0.1.11',
  port: 2222,
  key: { secret: 'ssh/web' },
  labels: ['web'],
};

const TOKYO: Inventory = {
  id: 'inv_tokyo00001',
  name: 'Tokyo production',
  version: 3,
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-02T00:00:00Z',
  defaults: { labels: ['site=tokyo'] },
  nodes: [SPEC, { name: 'web02', host: '10.0.1.12' }],
};

// The node as the list shows it: with the default labels merged in.
const NODE = {
  name: 'WEB01',
  kind: 'ssh',
  inventory: { id: TOKYO.id, name: TOKYO.name },
  host: SPEC.host,
  port: SPEC.port,
  auth: { keySecret: 'ssh/web' },
  strictHostKey: true,
  timeout: '30s',
  labels: ['site=tokyo', 'web'],
  disabled: false,
} as InventoryNode;

async function renderEditor() {
  const onChanged = vi.fn();
  const onRemoved = vi.fn();
  await act(async () => {
    render(
      <NodeEditor node={NODE} onChanged={onChanged} onRemoved={onRemoved} />
    );
  });
  return { onChanged, onRemoved };
}

async function click(name: string) {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name }));
  });
}

function putBody(call = 0) {
  return client.PUT.mock.calls[call]?.[1];
}

describe('NodeEditor', () => {
  beforeEach(() => {
    for (const fn of Object.values(client)) {
      fn.mockReset();
    }
    client.GET.mockResolvedValue({ data: TOKYO });
    client.PUT.mockResolvedValue({ data: TOKYO });
  });

  it("edits the node's own labels and keeps its other fields", async () => {
    const { onChanged } = await renderEditor();

    const labels = screen.getByLabelText('Node labels');
    expect(labels).toHaveValue('web');
    fireEvent.change(labels, { target: { value: 'web, canary' } });
    await click('Save labels');

    expect(putBody()).toEqual({
      params: {
        path: { inventoryId: TOKYO.id, nodeName: 'web01' },
        query: { version: 3 },
      },
      body: { ...SPEC, labels: ['web', 'canary'] },
    });
    expect(onChanged).toHaveBeenCalled();
    expect(client.GET).toHaveBeenCalledTimes(2);
  });

  it('disables a node', async () => {
    await renderEditor();
    await click('Disable');
    expect(putBody().body).toEqual({ ...SPEC, disabled: true });
  });

  it('enables a disabled node by dropping the field', async () => {
    client.GET.mockResolvedValue({
      data: { ...TOKYO, nodes: [{ ...SPEC, disabled: true }] },
    });
    await renderEditor();
    await click('Enable');
    expect(putBody().body).toEqual(SPEC);
  });

  it('removes the node after asking', async () => {
    client.DELETE.mockResolvedValue({ data: TOKYO });
    const { onRemoved } = await renderEditor();

    await click('Remove');
    const confirm = screen.getByRole('dialog');
    expect(confirm).toHaveTextContent('Its trusted host key is forgotten');
    await act(async () => {
      fireEvent.click(within(confirm).getByRole('button', { name: 'Remove' }));
    });

    expect(client.DELETE).toHaveBeenCalledWith(
      '/inventories/{inventoryId}/nodes/{nodeName}',
      {
        params: {
          path: { inventoryId: TOKYO.id, nodeName: 'web01' },
          query: { version: 3 },
        },
      }
    );
    expect(onRemoved).toHaveBeenCalled();
  });

  it('offers the current version after a change based on an older one', async () => {
    client.PUT.mockResolvedValue({
      error: {
        code: 'conflict',
        message: 'inventory changed',
        inventory: {
          ...TOKYO,
          version: 4,
          nodes: [{ ...SPEC, labels: ['web', 'blue'] }],
        },
      },
    });
    const { onChanged } = await renderEditor();

    await click('Disable');
    await click('Load current version');

    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Node labels')).toHaveValue('web, blue');
  });
});
