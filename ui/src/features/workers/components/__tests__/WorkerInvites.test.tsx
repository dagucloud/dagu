// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkerInviteKind, type components } from '@/api/v1/schema';
import { useIsAdmin } from '@/contexts/AuthContext';
import WorkerInvites, { JOIN_REREAD_DELAY_MS } from '../WorkerInvites';
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

function renderInvites(
  workers: Worker[] | undefined = [worker('worker-a')],
  live = true
) {
  const view = render(<WorkerInvites workers={workers} live={live} />, {
    wrapper: Providers,
  });
  return {
    ...view,
    update: (next: Worker[] | undefined, nextLive = live) =>
      view.rerender(<WorkerInvites workers={next} live={nextLive} />),
  };
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
    renderInvites(undefined, false);
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
        'Delete works on the local server only. Switch to it to delete an invite.'
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
      'This invite no longer exists.',
      'It was deleted, or it ended more than 7 days ago.',
    ],
    [
      409,
      refused(
        409,
        'invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file'
      ),
      'invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file',
      'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.',
    ],
    [
      503,
      refused(503, 'no coordinator is running or reachable', 'internal_error'),
      'no coordinator is running or reachable',
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

  it.each([
    [
      refused(403, 'insufficient role', 'forbidden'),
      'Only admins can manage workers and invites.',
    ],
    [
      refused(
        409,
        'the coordinator runs an earlier Dagu release without worker invites: upgrade it'
      ),
      'Nothing changed. Make the change the message names.',
    ],
    [
      refused(503, 'no coordinator is running or reachable', 'internal_error'),
      'Start the coordinator, or check that this server can reach it',
    ],
  ])('says why the invites cannot be listed', async (answer, shown) => {
    server.invites = answer;

    renderInvites();

    expect(await screen.findByText(new RegExp(shown))).toBeInTheDocument();
  });

  // The coordinator's reason for refusing the server already names the
  // change, such as setting peer.token.
  it("shows the coordinator's reason for refusing the server", async () => {
    const reason =
      "the coordinator refused the server: set peer.token, or run the server where it reads the coordinator's data directory";
    server.invites = refused(503, reason, 'internal_error');

    renderInvites();

    expect(await screen.findByText(reason)).toBeInTheDocument();
    expect(
      screen.getByText('Nothing changed. Make the change the message names.')
    ).toBeInTheDocument();
    expect(screen.queryByText(/Start the coordinator/)).not.toBeInTheDocument();
  });

  // A failed read is not retried on its own: each retry would be a
  // coordinator call from every open tab.
  it('does not retry a failed read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = refused(
      503,
      'no coordinator is running or reachable',
      'internal_error'
    );

    renderInvites();
    await screen.findByText(/no coordinator is running/);
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
    const { update } = renderInvites(undefined);
    await screen.findByText('No invites');
    update([worker('worker-a')]);
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
});
