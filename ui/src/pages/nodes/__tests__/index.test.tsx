// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import NodesPage from '..';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

type InventoryNode = components['schemas']['InventoryNode'];

function node(overrides: Partial<InventoryNode>): InventoryNode {
  return {
    name: 'web01',
    kind: 'ssh',
    inventory: { id: 'inv-tokyo', name: 'Tokyo production' },
    host: '10.0.1.11',
    port: 22,
    user: 'deploy',
    auth: { keySecret: 'ssh/tokyo' },
    strictHostKey: true,
    timeout: '30s',
    labels: ['role=web', 'site=tokyo'],
    disabled: false,
    ...overrides,
  } as InventoryNode;
}

const NODES = [
  node({}),
  node({
    name: 'db01',
    host: 'db01.internal',
    port: 2222,
    labels: ['role=db'],
    disabled: true,
    strictHostKey: false,
    auth: { keyPath: '/etc/dagu/keys/db', passphraseSecret: 'ssh/db' },
  }),
];

type Responses = {
  nodes?: { data?: unknown; error?: unknown };
  inventories?: unknown[];
};

function respond({ nodes, inventories }: Responses = {}) {
  useQueryMock.mockImplementation((path: string) => {
    switch (path) {
      case '/inventories':
        return {
          data: {
            inventories: inventories ?? [
              { id: 'inv-tokyo', name: 'Tokyo production', nodeCount: 3 },
            ],
          },
        };
      case '/node-labels':
        return { data: { labels: ['role=db', 'role=web'], errors: [] } };
      case '/nodes':
        return {
          data: nodes
            ? nodes.data
            : {
                nodes: NODES,
                pagination: {
                  totalRecords: 2,
                  currentPage: 1,
                  totalPages: 1,
                  nextPage: 1,
                  prevPage: 1,
                  hasNextPage: false,
                },
              },
          error: nodes?.error,
          isLoading: false,
          mutate: vi.fn(),
        };
    }
    throw new Error(`unexpected query ${path}`);
  });
}

function renderPage(remoteNode = 'local') {
  render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={{ setTitle: vi.fn(), selectedRemoteNode: remoteNode } as never}
      >
        <NodesPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

/** The query of the last /nodes request. */
function lastNodesQuery() {
  const calls = useQueryMock.mock.calls.filter(([path]) => path === '/nodes');
  return calls[calls.length - 1]?.[1]?.params?.query;
}

describe('NodesPage', () => {
  beforeEach(() => {
    useQueryMock.mockReset();
    respond();
  });

  it('lists nodes with their inventory, host, and labels, and the match count', () => {
    renderPage();

    expect(screen.getByText('web01')).toBeInTheDocument();
    expect(screen.getAllByText('Tokyo production').length).toBeGreaterThan(0);
    expect(screen.getByText('10.0.1.11')).toBeInTheDocument();
    expect(screen.getByText('db01.internal:2222')).toBeInTheDocument();
    expect(screen.getByText('site=tokyo')).toBeInTheDocument();
    expect(screen.getByText('2 of 3 nodes')).toBeInTheDocument();
  });

  it('marks disabled nodes and nodes that accept any host key', () => {
    renderPage();

    const db = screen.getByText('db01').closest('tr')!;
    expect(db).toHaveTextContent('Disabled');
    expect(db).toHaveTextContent('Any host key');
    const web = screen.getByText('web01').closest('tr')!;
    expect(web).not.toHaveTextContent('Disabled');
    expect(web).not.toHaveTextContent('Any host key');
  });

  it('asks for the nodes that match the labels and the name', async () => {
    renderPage();

    const labels = screen.getByRole('combobox', {
      name: 'Filter by labels...',
    });
    fireEvent.change(labels, { target: { value: 'role=web' } });
    fireEvent.keyDown(labels, { key: 'Enter' });
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Search by name...' }),
      {
        target: { value: ' web ' },
      }
    );

    await waitFor(() =>
      expect(lastNodesQuery()).toMatchObject({
        labels: 'role=web',
        name: 'web',
        page: 1,
      })
    );
  });

  it('shows the error that names an invalid selector term', () => {
    respond({
      nodes: {
        error: { code: 'bad_request', message: 'invalid label filter "role="' },
      },
    });
    renderPage();

    expect(screen.getByRole('alert')).toHaveTextContent(
      'invalid label filter "role="'
    );
  });

  it('opens a drawer with the connection settings and secret refs', () => {
    renderPage();

    fireEvent.click(screen.getByText('db01'));

    const drawer = screen.getByRole('dialog');
    expect(drawer).toHaveTextContent('db01.internal');
    expect(drawer).toHaveTextContent('2222');
    expect(drawer).toHaveTextContent('/etc/dagu/keys/db');
    expect(drawer).toHaveTextContent('ssh/db');
    expect(drawer).toHaveTextContent('Off: any key is accepted');
  });

  it('tells a user without inventories how to import one', () => {
    respond({ inventories: [] });
    renderPage();

    expect(
      screen.getByRole('heading', { name: 'No nodes yet' })
    ).toBeInTheDocument();
    expect(screen.getByText('dagu nodes import')).toBeInTheDocument();
  });

  it('reads no inventory while a remote node is selected', () => {
    renderPage('edge');

    expect(
      screen.getByRole('heading', { name: 'Nodes are kept on each server' })
    ).toBeInTheDocument();
    expect(useQueryMock).not.toHaveBeenCalled();
  });
});
