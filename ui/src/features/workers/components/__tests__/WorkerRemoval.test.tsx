// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  HostEnvironment,
  WorkerConnection,
  WorkerInviteKind,
  type components,
} from '@/api/v1/schema';
import { QueryFeedback } from '@/components/QueryFeedback';
import { useIsAdmin } from '@/contexts/AuthContext';
import WorkersSummary from '@/features/dashboard/components/WorkersSummary';
import { useQuery } from '@/hooks/api';
import { useWorkersList } from '../../hooks/useWorkersList';
import WorkerInvites from '../WorkerInvites';
import { WorkerActionsHost } from '../WorkerRemoval';
import {
  Providers,
  installServer,
  invite,
  invitesAnswer,
  refused,
  requests,
  server,
  setTabVisible,
  task,
  worker,
} from './workerAdminFixtures';

type Worker = components['schemas']['Worker'];

vi.mock('@/hooks/api', async () =>
  (await import('./workerAdminFixtures')).realApiModule()
);
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: vi.fn() }));

// System status renders the workers list, inside the host of its
// confirmations, and the invites list side by side; both read through one
// cache. The page also reads /workers, which a removal reads again, and
// shows the page's notices of failed background reads. `errors` is a failed
// refresh of the workers list; `refreshes` counts the manual refreshes.
function Page({
  workers,
  errors,
  refreshes = 0,
}: {
  workers: Worker[];
  errors?: string[];
  refreshes?: number;
}) {
  useQuery('/workers', { params: { query: { remoteNode: 'local' } } });
  return (
    <QueryFeedback>
      <WorkerActionsHost workers={workers}>
        <WorkersSummary workers={workers} isLoading={false} errors={errors} />
      </WorkerActionsHost>
      <WorkerInvites workers={workers} live refreshes={refreshes} />
    </QueryFeedback>
  );
}

// System status as it reads its workers itself, with its refresh paused.
function LivePage() {
  const list = useWorkersList('local', false);
  return (
    <WorkerActionsHost workers={list.workers || []}>
      <WorkersSummary
        workers={list.workers || []}
        isLoading={list.isLoading}
        errors={list.errors}
      />
    </WorkerActionsHost>
  );
}

function renderPage(workers: Worker[]) {
  let state: React.ComponentProps<typeof Page> = { workers };
  const view = render(<Page {...state} />, { wrapper: Providers });
  return {
    ...view,
    update: (next: Worker[], errors?: string[]) => {
      state = { ...state, workers: next, errors };
      view.rerender(<Page {...state} />);
    },
    refresh: () => {
      state = { ...state, refreshes: (state.refreshes ?? 0) + 1 };
      view.rerender(<Page {...state} />);
    },
  };
}

// The list renders the Remove action twice, as an icon in its own column
// and as a text button under the worker ID, and its width shows one.
function removeButtons(workerId: string) {
  return screen.queryAllByRole('button', { name: `Remove ${workerId}` });
}

async function openRemove(workerId: string) {
  await screen.findByRole('heading', { name: 'Invites' });
  const buttons = removeButtons(workerId);
  expect(buttons).toHaveLength(2);
  await userEvent.click(buttons[0]!);
  return screen.getByRole('dialog');
}

function removals() {
  return server.requests.filter(
    (request) =>
      request.method === 'DELETE' && request.path.endsWith('/certificate')
  );
}

describe('Remove', () => {
  beforeEach(() => {
    installServer();
    server.del = () => ({ status: 200, body: { state: 'removing' } });
    setTabVisible(true);
    vi.mocked(useIsAdmin).mockReturnValue(true);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('is not offered to non-admins', () => {
    vi.mocked(useIsAdmin).mockReturnValue(false);

    renderPage([worker('worker-a1')]);

    expect(removeButtons('worker-a1')).toHaveLength(0);
  });

  // Remove revokes the certificate the coordinator reported. Without its ID
  // there is nothing the removal can name.
  // An older coordinator reports neither the certificate ID nor the
  // removal: nothing is offered or claimed from what it leaves out.
  it('is not offered without a certificate ID', () => {
    renderPage([
      worker('worker-a1', { certificateId: undefined, removing: undefined }),
    ]);

    expect(removeButtons('worker-a1')).toHaveLength(0);
    expect(screen.queryByText(/^Removing/)).not.toBeInTheDocument();
  });

  // The server removes workers on its own coordinators only.
  it('offers no Remove or Stop runs now on a remote node', async () => {
    server.node = 'node-b';
    renderPage([
      worker('worker-a1'),
      worker('worker-b1', {
        removing: true,
        runningTasks: [task('etl', 'run-1')],
      }),
    ]);
    await screen.findByText('No invites');

    expect(removeButtons('worker-a1')).toHaveLength(0);
    expect(screen.getByText('Removing (1 run left)')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /^Stop runs now/ })
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(
        'Remove and Stop runs now work only for the server this page belongs to. To use them on the selected node, open the Dagu UI of that node.'
      )
    ).toBeInTheDocument();
  });

  // A narrow list shows Remove as text under the ID, so the ID keeps the
  // row's width; a wide one shows it as an icon in a column of its own.
  it('puts Remove under the ID of a narrow list', () => {
    renderPage([worker('worker-a1')]);

    const [text, icon] = removeButtons('worker-a1');
    expect(icon?.parentElement).toHaveClass('hidden', '@5xl:block');
    expect(text).toHaveTextContent('Remove');
    expect(text?.parentElement).toHaveClass('@5xl:hidden');
  });

  it('removes a worker without runs after one confirmation', async () => {
    renderPage([worker('worker-a1')]);

    const dialog = await openRemove('worker-a1');
    expect(dialog).toHaveTextContent('It has no running runs.');
    expect(dialog).toHaveTextContent(
      'it stays out across restarts until it gets a new invite'
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    expect(removals()).toHaveLength(1);
    expect(removals()[0]?.path).toBe('/api/v1/workers/worker-a1/certificate');
    expect(removals()[0]?.query.get('certificateId')).toBe('cert-worker-a1');
    expect(removals()[0]?.query.get('runs')).toBe('finish');
    expect(removals()[0]?.query.has('remoteNode')).toBe(false);
  });

  // A certificate revoked before, as by a removal whose answer was lost,
  // answers removed.
  it('closes when the certificate was already removed', async () => {
    server.del = () => ({ status: 200, body: { state: 'removed' } });
    renderPage([worker('worker-a1')]);

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
  });

  // A certificate already being removed, as by another admin, answers with
  // that removal's state. The dialog takes it as done, deletes the chosen
  // invite, and the list shows the removal.
  it('takes a removal already under way as accepted', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? { status: 200, body: { state: 'removing' } }
        : { status: 204 };
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');
    await waitFor(() => expect(requests('GET', '/workers')).toHaveLength(1));

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(1);
    await waitFor(() => expect(requests('GET', '/workers')).toHaveLength(2));
  });

  // A stop of a certificate the coordinator revoked meanwhile answers
  // removed: the stop is done.
  it('closes Stop runs now when the certificate was already revoked', async () => {
    server.del = () => ({ status: 200, body: { state: 'removed' } });
    renderPage([
      worker('worker-b1', {
        removing: true,
        runningTasks: [task('etl', 'run-1')],
      }),
    ]);
    await userEvent.click(
      screen.getByRole('button', { name: 'Stop runs now on worker-b1' })
    );
    await userEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Stop runs',
      })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
  });

  // The removal names the certificate the admin confirmed. A worker that
  // joined again holds another one: it is not removed, the dialog says why,
  // the list is read again, and the explanation stays when the refreshed
  // row shows the new certificate.
  it('says when the worker no longer holds the confirmed certificate', async () => {
    server.del = () =>
      refused(409, 'certificate changed', 'conflict', 'certificate_changed');
    const { update } = renderPage([worker('worker-a1')]);
    await waitFor(() => expect(requests('GET', '/workers')).toHaveLength(1));

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    const alert = await within(dialog).findByRole('alert');
    await waitFor(() => expect(requests('GET', '/workers')).toHaveLength(2));
    update([worker('worker-a1', { certificateId: 'cert-new' })]);

    expect(alert).toBeInTheDocument();
    expect(alert).toHaveTextContent(
      'This worker no longer holds the certificate the list showed, as after it joined again.'
    );
    expect(alert).toHaveTextContent('The worker was not removed.');
    expect(screen.getByRole('dialog')).toHaveTextContent(
      'This worker no longer connects with the certificate shown when this dialog opened.'
    );
  });

  // The invite is deleted only once the server accepted the removal: a
  // refused removal leaves the invite as it is.
  it('keeps the invite when the removal is refused', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? refused(409, 'certificate changed', 'conflict', 'certificate_changed')
        : { status: 204 };
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'The worker was not removed.'
    );
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(0);
  });

  // Only a removal state the API defines counts as accepted. An answer
  // without one deletes no invite.
  it('deletes no invite after a removal answer without a state', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? { status: 200, body: {} }
        : { status: 204 };
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'The request failed.'
    );
    expect(within(dialog).queryByRole('status')).not.toBeInTheDocument();
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(0);
  });

  // Each step reports its own outcome: an accepted removal and an invite
  // that could not be deleted, which a second click deletes on its own.
  // That second click leaves the workers list as it is.
  it('reports an invite delete that fails after the removal', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    let inviteAnswer = refused(503, '', 'internal_error', 'no_coordinator');
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? { status: 200, body: { state: 'removing' } }
        : inviteAnswer;
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    expect(await within(dialog).findByRole('status')).toHaveTextContent(
      'The server accepted the removal.'
    );
    const alert = within(dialog).getByRole('alert');
    expect(alert).toHaveTextContent('The invite was not deleted.');
    expect(alert).toHaveTextContent('No coordinator is running or reachable.');
    expect(alert).not.toHaveTextContent('not removed');

    inviteAnswer = { status: 204 } as typeof inviteAnswer;
    const workerReads = requests('GET', '/workers').length;
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Delete the invite' })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    expect(removals()).toHaveLength(1);
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(2);
    expect(requests('GET', '/workers')).toHaveLength(workerReads);
  });

  // A retry deletes the invite the admin confirmed, not the one the row
  // shows by then, and closes only once that delete went through.
  it('retries the delete of the confirmed invite only', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
      invite('inv-new', { kind: WorkerInviteKind.reusable }),
    ]);
    const inviteAnswers = [
      refused(503, '', 'internal_error', 'no_coordinator'),
      refused(503, '', 'internal_error', 'no_coordinator'),
      { status: 204 },
    ];
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? { status: 200, body: { state: 'removing' } }
        : (inviteAnswers.shift() as never);
    const { update } = renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findAllByText('Reusable');
    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    await within(dialog).findByText('The invite was not deleted.');

    update([
      worker('worker-a1', {
        certificateId: 'cert-new',
        inviteId: 'inv-new',
      }),
    ]);
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Delete the invite' })
    );
    await waitFor(() =>
      expect(
        server.requests.filter((r) => r.path.includes('/workers/invites/'))
      ).toHaveLength(2)
    );
    expect(screen.getByRole('dialog')).toBe(dialog);

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Delete the invite' })
    );

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    expect(
      server.requests
        .filter((r) => r.method === 'DELETE' && r.path.includes('/invites/'))
        .map((r) => r.path)
    ).toEqual([
      '/api/v1/workers/invites/inv-r',
      '/api/v1/workers/invites/inv-r',
      '/api/v1/workers/invites/inv-r',
    ]);
    expect(removals()).toHaveLength(1);
  });

  // Once the removal is accepted, the dialog shows and deletes the invite
  // the admin confirmed, from what it recorded then. A replacement row on
  // another invite changes neither, and its lookup does not hold the retry.
  it('keeps the retry on the confirmed invite after a replacement', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    const inviteAnswers = [
      refused(503, '', 'internal_error', 'no_coordinator'),
      { status: 204 },
    ];
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? { status: 200, body: { state: 'removing' } }
        : (inviteAnswers.shift() as never);
    const { update } = renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');
    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    await within(dialog).findByText('The invite was not deleted.');
    // A read of the invites would never answer: the dialog must not wait on
    // one for the replacement's invite.
    server.invites = () => new Promise(() => undefined);
    const reads = requests('GET', '/workers/invites').length;

    update([
      worker('worker-a1', {
        certificateId: 'cert-new',
        inviteId: 'inv-new',
        environment: HostEnvironment.kubernetes,
      }),
    ]);

    expect(dialog).toHaveTextContent('Invite to delete: inv-r (Reusable)');
    expect(dialog).not.toHaveTextContent('inv-new');
    expect(dialog).not.toHaveTextContent('Kubernetes');
    expect(requests('GET', '/workers/invites')).toHaveLength(reads);
    const retry = within(dialog).getByRole('button', {
      name: 'Delete the invite',
    });
    expect(retry).toBeEnabled();
    await userEvent.click(retry);

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    );
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(2);
    expect(requests('DELETE', '/workers/invites/inv-new')).toHaveLength(0);
  });

  // A refused removal says how to try again and takes no second request
  // from the stale dialog, also when the refresh no longer lists the row.
  it.each(['certificate_changed', 'peer_token_worker'])(
    'locks the confirmation after a %s refusal',
    async (reason) => {
      server.invites = invitesAnswer([
        invite('inv-r', { kind: WorkerInviteKind.reusable }),
      ]);
      server.del = () => refused(409, 'refused', 'conflict', reason);
      const { update } = renderPage([
        worker('worker-a1', { inviteId: 'inv-r' }),
      ]);
      await screen.findByText('Reusable');
      const dialog = await openRemove('worker-a1');
      await userEvent.click(
        within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
      );
      await userEvent.click(
        within(dialog).getByRole('button', { name: 'Remove' })
      );
      await within(dialog).findByRole('alert');

      update([]);

      expect(dialog).toHaveTextContent(
        'To try again, close this dialog and confirm again from the list.'
      );
      const remove = within(dialog).getByRole('button', { name: 'Remove' });
      expect(remove).toBeDisabled();
      await userEvent.click(remove);
      expect(removals()).toHaveLength(1);
      expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(0);
    }
  );

  it('locks Stop runs now after a refusal', async () => {
    server.del = () => refused(503, '', 'internal_error', 'no_coordinator');
    renderPage([
      worker('worker-b1', {
        removing: true,
        runningTasks: [task('etl', 'run-1')],
      }),
    ]);
    await userEvent.click(
      screen.getByRole('button', { name: 'Stop runs now on worker-b1' })
    );
    const dialog = screen.getByRole('dialog');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Stop runs' })
    );
    await within(dialog).findByRole('alert');

    expect(
      within(dialog).getByRole('button', { name: 'Stop runs' })
    ).toBeDisabled();
  });

  // Enter confirms only on the confirm button: a choice that a refresh took
  // away cannot send the removal.
  it('sends nothing on Enter after a refresh takes the focused choice away', async () => {
    const { update } = renderPage([
      worker('worker-a1', { runningTasks: [task('etl', 'run-1')] }),
    ]);
    const dialog = await openRemove('worker-a1');
    within(dialog)
      .getByRole('radio', { name: /Stop its runs and remove/ })
      .focus();

    update([worker('worker-a1')]);
    await userEvent.keyboard('{Enter}');

    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(removals()).toHaveLength(0);
  });

  // The submitted choices hold while their request is under way.
  it('locks the choices while the removal is under way', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    let answer: (value: { status: number; body: unknown }) => void = () =>
      undefined;
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? new Promise((resolve) => (answer = resolve))
        : { status: 204 };
    renderPage([
      worker('worker-a1', {
        inviteId: 'inv-r',
        runningTasks: [task('etl', 'run-1')],
      }),
    ]);
    await screen.findByText('Reusable');
    const dialog = await openRemove('worker-a1');
    const checkbox = within(dialog).getByRole('checkbox', {
      name: /Also delete the invite/,
    });
    await userEvent.click(checkbox);
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    expect(checkbox).toBeDisabled();
    for (const radio of within(dialog).getAllByRole('radio')) {
      expect(radio).toBeDisabled();
    }
    await act(async () => answer({ status: 200, body: { state: 'removing' } }));
    await waitFor(() =>
      expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(1)
    );
  });

  // Once the server accepted the removal, the dialog reads no invites for
  // its own sake: the retry has its target.
  it('reads no invites after the removal is accepted', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? { status: 200, body: { state: 'removing' } }
        : refused(503, '', 'internal_error', 'no_coordinator');
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');
    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    // The refresh after the delete leaves the invite out.
    server.invites = invitesAnswer([]);
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    await within(dialog).findByText('The invite was not deleted.');
    await waitFor(() =>
      expect(requests('GET', '/workers/invites')).toHaveLength(2)
    );
    await act(
      () => new Promise((resolve) => queueMicrotask(() => resolve(undefined)))
    );

    expect(requests('GET', '/workers/invites')).toHaveLength(2);
  });

  // A refused removal reads the workers again, also while the refresh is
  // paused. A failed read shows the page's words in the list, with no page
  // notice of the server's.
  it('shows a failed workers refresh in the list, not a notice', async () => {
    const answers = [
      () => ({
        status: 200,
        body: { workers: [worker('worker-a1')], errors: [] },
      }),
      () => ({
        status: 503,
        body: { code: 'internal_error', message: 'raw server words' },
      }),
    ];
    server.workers = () => answers.shift()!();
    server.del = () => refused(503, '', 'internal_error', 'no_coordinator');
    render(
      <QueryFeedback>
        <LivePage />
      </QueryFeedback>,
      { wrapper: Providers }
    );
    const dialog = await (async () => {
      const [button] = await screen.findAllByRole('button', {
        name: 'Remove worker-a1',
      });
      await userEvent.click(button!);
      return screen.getByRole('dialog');
    })();

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    expect(
      await screen.findByText(
        'The workers list could not be read. Refresh the page, or check the server connection.'
      )
    ).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(screen.queryByText(/raw server words/)).not.toBeInTheDocument();
    expect(
      screen.queryByText('Background request failed')
    ).not.toBeInTheDocument();
  });

  // The API reports coordinator errors in a 200 answer. The list shows
  // the page's own words for them, not the coordinator's.
  it('shows coordinator errors of a workers refresh in its own words', async () => {
    const answers = [
      { workers: [worker('worker-a1')], errors: [] },
      { workers: [], errors: ['raw coordinator words'] },
    ];
    server.workers = () => ({ status: 200, body: answers.shift()! });
    server.del = () => refused(503, '', 'internal_error', 'no_coordinator');
    render(
      <QueryFeedback>
        <LivePage />
      </QueryFeedback>,
      { wrapper: Providers }
    );
    const [button] = await screen.findAllByRole('button', {
      name: 'Remove worker-a1',
    });
    await userEvent.click(button!);

    await userEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Remove',
      })
    );

    expect(
      await screen.findByText(
        'The coordinator could not report the workers. Check the coordinator log, then refresh the page.'
      )
    ).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent('raw coordinator words');
  });

  // The invite can expire while the confirmation is open with the refresh
  // paused: the warning and the delete option go away with it.
  it('drops the invite warning when the invite expires', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    server.invites = invitesAnswer([
      invite('inv-r', {
        kind: WorkerInviteKind.reusable,
        expiresAt: new Date(Date.now() + 20e3).toISOString(),
      }),
    ]);
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');
    const dialog = await openRemove('worker-a1');
    expect(dialog).toHaveTextContent('It joined with a reusable invite');

    await act(() => vi.advanceTimersByTimeAsync(30e3));

    expect(dialog).not.toHaveTextContent('It joined with a reusable invite');
    expect(within(dialog).queryByRole('checkbox')).not.toBeInTheDocument();
    vi.useRealTimers();
  });

  // The stop choice says what the server does: a cancel request to each
  // run, and the worker removed within two minutes either way.
  it('says the stop choice removes the worker within two minutes', async () => {
    renderPage([worker('worker-a1', { runningTasks: [task('etl', 'run-1')] })]);

    const dialog = await openRemove('worker-a1');

    expect(
      within(dialog).getByRole('radio', { name: /Stop its runs and remove/ })
    ).toHaveAccessibleName(
      /Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended\./
    );
  });

  // A confirmation stays locked to the certificate it opened for: once the
  // row showed another one, it stays locked when that row goes away too.
  it('stays locked after the replacement row goes away', async () => {
    const { update } = renderPage([worker('worker-a1')]);
    const dialog = await openRemove('worker-a1');

    update([worker('worker-a1', { certificateId: 'cert-new' })]);
    update([]);

    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeDisabled();
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    expect(removals()).toHaveLength(0);
  });

  // The admin sees which runs the removal affects, and the default lets them
  // finish.
  it('lists the running runs and lets them finish by default', async () => {
    renderPage([
      worker('worker-a1', {
        runningTasks: [task('etl', 'run-1'), task('report', 'run-2')],
      }),
    ]);

    const dialog = await openRemove('worker-a1');
    expect(dialog).toHaveTextContent('It has 2 running runs:');
    expect(within(dialog).getByText('etl')).toBeInTheDocument();
    expect(within(dialog).getByText('run-2')).toBeInTheDocument();
    expect(
      within(dialog).getByRole('radio', {
        name: /Remove after its runs finish/,
      })
    ).toBeChecked();
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    await waitFor(() => expect(removals()).toHaveLength(1));
    expect(removals()[0]?.query.get('runs')).toBe('finish');
  });

  it('stops the runs when the admin chooses so', async () => {
    renderPage([worker('worker-a1', { runningTasks: [task('etl', 'run-1')] })]);

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('radio', { name: /Stop its runs and remove/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Stop its runs and remove' })
    );

    await waitFor(() => expect(removals()).toHaveLength(1));
    expect(removals()[0]?.query.get('runs')).toBe('stop');
  });

  // The stop choice applies to the runs on screen. Once the refresh shows
  // none, the button reads Remove, and a run handed out meanwhile must not
  // be canceled by a stop the admin no longer sees.
  it('sends finish once the runs the admin chose to stop are gone', async () => {
    const { update } = renderPage([
      worker('worker-a1', { runningTasks: [task('etl', 'run-1')] }),
    ]);
    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('radio', { name: /Stop its runs and remove/ })
    );

    update([worker('worker-a1')]);
    await userEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Remove',
      })
    );

    await waitFor(() => expect(removals()).toHaveLength(1));
    expect(removals()[0]?.query.get('runs')).toBe('finish');
  });

  // The count rides on the page's refresh of /workers: a new list shows the
  // new count with no reload and no request of its own.
  it('shows the runs left while removing', async () => {
    const removing = (tasks: number) =>
      worker('worker-a1', {
        removing: true,
        runningTasks: Array.from({ length: tasks }, (_, i) =>
          task('etl', `run-${i}`)
        ),
      });
    const { update } = renderPage([removing(2)]);
    await screen.findByText('No invites');

    expect(screen.getByText('Removing (2 runs left)')).toBeInTheDocument();
    expect(removeButtons('worker-a1')).toHaveLength(0);

    update([removing(1)]);

    expect(screen.getByText('Removing (1 run left)')).toBeInTheDocument();
    expect(server.requests.filter((r) => r.method !== 'GET')).toHaveLength(0);
  });

  it('stops the runs of a removing worker with Stop runs now', async () => {
    renderPage([
      worker('worker-a1', {
        removing: true,
        runningTasks: [task('etl', 'run-1'), task('report', 'run-2')],
      }),
      worker('worker-b1', {
        removing: true,
        runningTasks: [task('etl', 'run-3')],
      }),
    ]);

    await userEvent.click(
      screen.getByRole('button', { name: 'Stop runs now on worker-a1' })
    );
    const dialog = screen.getByRole('dialog');
    // The worker is removed within two minutes whether or not its runs end.
    expect(dialog).toHaveTextContent(
      'Each of its 2 runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.'
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Stop runs' })
    );

    await waitFor(() => expect(removals()).toHaveLength(1));
    expect(removals()[0]?.path).toBe('/api/v1/workers/worker-a1/certificate');
    expect(removals()[0]?.query.get('runs')).toBe('stop');
  });

  // A reusable invite still lets new hosts join, so the confirmation says
  // so, names the workers that share it, and deletes it once the server
  // accepted the removal.
  it('warns about a reusable invite and deletes it after the removal', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable, useCount: 2 }),
    ]);
    renderPage([
      worker('worker-a1', { inviteId: 'inv-r' }),
      worker('worker-b1', { inviteId: 'inv-r' }),
      worker('worker-c1', { inviteId: 'inv-other' }),
    ]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');
    expect(dialog).toHaveTextContent(
      'It joined with a reusable invite, which still lets new workers join'
    );
    expect(dialog).toHaveTextContent('Other workers on this invite: worker-b1');
    expect(dialog).not.toHaveTextContent('worker-c1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    await waitFor(() => expect(removals()).toHaveLength(1));
    await waitFor(() =>
      expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(1)
    );
    expect(
      server.requests
        .filter((request) => request.method === 'DELETE')
        .map((request) => request.path)
    ).toEqual([
      '/api/v1/workers/worker-a1/certificate',
      '/api/v1/workers/invites/inv-r',
    ]);
  });

  // The invites list already read the invites; the confirmation uses them
  // from the same cache instead of reading again.
  it('reads the invites once for the list and the confirmation', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');

    expect(dialog).toHaveTextContent('reusable invite');
    expect(requests('GET', '/workers/invites')).toHaveLength(1);
  });

  it('keeps the invite unless the admin asks to delete it', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );

    await waitFor(() => expect(removals()).toHaveLength(1));
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(0);
  });

  // The dialog acts on the certificate the admin confirmed. Once the worker
  // joined again with another one, it asks for a fresh confirmation and
  // sends nothing.
  it('asks for a fresh confirmation when the worker joins again', async () => {
    const { update } = renderPage([worker('worker-a1')]);
    const dialog = await openRemove('worker-a1');

    update([worker('worker-a1', { certificateId: 'cert-new' })]);

    expect(dialog).toHaveTextContent(
      'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.'
    );
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeDisabled();
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    expect(removals()).toHaveLength(0);
  });

  it('stops no runs of a certificate the admin did not confirm', async () => {
    const removing = (certificateId: string) =>
      worker('worker-b1', {
        certificateId,
        removing: true,
        runningTasks: [task('etl', 'run-1')],
      });
    const { update } = renderPage([removing('cert-1')]);
    await userEvent.click(
      screen.getByRole('button', { name: 'Stop runs now on worker-b1' })
    );
    const dialog = screen.getByRole('dialog');

    update([removing('cert-2')]);

    expect(dialog).toHaveTextContent(
      'This worker no longer connects with the certificate shown when this dialog opened.'
    );
    expect(
      within(dialog).getByRole('button', { name: 'Stop runs' })
    ).toBeDisabled();
    expect(removals()).toHaveLength(0);
  });

  // The list's cached read can predate the worker's join, so an invite it
  // lacks is read once more before Remove is offered.
  it('reads an invite the cache lacks before offering Remove', async () => {
    server.invites = invitesAnswer([invite('inv-old')]);
    renderPage([worker('worker-a1', { inviteId: 'inv-new' })]);
    await screen.findByText('Single use');
    server.invites = invitesAnswer([
      invite('inv-old'),
      invite('inv-new', { kind: WorkerInviteKind.reusable }),
    ]);

    const dialog = await openRemove('worker-a1');

    await waitFor(() =>
      expect(dialog).toHaveTextContent('It joined with a reusable invite')
    );
    expect(requests('GET', '/workers/invites')).toHaveLength(2);
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeEnabled();
  });

  it('says when the invite it joined with no longer exists', async () => {
    server.invites = invitesAnswer([invite('inv-old')]);
    renderPage([worker('worker-a1', { inviteId: 'inv-gone' })]);
    await screen.findByText('Single use');

    const dialog = await openRemove('worker-a1');

    await waitFor(() =>
      expect(dialog).toHaveTextContent(
        'The invite it joined with no longer exists, so it lets no new worker join.'
      )
    );
  });

  // A single-use invite ended at its use and admits no one else.
  it('gives no invite warning for a single-use invite', async () => {
    server.invites = invitesAnswer([invite('inv-s', { useCount: 1 })]);
    renderPage([worker('worker-a1', { inviteId: 'inv-s' })]);
    await screen.findByText('Single use');

    const dialog = await openRemove('worker-a1');

    expect(dialog).not.toHaveTextContent('reusable invite');
    expect(within(dialog).queryByRole('checkbox')).not.toBeInTheDocument();
  });

  // Without the invite, the confirmation cannot say whether it still admits
  // new workers. It says so and lets the admin read it again.
  it('says when the invite cannot be read and reads it again on request', async () => {
    server.invites = refused(503, '', 'internal_error', 'no_coordinator');
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText(/Start the coordinator/);
    const before = requests('GET', '/workers/invites').length;

    const dialog = await openRemove('worker-a1');
    await waitFor(() =>
      expect(dialog).toHaveTextContent(
        'The invite it joined with could not be read'
      )
    );
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeEnabled();

    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Try again' })
    );

    await waitFor(() =>
      expect(dialog).toHaveTextContent('It joined with a reusable invite')
    );
    expect(requests('GET', '/workers/invites').length).toBeGreaterThan(before);
  });

  // The latest invite read decides. Once a refresh fails, the invite from
  // the read before it is neither warned about nor offered for deletion.
  it('lets a failed invite read win over an earlier one', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    const { refresh } = renderPage([
      worker('worker-a1', { inviteId: 'inv-r' }),
    ]);
    await screen.findByText('Reusable');
    const dialog = await openRemove('worker-a1');
    expect(dialog).toHaveTextContent('It joined with a reusable invite');

    server.invites = refused(503, '', 'internal_error', 'no_coordinator');
    refresh();

    await waitFor(() =>
      expect(dialog).toHaveTextContent(
        'The invite it joined with could not be read'
      )
    );
    expect(dialog).not.toHaveTextContent('It joined with a reusable invite');
    expect(
      within(dialog).queryByRole('checkbox', { name: /Also delete the invite/ })
    ).not.toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Try again' })
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeEnabled();
  });

  it('says a Kubernetes pod keeps restarting until it is scaled down', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    renderPage([
      worker('worker-k1', {
        inviteId: 'inv-r',
        environment: HostEnvironment.kubernetes,
      }),
    ]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-k1');

    expect(dialog).toHaveTextContent(
      'its pod keeps restarting until its Deployment is scaled down'
    );
    expect(dialog).toHaveTextContent(
      'A replacement pod can join with the invite while it is active, so delete the invite as well.'
    );
  });

  // A worker on TLS or plaintext would connect again at once after a
  // removal.
  it('says how to stop a worker without a certificate', async () => {
    renderPage([
      worker('worker-t1', { connection: WorkerConnection.tls }),
      worker('worker-p1', { connection: WorkerConnection.plaintext }),
    ]);
    await screen.findByText('No invites');

    expect(
      screen.queryByRole('button', { name: /^Remove/ })
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(
        /no Remove, since it would connect again at once\. To stop one, stop it on its host/
      )
    ).toBeInTheDocument();
  });

  // An older coordinator reports no connection: nothing says the worker is on
  // the peer token.
  it('says nothing about a worker whose connection is not reported', async () => {
    renderPage([
      worker('worker-a1'),
      worker('worker-o1', { connection: undefined, certificateId: undefined }),
    ]);
    await screen.findByText('No invites');

    expect(screen.queryByText(/peer token/)).not.toBeInTheDocument();
  });

  it.each([
    [
      refused(403, 'insufficient role', 'forbidden'),
      'Only admins can manage workers and invites.',
      'Sign in with an admin account, or ask an admin.',
    ],
    [
      refused(404, 'no worker has the ID', 'not_found'),
      'This worker is no longer listed.',
      'It may be removed already. The list shows its state.',
    ],
    [
      refused(409, 'peer token worker', 'conflict', 'peer_token_worker'),
      'This worker connects with the peer token, so it would connect again at once.',
      'Stop it on its host, or change the peer token.',
    ],
    [
      refused(409, 'own certificates', 'conflict', 'own_certificates'),
      'The coordinator uses its own certificates, so it has no worker certificates to remove.',
      'Stop the worker on its host, or change the peer token.',
    ],
    [
      refused(409, 'older', 'conflict', 'older_coordinator'),
      'The coordinator runs an earlier Dagu release that cannot remove workers.',
      'Upgrade the coordinator.',
    ],
    [
      refused(503, 'no coordinator', 'internal_error', 'no_coordinator'),
      'No coordinator is running or reachable.',
      'Start the coordinator, or check that this server can reach it, then try again.',
    ],
    // The server's own words for a missing token name where it runs; the
    // dialog names the setting only.
    [
      refused(
        503,
        "the coordinator refused the server: set peer.token, or run the server where it reads the coordinator's data directory",
        'internal_error',
        'peer_token_missing'
      ),
      'This server has no peer token for the coordinator.',
      'Set peer.token on this server to the peer token of the coordinator.',
    ],
  ])(
    'announces the cause and the change of a refused removal: %o',
    async (answer, cause, change) => {
      server.del = () => answer;
      renderPage([worker('worker-a1')]);

      const dialog = await openRemove('worker-a1');
      await userEvent.click(
        within(dialog).getByRole('button', { name: 'Remove' })
      );

      const alert = await within(dialog).findByRole('alert');
      expect(alert).toHaveTextContent(cause);
      expect(alert).toHaveTextContent(change);
      expect(alert).not.toHaveTextContent('data directory');
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    }
  );

  // A request that ends after its dialog was closed leaves a newer
  // confirmation of the same row open.
  it('keeps a newer confirmation open when an older removal ends', async () => {
    let answer: (value: { status: number; body: unknown }) => void = () =>
      undefined;
    server.del = () => new Promise((resolve) => (answer = resolve));
    renderPage([worker('worker-a1')]);
    let dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Cancel' })
    );
    dialog = await openRemove('worker-a1');

    await act(async () => answer({ status: 200, body: { state: 'removing' } }));

    expect(screen.getByRole('dialog')).toBe(dialog);
  });

  // Only the admin closes a confirmation: a refresh that takes away its
  // button keeps it, with its refusal, and its button disabled.
  it('keeps Stop runs now open when the worker joins again', async () => {
    server.del = () =>
      refused(409, 'certificate changed', 'conflict', 'certificate_changed');
    const { update } = renderPage([
      worker('worker-b1', {
        removing: true,
        runningTasks: [task('etl', 'run-1')],
      }),
    ]);
    await userEvent.click(
      screen.getByRole('button', { name: 'Stop runs now on worker-b1' })
    );
    const dialog = screen.getByRole('dialog');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Stop runs' })
    );
    const alert = await within(dialog).findByRole('alert');

    update([worker('worker-b1', { certificateId: 'cert-new' })]);

    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(alert).toHaveTextContent('The worker was not removed.');
    expect(
      within(dialog).getByRole('button', { name: 'Stop runs' })
    ).toBeDisabled();
  });

  it('keeps Remove open when the worker comes back on the peer token', async () => {
    server.del = () =>
      refused(409, 'certificate changed', 'conflict', 'certificate_changed');
    const { update } = renderPage([worker('worker-a1')]);
    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    const alert = await within(dialog).findByRole('alert');

    update([
      worker('worker-a1', {
        connection: WorkerConnection.tls,
        certificateId: undefined,
      }),
    ]);

    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(alert).toBeInTheDocument();
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeDisabled();
  });

  // A failed refresh of the workers list takes the rows away; the outcome
  // of the removal stays until the admin closes it.
  it('keeps the outcome when the workers refresh fails', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    server.del = (request) =>
      request.path.endsWith('/certificate')
        ? refused(503, '', 'internal_error', 'no_coordinator')
        : { status: 204 };
    const { update } = renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);
    await screen.findByText('Reusable');
    const dialog = await openRemove('worker-a1');
    await userEvent.click(
      within(dialog).getByRole('checkbox', { name: /Also delete the invite/ })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    await within(dialog).findByRole('alert');

    update([], ['no coordinator is running or reachable']);

    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(within(dialog).getByRole('alert')).toHaveTextContent(
      'No coordinator is running or reachable.'
    );
    expect(requests('DELETE', '/workers/invites/inv-r')).toHaveLength(0);
  });

  // The dialog explains a failed invite lookup itself, with no page notice
  // that would show the server's words.
  it('raises no page notice for a failed invite lookup', async () => {
    server.invites = refused(
      503,
      'the coordinator refused the server: run the server where it reads the data directory',
      'internal_error',
      'peer_token_missing'
    );
    renderPage([worker('worker-a1', { inviteId: 'inv-r' })]);

    const dialog = await openRemove('worker-a1');

    await waitFor(() =>
      expect(dialog).toHaveTextContent(
        'This server has no peer token for the coordinator.'
      )
    );
    expect(screen.queryByText(/data directory/)).not.toBeInTheDocument();
    expect(
      screen.queryByText('Background request failed')
    ).not.toBeInTheDocument();
  });

  // A long list scrolls inside the dialog, which never grows past the
  // screen, so its buttons stay in reach.
  it('scrolls a long confirmation inside the dialog', async () => {
    server.invites = invitesAnswer([
      invite('inv-r', { kind: WorkerInviteKind.reusable }),
    ]);
    const others = Array.from({ length: 60 }, (_, i) =>
      worker(`worker-${i}`, { inviteId: 'inv-r' })
    );
    renderPage([worker('worker-a1', { inviteId: 'inv-r' }), ...others]);
    await screen.findByText('Reusable');

    const dialog = await openRemove('worker-a1');

    expect(dialog).toHaveClass('max-h-[calc(100dvh-2rem)]');
    const body = within(dialog)
      .getByText(/Other workers on this invite/)
      .closest('.overflow-y-auto');
    expect(body).not.toBeNull();
    expect(
      within(dialog).getByRole('button', { name: 'Remove' })
    ).toBeVisible();
  });

  // A plain removal changes no invite, so only the workers are read again.
  it('rereads the workers, not the invites, after a removal', async () => {
    renderPage([worker('worker-a1')]);
    const dialog = await openRemove('worker-a1');
    const before = requests('GET', '/workers/invites').length;

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Remove' })
    );
    await waitFor(() => expect(removals()).toHaveLength(1));
    await act(() => Promise.resolve());

    expect(requests('GET', '/workers/invites')).toHaveLength(before);
  });
});
