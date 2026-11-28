// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  WorkerConnection,
  WorkerInviteKind,
  type components,
} from '@/api/v1/schema';
import { QueryFeedback } from '@/components/QueryFeedback';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useQuery } from '@/hooks/api';
import WorkerInvites, {
  JOIN_REREAD_DELAY_MS,
  inviteBaseline,
} from '../WorkerInvites';
import {
  Providers,
  hoursFromNow,
  installServer,
  invite,
  invitesAnswer,
  refused,
  requests,
  server,
  setTabVisible,
  worker,
} from './workerAdminFixtures';

type Worker = components['schemas']['Worker'];

vi.mock('@/hooks/api', async () =>
  (await import('./workerAdminFixtures')).realApiModule()
);
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: vi.fn() }));

// System status as it reads its workers from the API: the list follows
// the workers the API reports, each read on request.
function WorkersPage() {
  const { data, isLoading, mutate } = useQuery('/workers', {
    params: { query: { remoteNode: 'local' } },
  });
  return (
    <>
      <button type="button" onClick={() => void mutate()}>
        Read workers
      </button>
      <WorkerInvites
        workers={inviteBaseline({
          workers: data?.workers,
          settled: !isLoading,
        })}
        live
        refreshes={0}
      />
    </>
  );
}

interface PageState {
  workers: Worker[] | undefined;
  live: boolean;
  refreshes: number;
}

// Renders the list as System status does; `update` changes what the page
// passes, as its refresh of /workers, its auto refresh, or a manual refresh.
// `null` renders the list before the page's first workers list arrived.
function renderInvites(
  workers: Worker[] | null = [worker('worker-a')],
  live = true
) {
  let state: PageState = { workers: workers ?? undefined, live, refreshes: 0 };
  const view = render(<WorkerInvites {...state} />, { wrapper: Providers });
  return {
    ...view,
    update: (next: Worker[] | undefined, nextLive = state.live) => {
      state = { ...state, workers: next, live: nextLive };
      view.rerender(<WorkerInvites {...state} />);
    },
    refresh: () => {
      state = { ...state, refreshes: state.refreshes + 1 };
      view.rerender(<WorkerInvites {...state} />);
    },
    rerender: () => view.rerender(<WorkerInvites {...state} />),
  };
}

// Holds each DELETE until the test answers it, in the order they came.
function heldDeletes() {
  const pending: Array<(answer: { status: number; body?: unknown }) => void> =
    [];
  server.del = () => new Promise((resolve) => pending.push(resolve));
  return pending;
}

// Confirms the delete of an invite from its row.
async function startDelete(id: string) {
  await userEvent.click(
    await screen.findByRole('button', {
      name: new RegExp(`^Delete invite ${id}`),
    })
  );
  await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
}

async function rows() {
  return screen.findAllByRole('listitem');
}

function reads() {
  return requests('GET', '/workers/invites').length;
}

describe('WorkerInvites', () => {
  beforeEach(() => {
    installServer();
    setTabVisible(true);
    vi.mocked(useIsAdmin).mockReturnValue(true);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('is hidden from non-admins and reads nothing', () => {
    vi.mocked(useIsAdmin).mockReturnValue(false);

    const { container } = renderInvites();

    expect(container).toBeEmptyDOMElement();
    expect(reads()).toBe(0);
  });

  it('says when there are no invites', async () => {
    renderInvites();

    expect(await screen.findByText('No invites')).toBeInTheDocument();
  });

  it('lists a single-use invite with its ID, creator, times, and uses', async () => {
    server.invites = invitesAnswer([
      invite('a4611fb6-951a', {
        createdBy: 'alice',
        expiresAt: hoursFromNow(23),
        address: '192.168.1.20:50055',
      }),
    ]);

    renderInvites();

    const [row] = await rows();
    expect(row).toHaveTextContent('Single use');
    expect(row).toHaveTextContent('a4611fb6 · 192.168.1.20:50055');
    expect(row).toHaveTextContent('alice');
    expect(row).toHaveTextContent('in 23h');
    expect(row).toHaveTextContent('0 uses');
    expect(row).toHaveTextContent('Last use Never');
    expect(row).toHaveTextContent('Active');
  });

  it('lists a reusable invite without an expiry as active after uses', async () => {
    server.invites = invitesAnswer([
      invite('inv-1', {
        kind: WorkerInviteKind.reusable,
        useCount: 3,
        lastUsedAt: hoursFromNow(-2),
      }),
    ]);

    renderInvites();

    const [row] = await rows();
    expect(row).toHaveTextContent('Reusable');
    expect(row).toHaveTextContent('No expiry');
    expect(row).toHaveTextContent('3 uses');
    expect(row).toHaveTextContent('2h ago');
    expect(row).toHaveTextContent('Active');
  });

  it('shows an expired invite as expired', async () => {
    server.invites = invitesAnswer([
      invite('inv-1', { expiresAt: hoursFromNow(-3) }),
    ]);

    renderInvites();

    const [row] = await rows();
    expect(row).toHaveTextContent('3h ago');
    expect(row).toHaveTextContent('Expired');
  });

  // An invite that ended stays listed for 7 days (RFC 029 §Invites), so an
  // admin can see what happened to it; the coordinator prunes it later.
  it('shows an ended invite for 7 days', async () => {
    server.invites = invitesAnswer([
      invite('used', { useCount: 1, lastUsedAt: hoursFromNow(-1) }),
      invite('old-use', {
        useCount: 1,
        createdAt: hoursFromNow(-24 * 9),
        lastUsedAt: hoursFromNow(-24 * 8),
      }),
      invite('old-expiry', {
        kind: WorkerInviteKind.reusable,
        createdAt: hoursFromNow(-24 * 9),
        expiresAt: hoursFromNow(-24 * 7.5),
      }),
    ]);

    renderInvites();

    const shown = await rows();
    expect(shown).toHaveLength(1);
    expect(shown[0]).toHaveTextContent('1 use');
    expect(shown[0]).toHaveTextContent('Used');
  });

  // The page's refresh may be paused, so the list follows the clock on its
  // own: an invite turns Expired at its expiry, and an ended one leaves after
  // 7 days, with no read.
  it('updates the states from the clock without reading again', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([
      invite('expiring', { expiresAt: hoursFromNow(0.01) }),
      invite('leaving', {
        useCount: 1,
        createdAt: hoursFromNow(-24 * 8),
        lastUsedAt: new Date(Date.now() - 7 * 24 * 3600e3 + 30e3).toISOString(),
      }),
    ]);
    renderInvites([], false);
    expect(await rows()).toHaveLength(2);
    expect(
      screen.getByRole('listitem', { name: 'Invite expiring' })
    ).toHaveTextContent('Active');

    await act(() => vi.advanceTimersByTimeAsync(2 * 60 * 1000));

    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(
      screen.getByRole('listitem', { name: 'Invite expiring' })
    ).toHaveTextContent('Expired');
    expect(reads()).toBe(1);
  });

  // A used invite keeps its state when its expiry passes; its expiry still
  // reads as past once it is, also while the page's refresh is paused.
  it('shows a passed expiry of a used invite as past', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([
      invite('used', {
        useCount: 1,
        lastUsedAt: hoursFromNow(-1),
        expiresAt: new Date(Date.now() + 30e3).toISOString(),
      }),
    ]);
    renderInvites([], false);
    const [row] = await rows();
    expect(row).toHaveTextContent('Expiry in 30s');

    await act(() => vi.advanceTimersByTimeAsync(2 * 60 * 1000));

    expect(row).toHaveTextContent(/Expiry 2m ago/);
    expect(row).toHaveTextContent('Used');
    expect(reads()).toBe(1);
  });

  // The expiry turns past at the expiry itself, not at the next minute of
  // the list's clock: it never reads as ahead once it passed.
  it('never shows a passed expiry as ahead', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([
      invite('used', {
        useCount: 1,
        lastUsedAt: hoursFromNow(-1),
        expiresAt: new Date(Date.now() + 20e3).toISOString(),
      }),
    ]);
    renderInvites([], false);
    const [row] = await rows();

    await act(() => vi.advanceTimersByTimeAsync(30e3));

    expect(row).not.toHaveTextContent(/Expiry in/);
    expect(row).toHaveTextContent(/Expiry 10s ago/);
  });

  // The state follows the same clock as the expiry: an invite reads
  // Expired as soon as its expiry reads as past, not at the next minute.
  it('turns the state with the expiry', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([
      invite('expiring', {
        expiresAt: new Date(Date.now() + 20e3).toISOString(),
      }),
    ]);
    renderInvites([], false);
    const [row] = await rows();
    expect(row).toHaveTextContent('Active');

    await act(() => vi.advanceTimersByTimeAsync(30e3));

    expect(row).toHaveTextContent(/Expiry 10s ago/);
    expect(row).toHaveTextContent('Expired');
    expect(row).not.toHaveTextContent('Active');
  });

  it('deletes the invite it names after one confirmation', async () => {
    server.invites = invitesAnswer([
      invite('a4611fb6-951a', { kind: WorkerInviteKind.reusable }),
      invite('84e24639-a148'),
    ]);

    renderInvites();
    await userEvent.click(
      await screen.findByRole('button', {
        name: 'Delete invite a4611fb6 (Reusable)',
      })
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Invite a4611fb6: Reusable, created');
    expect(dialog).toHaveTextContent(
      'Workers that joined with it keep their certificates'
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Delete' })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    expect(requests('DELETE', '/workers/invites/a4611fb6-951a')).toHaveLength(
      1
    );
  });

  // The server deletes on its own coordinators only. A remote node still
  // lists its invites, with no Delete.
  it('lists the invites of a remote node without Delete', async () => {
    server.node = 'node-b';
    server.invites = invitesAnswer([invite('inv-1')]);

    renderInvites();

    expect(await rows()).toHaveLength(1);
    expect(
      requests('GET', '/workers/invites')[0]?.query.get('remoteNode')
    ).toBe('node-b');
    expect(
      screen.queryByRole('button', { name: /^Delete invite/ })
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(
        'Delete works only for the server this page belongs to. To delete an invite of the selected node, open the Dagu UI of that node.'
      )
    ).toBeInTheDocument();
  });

  it.each([
    [
      403,
      refused(403, 'insufficient role', 'forbidden'),
      'Only admins can manage workers and invites.',
      'Sign in with an admin account, or ask an admin.',
    ],
    [
      404,
      refused(404, 'no such invite', 'not_found'),
      'This invite is already gone.',
      'The list is read again. Close this dialog.',
    ],
    [
      409,
      refused(
        409,
        'invites need the built-in CA',
        'conflict',
        'own_certificates'
      ),
      'The coordinator uses its own certificates, so it has no built-in CA for invites.',
      'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.',
    ],
    [
      503,
      refused(503, 'no coordinator', 'internal_error', 'no_coordinator'),
      'No coordinator is running or reachable.',
      'Start the coordinator, or check that this server can reach it, then try again.',
    ],
  ])(
    'announces the cause and the change of a %i on delete',
    async (_status, answer, cause, change) => {
      server.invites = invitesAnswer([invite('inv-1')]);
      server.del = () => answer;

      renderInvites();
      await userEvent.click(
        await screen.findByRole('button', { name: /^Delete invite inv-1/ })
      );
      await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

      const alert = await within(screen.getByRole('dialog')).findByRole(
        'alert'
      );
      expect(alert).toHaveTextContent(cause);
      expect(alert).toHaveTextContent(change);
    }
  );

  // A non-admin gets 403 before the request is checked, a bad ID included.
  // The page shows its own admin text and never the server's words.
  it('shows only the admin text for a 403 on delete', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    server.del = () => refused(403, 'Insufficient permissions', 'forbidden');

    render(
      <QueryFeedback>
        <WorkerInvites workers={[worker('worker-a')]} live refreshes={0} />
      </QueryFeedback>,
      { wrapper: Providers }
    );
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const alert = await within(screen.getByRole('dialog')).findByRole('alert');
    expect(alert).toHaveTextContent(
      'Only admins can manage workers and invites.'
    );
    expect(document.body).not.toHaveTextContent('Insufficient permissions');
    expect(screen.queryByText(/Server message/)).not.toBeInTheDocument();
  });

  // A delete that fails after its dialog was closed is announced in the
  // list once no confirmation is open: under an open one the page is
  // hidden from screen readers. A newer confirmation stays as it is.
  it('announces a delete that fails after its dialog was closed', async () => {
    server.invites = invitesAnswer([invite('inv-1'), invite('inv-2')]);
    const pending = heldDeletes();
    renderInvites();
    await startDelete('inv-1');
    await userEvent.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    await userEvent.click(
      screen.getByRole('button', { name: /^Delete invite inv-2/ })
    );
    const newer = screen.getByRole('dialog');

    await act(async () =>
      pending[0]!(
        refused(503, 'server words', 'internal_error', 'no_coordinator')
      )
    );
    await act(() => Promise.resolve());
    expect(screen.getByRole('dialog')).toBe(newer);
    expect(within(newer).queryByRole('alert')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Invite inv-1 was not deleted.')
    ).not.toBeInTheDocument();

    await userEvent.click(
      within(newer).getByRole('button', { name: 'Cancel' })
    );
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Invite inv-1 was not deleted.');
    expect(alert).toHaveTextContent('No coordinator is running or reachable.');
    expect(document.body).not.toHaveTextContent('server words');

    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  // The outcome of a delete outlives a switch to another node and back.
  it('keeps a late delete failure across a node switch', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const pending = heldDeletes();
    const { rerender } = renderInvites();
    await startDelete('inv-1');

    server.node = 'node-b';
    rerender();
    await act(async () =>
      pending[0]!(refused(503, '', 'internal_error', 'no_coordinator'))
    );
    server.node = 'local';
    rerender();

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invite inv-1 was not deleted.'
    );
  });

  // Each invite keeps its own late failure.
  it('keeps the late failures of two invites', async () => {
    server.invites = invitesAnswer([invite('inv-1'), invite('inv-2')]);
    const pending = heldDeletes();
    renderInvites();
    await startDelete('inv-1');
    await userEvent.keyboard('{Escape}');
    await startDelete('inv-2');
    await userEvent.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );

    await act(async () => {
      pending[0]!(refused(503, '', 'internal_error', 'no_coordinator'));
      pending[1]!(refused(503, '', 'internal_error', 'no_coordinator'));
    });

    await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2));
    const alerts = screen.getAllByRole('alert');
    expect(alerts[0]).toHaveTextContent('Invite inv-1 was not deleted.');
    expect(alerts[1]).toHaveTextContent('Invite inv-2 was not deleted.');
  });

  // A delete that succeeds clears the failure of an earlier one.
  it('clears a late delete failure once a retry deletes the invite', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const pending = heldDeletes();
    renderInvites();
    await startDelete('inv-1');
    await userEvent.keyboard('{Escape}');
    await act(async () =>
      pending[0]!(refused(503, '', 'internal_error', 'no_coordinator'))
    );
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invite inv-1 was not deleted.'
    );

    server.invites = invitesAnswer([]);
    await startDelete('inv-1');
    await act(async () => pending[1]!({ status: 204 } as never));

    expect(await screen.findByText('No invites')).toBeInTheDocument();
    expect(
      screen.queryByText('Invite inv-1 was not deleted.')
    ).not.toBeInTheDocument();
  });

  // An invite has one delete under way at most: its Delete waits for the
  // first request to settle, also after that dialog was closed.
  it('sends one delete at a time for an invite', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const pending = heldDeletes();
    renderInvites();
    await startDelete('inv-1');
    await userEvent.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );

    const row = screen.getByRole('button', { name: /^Delete invite inv-1/ });
    expect(row).toBeDisabled();
    await userEvent.click(row);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(requests('DELETE', '/workers/invites/inv-1')).toHaveLength(1);

    await act(async () =>
      pending[0]!(refused(503, '', 'internal_error', 'no_coordinator'))
    );
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Invite inv-1 was not deleted.'
    );
    expect(
      screen.getByRole('button', { name: /^Delete invite inv-1/ })
    ).toBeEnabled();
  });

  // A bad ID is refused before any coordinator call. The words of that
  // refusal appear nowhere, in the dialog or in a page notice.
  it('keeps the words of a refused bad ID off the page', async () => {
    const words =
      'this is not an invite ID: list the invites to find their IDs';
    server.invites = invitesAnswer([invite('inv-1')]);
    server.del = () => refused(400, words, 'bad_request');

    render(
      <QueryFeedback>
        <WorkerInvites workers={[worker('worker-a')]} live refreshes={0} />
      </QueryFeedback>,
      { wrapper: Providers }
    );
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const alert = await within(screen.getByRole('dialog')).findByRole('alert');
    expect(alert).toHaveTextContent('The request failed.');
    expect(alert).toHaveTextContent(
      'Please try again or check the server connection.'
    );
    expect(document.body).not.toHaveTextContent('not an invite ID');
  });

  // One reason covers each way TLS fails on the way to the coordinator. The
  // advice names every remedy, whichever the server's words describe.
  it.each([
    'the coordinator requires TLS: this server calls it over plaintext',
    'the coordinator requires TLS: this server has no CA certificate for it',
  ])('gives every TLS remedy for require_tls: %s', async (words) => {
    server.invites = refused(503, words, 'internal_error', 'require_tls');

    renderInvites();

    const change = await screen.findByText(/^Clear peer\.insecure: true/);
    expect(change).toHaveTextContent('peer.ca_cert_file');
    expect(change).toHaveTextContent(
      'check that the network path to the coordinator passes TLS'
    );
    expect(document.body).not.toHaveTextContent(words);
  });

  // The API names why it refused in details.reason; the explanation follows
  // it, not the wording of the message.
  it.each([
    [
      'ca_unavailable',
      'The coordinator could not create or read its CA.',
      'See the coordinator start warning, fix its CA, then restart it.',
    ],
    [
      'peer_token_missing',
      'This server has no peer token for the coordinator.',
      'Set peer.token on this server to the peer token of the coordinator.',
    ],
    [
      'peer_token_mismatch',
      'This server and the coordinator have different peer tokens.',
      'Give the server the same peer.token as the coordinator.',
    ],
    [
      'require_tls',
      'The coordinator requires TLS, and this server called it over plaintext.',
      'Clear peer.insecure: true or peer.skip_tls_verify: true in the server config if set, and set peer.ca_cert_file to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.',
    ],
    [
      'older_coordinator',
      'The coordinator runs an earlier Dagu release without worker invites.',
      'Upgrade the coordinator.',
    ],
    [
      'coordinator_conflict',
      'The coordinator could not do this in its current state.',
      'Check the coordinator log for the state that conflicts, then try again once it is resolved.',
    ],
    [
      'retry_later',
      'The coordinator answered but cannot do this yet.',
      'Wait a moment, then try again.',
    ],
  ])(
    'explains why the invites cannot be listed: %s',
    async (reason, cause, change) => {
      server.invites = refused(503, 'server words', 'internal_error', reason);

      renderInvites();

      expect(await screen.findByText(cause)).toBeInTheDocument();
      expect(screen.getByText(change)).toBeInTheDocument();
      expect(screen.queryByText('server words')).not.toBeInTheDocument();
    }
  );

  it('says a non-admin cannot list the invites', async () => {
    server.invites = refused(403, 'insufficient role', 'forbidden');

    renderInvites();

    expect(
      await screen.findByText('Only admins can manage workers and invites.')
    ).toBeInTheDocument();
  });

  // A coordinator refusal names its cause only in the server's message: the
  // page explains it in its own words, with the message quoted below.
  it('quotes the server message below a coordinator refusal', async () => {
    const words = 'the coordinator refused the server: certificate expired';
    server.invites = refused(
      503,
      words,
      'internal_error',
      'coordinator_refused'
    );

    renderInvites();

    expect(
      await screen.findByText('The coordinator refused the server.')
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        'Check the coordinator log for the reason, or run dagu worker --check on this host, then try again.'
      )
    ).toBeInTheDocument();
    expect(screen.getByText(`Server message: “${words}”`)).toBeInTheDocument();
    expect(screen.queryByText(words)).not.toBeInTheDocument();
  });

  // A reason the page does not know, even one named like a built-in
  // property, is explained by the status, with the message quoted below.
  it.each(['toString', 'constructor', 'some_new_reason'])(
    'explains the unknown reason %s by its status',
    async (reason) => {
      server.invites = refused(503, 'server words', 'internal_error', reason);

      renderInvites();

      expect(
        await screen.findByText('The request failed.')
      ).toBeInTheDocument();
      expect(
        screen.getByText('Please try again or check the server connection.')
      ).toBeInTheDocument();
      expect(
        screen.getByText('Server message: “server words”')
      ).toBeInTheDocument();
    }
  );

  // A known reason has the page's own words only.
  it('shows no server message for a known reason', async () => {
    server.invites = refused(
      503,
      'run the server where it reads the data directory',
      'internal_error',
      'peer_token_missing'
    );

    renderInvites();

    expect(
      await screen.findByText(
        'This server has no peer token for the coordinator.'
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/Server message/)).not.toBeInTheDocument();
  });

  // The list explains a failed read itself; the page's notice of a failed
  // background read would repeat the server's words.
  it('raises no page notice for a failed read', async () => {
    server.invites = refused(
      503,
      'the coordinator refused the server: set peer.token, or run the server where it reads the data directory',
      'internal_error',
      'peer_token_missing'
    );

    render(
      <QueryFeedback>
        <WorkerInvites workers={[worker('worker-a')]} live refreshes={0} />
      </QueryFeedback>,
      { wrapper: Providers }
    );

    expect(
      await screen.findByText(
        'This server has no peer token for the coordinator.'
      )
    ).toBeInTheDocument();
    expect(
      screen.queryByText('Background request failed')
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/data directory/)).not.toBeInTheDocument();
  });

  // Without a reason, the advice follows the status, and the message is
  // never read for meaning or shown.
  it('explains a refusal without a reason by its status', async () => {
    const words =
      'invites need the built-in CA: this coordinator uses peer.cert_file';
    server.invites = refused(409, words);

    renderInvites();

    expect(
      await screen.findByText('The coordinator refused the request.')
    ).toBeInTheDocument();
    expect(screen.queryByText(words)).not.toBeInTheDocument();
    expect(screen.queryByText(/To use invites/)).not.toBeInTheDocument();
  });

  // Only a list is a list: a 200 without one is a failure, not No invites.
  it('treats a 200 without a list as a failure', async () => {
    server.invites = { status: 200, body: {} };

    renderInvites();

    expect(await screen.findByText('The request failed.')).toBeInTheDocument();
    expect(screen.queryByText('No invites')).not.toBeInTheDocument();
  });

  // The list read names no invite: its 404 means the server has no such
  // endpoint, as one of an earlier release.
  it('says when the server does not offer invites', async () => {
    server.node = 'node-b';
    server.invites = refused(404, 'not found', 'not_found');

    renderInvites();

    expect(
      await screen.findByText(
        'The selected server does not offer worker invites.'
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/already gone/)).not.toBeInTheDocument();
  });

  // A proxy can answer with a bare status and no body; that is a failure,
  // not an empty list or a done delete.
  it('treats a bare 502 read as a failure', async () => {
    server.invites = { status: 502 };

    renderInvites();

    expect(await screen.findByText('The request failed.')).toBeInTheDocument();
    expect(screen.queryByText('Loading invites...')).not.toBeInTheDocument();
  });

  it('keeps the dialog open on a bare 502 delete', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    server.del = () => ({ status: 502 });
    renderInvites();
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const alert = await within(screen.getByRole('dialog')).findByRole('alert');
    expect(alert).toHaveTextContent('The request failed.');
  });

  // Every worker in the first workers list joined before the first read of
  // the invites, so a late first list cannot hide a join.
  it('reads the invites only after the first workers list', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { update } = renderInvites(null);
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS));
    expect(reads()).toBe(0);

    update([worker('worker-a')]);

    await waitFor(() => expect(reads()).toBe(1));
  });

  // The server deletes on its own coordinators, so a delete that ends after
  // a switch to another node and back reads the local invites again.
  it('reads the local invites after a delete that ends on return', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    let answer: (value: { status: number }) => void = () => undefined;
    server.del = () => new Promise((resolve) => (answer = resolve));
    const { rerender } = renderInvites();
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    server.node = 'node-b';
    rerender();
    server.node = 'local';
    rerender();
    const localReads = () =>
      requests('GET', '/workers/invites').filter(
        (request) => request.query.get('remoteNode') === 'local'
      ).length;
    await waitFor(() => expect(localReads()).toBe(2));

    server.invites = invitesAnswer([]);
    await act(async () => answer({ status: 204 }));

    await waitFor(() => expect(localReads()).toBe(3));
    expect(await screen.findByText('No invites')).toBeInTheDocument();
  });

  // A failed read is not retried on its own: each retry would be a
  // coordinator call from every open tab.
  it('does not retry a failed read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = refused(503, '', 'internal_error', 'no_coordinator');

    renderInvites();
    await screen.findByText('No coordinator is running or reachable.');
    await act(() => vi.advanceTimersByTimeAsync(5 * 60 * 1000));

    expect(reads()).toBe(1);
  });

  it('reads nothing while the tab is hidden', async () => {
    setTabVisible(false);
    renderInvites();
    await act(() => Promise.resolve());
    expect(reads()).toBe(0);

    act(() => setTabVisible(true));

    await waitFor(() => expect(reads()).toBe(1));
  });

  // A join uses an invite, so the list reads again once after workers join.
  // Joins close together cost one read, and a worker that leaves and comes
  // back is no join.
  it('reads once after workers join, and not when one comes back', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { update } = renderInvites(null);
    update([worker('worker-a')]);
    await screen.findByText('No invites');
    expect(reads()).toBe(1);

    update([worker('worker-a'), worker('worker-b')]);
    update([worker('worker-a'), worker('worker-b'), worker('worker-c')]);
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS + 100));
    expect(reads()).toBe(2);

    update([worker('worker-a')]);
    update([worker('worker-a'), worker('worker-b')]);
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS + 100));
    expect(reads()).toBe(2);
  });

  it('reads again when the page refresh resumes', async () => {
    const { update } = renderInvites([worker('worker-a')], false);
    await screen.findByText('No invites');
    expect(reads()).toBe(1);

    update([worker('worker-a')], true);

    await waitFor(() => expect(reads()).toBe(2));
  });
  // A join is a new certificate or invite too: a worker that enrolled again
  // under its old ID used an invite.
  it('reads after a worker joins again under its ID', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { update } = renderInvites([
      worker('worker-a', { certificateId: 'cert-1', inviteId: 'inv-1' }),
    ]);
    await screen.findByText('No invites');
    expect(reads()).toBe(1);

    update([
      worker('worker-a', { certificateId: 'cert-2', inviteId: 'inv-2' }),
    ]);
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS + 100));

    expect(reads()).toBe(2);
  });

  // The same, with the workers as the API reports them: a worker that
  // enrolled again keeps its ID and reports a new certificate and invite.
  it('reads after the workers API reports a worker enrolled again', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let reported = worker('worker-a', {
      connection: WorkerConnection.certificate,
      certificateId: 'cert-1',
      inviteId: 'inv-1',
    });
    server.workers = () => ({
      status: 200,
      body: { workers: [reported], errors: [] },
    });
    render(<WorkersPage />, { wrapper: Providers });
    await screen.findByText('No invites');
    expect(reads()).toBe(1);

    reported = { ...reported, certificateId: 'cert-2', inviteId: 'inv-2' };
    fireEvent.click(screen.getByRole('button', { name: 'Read workers' }));
    await waitFor(() => expect(requests('GET', '/workers')).toHaveLength(2));
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS + 100));

    expect(reads()).toBe(2);
  });

  // Another node lists other workers; they are its baseline, not joins, and
  // its invites are read once.
  it('reads once for the node it switches to', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { update } = renderInvites([worker('worker-a')]);
    await screen.findByText('No invites');

    // System status shows the new node with its workers not read yet.
    server.node = 'node-b';
    update(undefined);
    update([worker('worker-x'), worker('worker-y')]);
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS + 100));

    const nodes = requests('GET', '/workers/invites').map((request) =>
      request.query.get('remoteNode')
    );
    expect(nodes).toEqual(['local', 'node-b']);
  });

  it('waits for the tab to show before reading for a node switch', async () => {
    const { rerender } = renderInvites();
    await screen.findByText('No invites');

    act(() => setTabVisible(false));
    server.node = 'node-b';
    rerender();
    await act(() => Promise.resolve());
    expect(reads()).toBe(1);

    act(() => setTabVisible(true));

    await waitFor(() => expect(reads()).toBe(2));
  });

  // A delete that ends while the tab is hidden asks for the read the tab
  // makes once it shows.
  it('reads after a delete only once the tab shows', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    let answer: (value: { status: number }) => void = () => undefined;
    server.del = () => new Promise((resolve) => (answer = resolve));
    renderInvites();
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    act(() => setTabVisible(false));
    await act(async () => answer({ status: 204 }));
    await act(() => Promise.resolve());
    expect(reads()).toBe(1);

    act(() => setTabVisible(true));

    await waitFor(() => expect(reads()).toBe(2));
  });

  // A read that started before a delete cannot answer for after it: the
  // delete reads afresh, and the older answer is dropped.
  it('drops a read that started before a delete', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const { refresh } = renderInvites();
    await screen.findByRole('button', { name: /^Delete invite inv-1/ });
    let stale: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (stale = resolve)
        ),
      () => invitesAnswer([]),
    ];
    server.invites = () => answers.shift()!() as never;
    refresh();
    await waitFor(() => expect(reads()).toBe(2));

    await userEvent.click(
      screen.getByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(reads()).toBe(3));
    await act(async () =>
      stale({ status: 200, body: { invites: [invite('inv-1')] } })
    );

    expect(await screen.findByText('No invites')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /^Delete invite inv-1/ })
    ).not.toBeInTheDocument();
  });

  // A read under way when a worker joins may predate the join: the read
  // for the join starts afresh, and the older answer is dropped.
  it('drops a read that started before a worker joined', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([invite('inv-1')]);
    const { update, refresh } = renderInvites([worker('worker-a')]);
    const [row] = await rows();
    expect(row).toHaveTextContent('Active');
    let stale: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (stale = resolve)
        ),
      () =>
        invitesAnswer([
          invite('inv-1', { useCount: 1, lastUsedAt: hoursFromNow(0) }),
        ]),
    ];
    server.invites = () => answers.shift()!() as never;
    refresh();
    await waitFor(() => expect(reads()).toBe(2));

    update([worker('worker-a'), worker('worker-b', { inviteId: 'inv-1' })]);
    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS + 100));
    await waitFor(() => expect(reads()).toBe(3));
    await act(async () =>
      stale({ status: 200, body: { invites: [invite('inv-1')] } })
    );

    await waitFor(() => expect(rows()).resolves.toHaveLength(1));
    expect((await rows())[0]).toHaveTextContent('Used');
  });

  // The answer of a read under way when a worker joins is dropped at once,
  // also when it comes before the read for the join starts.
  it('drops a read that answers between a join and its read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([invite('inv-1')]);
    const { update, refresh } = renderInvites([worker('worker-a')]);
    expect(await rows()).toHaveLength(1);
    let stale: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (stale = resolve)
        ),
      () =>
        invitesAnswer([
          invite('inv-1', { useCount: 1, lastUsedAt: hoursFromNow(0) }),
        ]),
    ];
    server.invites = () => answers.shift()!() as never;
    refresh();
    await waitFor(() => expect(reads()).toBe(2));

    update([worker('worker-a'), worker('worker-b', { inviteId: 'inv-1' })]);
    await act(async () =>
      stale({
        status: 200,
        body: { invites: [invite('inv-1'), invite('inv-stale')] },
      })
    );
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(reads()).toBe(2);
    expect(await rows()).toHaveLength(1);
    expect(screen.queryByText(/inv-stal/)).not.toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS));
    await waitFor(() => expect(reads()).toBe(3));
    await waitFor(async () =>
      expect((await rows())[0]).toHaveTextContent('Used')
    );
  });

  // The same for a failed answer: it cannot replace the list with an error.
  it('drops a failure that answers between a join and its read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([invite('inv-1')]);
    const { update, refresh } = renderInvites([worker('worker-a')]);
    expect(await rows()).toHaveLength(1);
    let stale: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (stale = resolve)
        ),
      () =>
        invitesAnswer([
          invite('inv-1', { useCount: 1, lastUsedAt: hoursFromNow(0) }),
        ]),
    ];
    server.invites = () => answers.shift()!() as never;
    refresh();
    await waitFor(() => expect(reads()).toBe(2));

    update([worker('worker-a'), worker('worker-b', { inviteId: 'inv-1' })]);
    await act(async () =>
      stale(refused(503, '', 'internal_error', 'no_coordinator'))
    );
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(reads()).toBe(2);
    expect(await rows()).toHaveLength(1);
    expect(
      screen.queryByText('No coordinator is running or reachable.')
    ).not.toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS));
    await waitFor(() => expect(reads()).toBe(3));
    await waitFor(async () =>
      expect((await rows())[0]).toHaveTextContent('Used')
    );
  });

  // The first read can fail after a join made it stale. With nothing shown
  // yet, the list keeps loading until the read for the join answers.
  it('keeps loading when the first read fails after a join', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let first: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (first = resolve)
        ),
      () => invitesAnswer([invite('inv-1')]),
    ];
    server.invites = () => answers.shift()!() as never;
    const { update } = renderInvites([worker('worker-a')]);
    await waitFor(() => expect(reads()).toBe(1));

    update([worker('worker-a'), worker('worker-b', { inviteId: 'inv-1' })]);
    await act(async () =>
      first(refused(503, '', 'internal_error', 'no_coordinator'))
    );
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(screen.getByText('Loading invites...')).toBeInTheDocument();
    expect(
      screen.queryByText('No coordinator is running or reachable.')
    ).not.toBeInTheDocument();

    await act(() => vi.advanceTimersByTimeAsync(JOIN_REREAD_DELAY_MS));
    expect(await rows()).toHaveLength(1);
    expect(reads()).toBe(2);
  });

  // A read that started before a delete cannot answer after it, also while
  // the tab is hidden and the read for the delete waits for it to show.
  it('drops a read from before a delete in a hidden tab', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const { refresh } = renderInvites();
    expect(await rows()).toHaveLength(1);
    let stale: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (stale = resolve)
        ),
      () => invitesAnswer([]),
    ];
    server.invites = () => answers.shift()!() as never;
    refresh();
    await waitFor(() => expect(reads()).toBe(2));
    const pending = heldDeletes();
    await startDelete('inv-1');

    act(() => setTabVisible(false));
    await act(async () => pending[0]!({ status: 204 }));
    await act(async () =>
      stale({
        status: 200,
        body: { invites: [invite('inv-1'), invite('inv-old')] },
      })
    );
    await act(() => Promise.resolve());
    expect(reads()).toBe(2);
    expect(screen.queryByText(/inv-old/)).not.toBeInTheDocument();

    act(() => setTabVisible(true));
    expect(await screen.findByText('No invites')).toBeInTheDocument();
    expect(screen.queryByText(/inv-old/)).not.toBeInTheDocument();
  });

  // A read under way when the list left a node cannot answer for the
  // workers it shows on return: the first read on return starts afresh.
  it('reads afresh on return to a node', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const { update, refresh } = renderInvites([worker('worker-a')]);
    await rows();
    let stale: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (stale = resolve)
        ),
      () => invitesAnswer([]),
      () =>
        invitesAnswer([
          invite('inv-1', { useCount: 1, lastUsedAt: hoursFromNow(0) }),
        ]),
    ];
    server.invites = () => answers.shift()!() as never;
    // A manual refresh starts a read that will not answer for a while.
    refresh();
    await waitFor(() => expect(reads()).toBe(2));
    server.node = 'node-b';
    update(undefined);
    update([]);
    server.node = 'local';
    update(undefined);
    update([worker('worker-a'), worker('worker-b', { inviteId: 'inv-1' })]);

    await waitFor(() =>
      expect(
        screen.getByRole('listitem', { name: 'Invite inv-1' })
      ).toHaveTextContent('Used')
    );
    await act(async () =>
      stale({ status: 200, body: { invites: [invite('inv-1')] } })
    );
    expect(
      screen.getByRole('listitem', { name: 'Invite inv-1' })
    ).toHaveTextContent('Used');
  });

  // A remote node is called with the credentials this server keeps for
  // it: signing in again here would not help.
  it('names the node credentials on a remote 403', async () => {
    server.node = 'node-b';
    server.invites = refused(403, 'forbidden', 'forbidden');

    renderInvites();

    expect(
      await screen.findByText(
        'The credentials this server uses for the selected node lack admin access there.'
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/Sign in/)).not.toBeInTheDocument();
  });

  // On a remote node the selected node calls its coordinator: the remedies
  // name that node, not this server.
  it.each([
    [
      'peer_token_missing',
      'The selected node has no peer token for its coordinator.',
      'Set peer.token on the selected node to the peer token of its coordinator.',
    ],
    [
      'peer_token_mismatch',
      'The selected node and its coordinator have different peer tokens.',
      'Give the selected node the same peer.token as its coordinator.',
    ],
    [
      'require_tls',
      'The coordinator requires TLS, and the selected node called it over plaintext.',
      'Clear peer.insecure: true or peer.skip_tls_verify: true in the config of the selected node if set, and set peer.ca_cert_file there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.',
    ],
    [
      'no_coordinator',
      'No coordinator is running or reachable.',
      'Start the coordinator, or check that the selected node can reach it, then try again.',
    ],
  ])(
    'names the selected node in the remedy for %s',
    async (reason, cause, change) => {
      server.node = 'node-b';
      server.invites = refused(503, '', 'internal_error', reason);

      renderInvites();

      expect(await screen.findByText(cause)).toBeInTheDocument();
      expect(screen.getByText(change)).toBeInTheDocument();
      expect(document.body).not.toHaveTextContent(/this server/i);
    }
  );

  // A read that a delete superseded cannot show its failure over the newer
  // list: only the last read publishes a list or an error.
  it('drops a late failure of a read a delete superseded', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    const { refresh } = renderInvites();
    await screen.findByRole('button', { name: /^Delete invite inv-1/ });
    let fail: (answer: { status: number; body: unknown }) => void = () =>
      undefined;
    const answers = [
      () =>
        new Promise<{ status: number; body: unknown }>(
          (resolve) => (fail = resolve)
        ),
      () => invitesAnswer([]),
    ];
    server.invites = () => answers.shift()!() as never;
    refresh();
    await waitFor(() => expect(reads()).toBe(2));

    await userEvent.click(
      screen.getByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('No invites')).toBeInTheDocument();
    await act(async () =>
      fail({
        status: 503,
        body: {
          code: 'internal_error',
          message: 'no coordinator',
          details: { reason: 'no_coordinator' },
        },
      })
    );

    expect(screen.getByText('No invites')).toBeInTheDocument();
    expect(
      screen.queryByText('No coordinator is running or reachable.')
    ).not.toBeInTheDocument();
  });

  // Only a 409 and a 503 carry a worker access reason: a 403 with details
  // gets the advice for its status and quotes nothing.
  it('reads no reason from a 403', async () => {
    server.node = 'node-b';
    server.invites = {
      status: 403,
      body: {
        code: 'forbidden',
        message: 'role developer cannot do this',
        details: { reason: 'some_reason' },
      },
    };

    renderInvites();

    expect(
      await screen.findByText(
        'The credentials this server uses for the selected node lack admin access there.'
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/Server message/)).not.toBeInTheDocument();
    expect(screen.queryByText(/role developer/)).not.toBeInTheDocument();
  });

  // Reads asked for while the tab is hidden, by two deletes and a manual
  // refresh, run as one read once it shows.
  it('reads once for all that asked while the tab was hidden', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([invite('inv-1'), invite('inv-2')]);
    const answers: ((value: { status: number }) => void)[] = [];
    server.del = () => new Promise((resolve) => answers.push(resolve));
    const { refresh } = renderInvites();
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await userEvent.click(
      screen.getByRole('button', { name: /^Delete invite inv-2/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(reads()).toBe(1);

    act(() => setTabVisible(false));
    await act(async () => answers.forEach((answer) => answer({ status: 204 })));
    refresh();
    await act(() => Promise.resolve());
    expect(reads()).toBe(1);

    act(() => setTabVisible(true));

    await waitFor(() => expect(reads()).toBe(2));
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(reads()).toBe(2);
  });

  // Each confirmation owns its delete: one that ends after its dialog was
  // closed leaves a newer confirmation open.
  it('keeps a newer confirmation open when an older delete ends', async () => {
    server.invites = invitesAnswer([invite('inv-1'), invite('inv-2')]);
    let answer: (value: { status: number }) => void = () => undefined;
    server.del = () => new Promise((resolve) => (answer = resolve));
    renderInvites();
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await userEvent.click(
      screen.getByRole('button', { name: /^Delete invite inv-2/ })
    );

    await act(async () => answer({ status: 204 }));

    expect(screen.getByRole('dialog')).toHaveTextContent('Invite inv-2');
  });

  // Refresh on System status reads the invites again, so a failed read can
  // be tried again.
  it('reads again on a manual refresh', async () => {
    server.invites = refused(503, '', 'internal_error', 'no_coordinator');
    const { refresh } = renderInvites();
    await screen.findByText('No coordinator is running or reachable.');

    server.invites = invitesAnswer([invite('inv-1')]);
    refresh();

    expect(await rows()).toHaveLength(1);
    expect(reads()).toBe(2);
  });

  // An invite that is already gone has nothing left to delete.
  it('says an invite is already gone and offers no second delete', async () => {
    server.invites = invitesAnswer([invite('inv-1')]);
    server.del = () => refused(404, 'no such invite', 'not_found');
    renderInvites();
    await userEvent.click(
      await screen.findByRole('button', { name: /^Delete invite inv-1/ })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const alert = await within(screen.getByRole('dialog')).findByRole('alert');
    expect(alert).toHaveTextContent('This invite is already gone.');
    expect(alert).toHaveTextContent(
      'The list is read again. Close this dialog.'
    );
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
  });
});

// System status passes the invites list its workers once their read settled,
// whatever the answer, so the invites load also when /workers failed.
describe('inviteBaseline', () => {
  it('waits while the workers read is under way', () => {
    expect(inviteBaseline({ settled: false })).toBeUndefined();
  });

  it('takes an empty list once a read settled without workers', () => {
    expect(inviteBaseline({ settled: true })).toEqual([]);
  });

  it('takes the workers once read', () => {
    const workers = [worker('worker-a')];
    expect(inviteBaseline({ workers, settled: true })).toBe(workers);
  });
});
