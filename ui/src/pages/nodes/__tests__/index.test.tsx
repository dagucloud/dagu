// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  useCanTestNodes,
  useCanTrustNodeHostKeys,
} from '@/contexts/AuthContext';
import { useQuery } from '@/hooks/api';
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import NodesPage from '..';

const clientGet = vi.fn();

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
  useClient: () => ({ GET: clientGet }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanTestNodes: vi.fn(),
  useCanTrustNodeHostKeys: vi.fn(),
}));
vi.mock('@/contexts/ConfigContext', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/contexts/ConfigContext')>()),
  useConfig: () => ({ apiURL: '/api/v1' }),
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;
const canTestMock = useCanTestNodes as unknown as ReturnType<typeof vi.fn>;
const canTrustMock = useCanTrustNodeHostKeys as unknown as ReturnType<
  typeof vi.fn
>;

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
  nodes?: { data?: unknown; error?: unknown; isValidating?: boolean };
  inventories?: unknown[];
  inventoriesError?: unknown;
  retryInventories?: () => void;
};

function pages(currentPage: number, totalPages: number) {
  return {
    totalRecords: totalPages * 50,
    currentPage,
    totalPages,
    nextPage: Math.min(currentPage + 1, totalPages),
    prevPage: Math.max(currentPage - 1, 1),
    hasNextPage: currentPage < totalPages,
  };
}

function respond({
  nodes,
  inventories,
  inventoriesError,
  retryInventories,
}: Responses = {}) {
  useQueryMock.mockImplementation((path: string) => {
    switch (path) {
      case '/inventories':
        if (inventoriesError) {
          return { error: inventoriesError, mutate: retryInventories };
        }
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
          isValidating: nodes?.isValidating ?? false,
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
    canTestMock.mockReturnValue(true);
    canTrustMock.mockReturnValue(true);
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

  it('drops the count and pages of a request that failed', () => {
    // After a failed request the previous filter's rows are still returned
    // next to the error.
    respond({
      nodes: {
        data: { nodes: NODES, pagination: pages(1, 3) },
        error: { code: 'bad_request', message: 'invalid label filter "role="' },
      },
    });
    renderPage();

    expect(screen.getByRole('alert')).toHaveTextContent(
      'invalid label filter "role="'
    );
    expect(screen.queryByText(/of 3 nodes/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Next' })
    ).not.toBeInTheDocument();
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

  it('reports inventories that fail to load, offers a retry, and keeps the nodes', () => {
    const retry = vi.fn();
    respond({
      inventoriesError: { code: 'internal_error', message: 'disk unavailable' },
      retryInventories: retry,
    });
    renderPage();

    expect(screen.getByText("Couldn't load inventories.")).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('disk unavailable');
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(retry).toHaveBeenCalled();
    expect(screen.getByText('web01')).toBeInTheDocument();
    expect(screen.getByText('2 nodes')).toBeInTheDocument();
  });

  it('pages only from the rows of the current request', () => {
    // Page 2 of the previous filter is still shown while page 1 of the new
    // one loads.
    respond({ nodes: { data: { nodes: NODES, pagination: pages(2, 3) } } });
    renderPage();

    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  it('moves to the next page', async () => {
    respond({ nodes: { data: { nodes: NODES, pagination: pages(1, 3) } } });
    renderPage();

    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(lastNodesQuery()).toMatchObject({ page: 2 }));
  });

  it('reads no inventory while a remote node is selected', () => {
    renderPage('edge');

    expect(
      screen.getByRole('heading', { name: 'Nodes are kept on each server' })
    ).toBeInTheDocument();
    expect(useQueryMock).not.toHaveBeenCalled();
  });
});

type ConnectionTest = components['schemas']['InventoryNodeConnectionTest'];

/** A node event stream the test sends events on. */
function eventStream() {
  const encoder = new TextEncoder();
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c;
    },
  });
  return {
    response: new Response(body, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    }),
    send: (name: string, data: unknown) =>
      act(async () => {
        controller.enqueue(
          encoder.encode(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`)
        );
      }),
  };
}

/** A finished stream of results, then done. */
function finished(...results: unknown[]): Response {
  const body = results
    .map((result) => `event: result\ndata: ${JSON.stringify(result)}\n\n`)
    .join('');
  return new Response(`${body}event: done\ndata: {}\n\n`, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

function passed(name: string): ConnectionTest {
  return {
    node: name,
    ok: true,
    fingerprint: `SHA256:${name}`,
    durationMs: 85,
  } as ConnectionTest;
}

function unknownKey(name: string): ConnectionTest {
  return {
    node: name,
    ok: false,
    durationMs: 3,
    error: `host key of node "${name}" is unknown`,
    hostKey: {
      hop: 'node',
      node: name,
      status: 'unknown',
      presented: `SHA256:${name}-new`,
      expected: [],
    },
  } as ConnectionTest;
}

/** The parsed body of each request to path. */
function bodiesTo(fetchMock: ReturnType<typeof vi.fn>, path: string) {
  return fetchMock.mock.calls
    .filter(([url]) => url === `/api/v1${path}`)
    .map(([, init]) => JSON.parse(init.body));
}

describe('NodesPage connection tests', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    useQueryMock.mockReset();
    clientGet.mockReset();
    canTestMock.mockReturnValue(true);
    canTrustMock.mockReturnValue(true);
    respond();
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('tests the checked nodes and shows each result as it arrives', async () => {
    const stream = eventStream();
    fetchMock.mockResolvedValueOnce(stream.response);
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));
    const panel = await screen.findByRole('region', {
      name: 'Connection test',
    });
    expect(bodiesTo(fetchMock, '/node-connection-tests')).toEqual([
      { nodes: ['web01'] },
    ]);
    expect(panel).toHaveTextContent('Tested 0 of 1');

    await stream.send('result', passed('web01'));
    expect(panel).toHaveTextContent('SHA256:web01');
    expect(panel).toHaveTextContent('1 passed, 0 failed');
    expect(
      screen.getByText('web01', { selector: 'td span' }).closest('tr')
    ).toHaveTextContent('Passed');
    await stream.send('done', { passed: 1, failed: 0 });

    // The drawer shows the node's last test.
    fireEvent.click(screen.getByText('web01', { selector: 'td span' }));
    const drawer = screen.getByRole('dialog');
    expect(drawer).toHaveTextContent('Last test');
    expect(drawer).toHaveTextContent('SHA256:web01');
  });

  it('tests every matched node when none is checked, reading them a page at a time', async () => {
    clientGet
      .mockResolvedValueOnce({
        data: { nodes: [NODES[0]], pagination: pages(1, 2) },
      })
      .mockResolvedValueOnce({
        data: { nodes: [NODES[1]], pagination: pages(2, 2) },
      });
    fetchMock.mockResolvedValueOnce(finished(passed('web01'), passed('db01')));
    renderPage();

    const labels = screen.getByRole('combobox', {
      name: 'Filter by labels...',
    });
    fireEvent.change(labels, { target: { value: 'role=web' } });
    fireEvent.keyDown(labels, { key: 'Enter' });
    fireEvent.click(screen.getByRole('button', { name: 'Test matched nodes' }));

    await screen.findByText('2 passed, 0 failed');
    expect(clientGet.mock.calls.map(([, init]) => init.params.query)).toEqual([
      expect.objectContaining({ page: 1, perPage: 200, labels: 'role=web' }),
      expect.objectContaining({ page: 2, perPage: 200, labels: 'role=web' }),
    ]);
    expect(bodiesTo(fetchMock, '/node-connection-tests')).toEqual([
      { nodes: ['web01', 'db01'] },
    ]);
  });

  it('starts one test when clicked again while the matched nodes load', async () => {
    let listed!: (value: unknown) => void;
    clientGet.mockReturnValue(new Promise((resolve) => (listed = resolve)));
    fetchMock.mockResolvedValueOnce(finished(passed('web01')));
    renderPage();

    const button = screen.getByRole('button', { name: 'Test matched nodes' });
    fireEvent.click(button);
    expect(button).toBeDisabled();
    fireEvent.click(button);
    await act(async () =>
      listed({ data: { nodes: [NODES[0]], pagination: pages(1, 1) } })
    );

    await screen.findByText('1 passed, 0 failed');
    expect(clientGet).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('blocks checking nodes while a new filter loads', () => {
    respond({
      nodes: {
        data: { nodes: NODES, pagination: pages(1, 1) },
        isValidating: true,
      },
    });
    renderPage();

    expect(
      screen.getByRole('checkbox', { name: 'Check web01' })
    ).toBeDisabled();
    expect(
      screen.getByRole('checkbox', { name: 'Check the nodes of this page' })
    ).toBeDisabled();
  });

  it('does not show an earlier result as the result of a run that failed', async () => {
    fetchMock
      .mockResolvedValueOnce(finished(passed('web01')))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ message: 'secret store unavailable' }), {
          status: 500,
          headers: { 'Content-Type': 'application/json' },
        })
      );
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));
    await screen.findByText('1 passed, 0 failed');
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));

    const panel = screen.getByRole('region', { name: 'Connection test' });
    expect(await within(panel).findByRole('alert')).toHaveTextContent(
      'secret store unavailable'
    );
    expect(panel).toHaveTextContent('Not tested');
    expect(panel).not.toHaveTextContent('SHA256:web01');
  });

  it('refuses to test more than 1,000 matched nodes', async () => {
    clientGet.mockResolvedValue({
      data: {
        nodes: NODES,
        pagination: { ...pages(1, 1), totalRecords: 1001 },
      },
    });
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Test matched nodes' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'A test takes at most 1,000 nodes.'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('shows why the server refused a test', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          code: 'forbidden',
          message: 'insufficient permissions',
        }),
        { status: 403, headers: { 'Content-Type': 'application/json' } }
      )
    );
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'insufficient permissions'
    );
  });

  it('trusts unknown keys on one confirmation, then tests their nodes again', async () => {
    fetchMock
      .mockResolvedValueOnce(finished(unknownKey('web01'), unknownKey('db01')))
      .mockResolvedValueOnce(
        finished(
          {
            node: 'web01',
            fingerprint: 'SHA256:web01-new',
            outcome: 'trusted',
          },
          { node: 'db01', fingerprint: 'SHA256:db01-new', outcome: 'trusted' }
        )
      )
      .mockResolvedValueOnce(finished(passed('web01'), passed('db01')));
    renderPage();

    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Check the nodes of this page' })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Test 2 checked' }));
    fireEvent.click(
      await screen.findByRole('button', { name: 'Trust 2 unknown keys' })
    );
    const confirm = screen.getByRole('dialog');
    expect(confirm).toHaveTextContent('SHA256:web01-new');
    expect(confirm).toHaveTextContent('SHA256:db01-new');
    fireEvent.click(within(confirm).getByRole('button', { name: 'Trust' }));

    await screen.findByText('2 passed, 0 failed');
    expect(bodiesTo(fetchMock, '/node-host-keys')).toEqual([
      {
        keys: [
          { node: 'web01', fingerprint: 'SHA256:web01-new' },
          { node: 'db01', fingerprint: 'SHA256:db01-new' },
        ],
      },
    ]);
    expect(bodiesTo(fetchMock, '/node-connection-tests')).toEqual([
      { nodes: ['web01', 'db01'] },
      { nodes: ['web01', 'db01'] },
    ]);
  });

  it('replaces a changed key only after showing the trusted and presented keys', async () => {
    const changed = {
      node: 'web01',
      ok: false,
      durationMs: 3,
      error: 'host key of node "web01" has changed',
      hostKey: {
        hop: 'node',
        node: 'web01',
        status: 'changed',
        presented: 'SHA256:new',
        expected: ['SHA256:old'],
        trusted: 'SHA256:old',
      },
    };
    fetchMock
      .mockResolvedValueOnce(finished(changed))
      .mockResolvedValueOnce(
        finished({
          node: 'web01',
          fingerprint: 'SHA256:new',
          previous: 'SHA256:old',
          outcome: 'replaced',
        })
      )
      .mockResolvedValueOnce(finished(passed('web01')));
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Replace key' }));
    const confirm = screen.getByRole('dialog');
    expect(confirm).toHaveTextContent('Replace trusted host key');
    expect(confirm).toHaveTextContent('TrustedSHA256:old');
    expect(confirm).toHaveTextContent('PresentedSHA256:new');
    fireEvent.click(
      within(confirm).getByRole('button', { name: 'Replace key' })
    );

    await screen.findByText('Key replaced');
    expect(bodiesTo(fetchMock, '/node-host-keys')).toEqual([
      {
        keys: [
          { node: 'web01', fingerprint: 'SHA256:new', replace: 'SHA256:old' },
        ],
      },
    ]);
    await screen.findByText('1 passed, 0 failed');
  });

  it('keeps a trust that did not finish reported while its nodes are tested again', async () => {
    fetchMock
      .mockResolvedValueOnce(finished(unknownKey('web01'), unknownKey('db01')))
      .mockResolvedValueOnce(
        // The stream closes after the first result, before done.
        new Response(
          `event: result\ndata: ${JSON.stringify({ node: 'web01', fingerprint: 'SHA256:web01-new', outcome: 'trusted' })}\n\n`,
          { status: 200, headers: { 'Content-Type': 'text/event-stream' } }
        )
      )
      .mockResolvedValueOnce(finished(passed('web01')));
    renderPage();

    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Check the nodes of this page' })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Test 2 checked' }));
    fireEvent.click(
      await screen.findByRole('button', { name: 'Trust 2 unknown keys' })
    );
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Trust' })
    );

    await screen.findByText('1 passed, 1 failed');
    expect(bodiesTo(fetchMock, '/node-connection-tests')[1]).toEqual({
      nodes: ['web01'],
    });
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The connection closed before every node finished.'
    );
  });

  it('tests nothing again when the panel closes during a trust', async () => {
    const trustStream = eventStream();
    fetchMock
      .mockResolvedValueOnce(finished(unknownKey('web01')))
      .mockResolvedValueOnce(trustStream.response);
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Trust key' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Trust' })
    );
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await trustStream.send('result', {
      node: 'web01',
      fingerprint: 'SHA256:web01-new',
      outcome: 'trusted',
    });
    await trustStream.send('done', { trusted: 1, failed: 0 });

    expect(
      screen.queryByRole('region', { name: 'Connection test' })
    ).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('offers no trust for a key that differs only from the pinned key', async () => {
    fetchMock.mockResolvedValueOnce(
      finished({
        node: 'web01',
        ok: false,
        durationMs: 3,
        error: 'host key of node "web01" has changed',
        hostKey: {
          hop: 'node',
          node: 'web01',
          status: 'changed',
          presented: 'SHA256:new',
          expected: ['SHA256:pinned'],
        },
      })
    );
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));

    await screen.findByText('0 passed, 1 failed');
    expect(
      screen.queryByRole('button', { name: 'Replace key' })
    ).not.toBeInTheDocument();
  });

  it('offers no trust to a user who cannot trust keys', async () => {
    canTrustMock.mockReturnValue(false);
    fetchMock.mockResolvedValueOnce(finished(unknownKey('web01')));
    renderPage();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Check web01' }));
    fireEvent.click(screen.getByRole('button', { name: 'Test 1 checked' }));

    await screen.findByText('0 passed, 1 failed');
    expect(
      screen.queryByRole('button', { name: 'Trust key' })
    ).not.toBeInTheDocument();
  });

  it('offers no test to a user who cannot test nodes', () => {
    canTestMock.mockReturnValue(false);
    renderPage();

    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Test matched nodes' })
    ).not.toBeInTheDocument();
  });
});
