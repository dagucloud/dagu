// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Trash2 } from 'lucide-react';
import React from 'react';
import { WorkerInviteKind, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import RelativeTime from '@/components/ui/relative-time';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClock } from '@/hooks/useClock';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { cn } from '@/lib/utils';
import {
  WorkerRequestError,
  describeWorkerError,
  invitesKey,
  readWhenVisible,
  type WorkerErrorAdvice,
  useWorkerActions,
  useWorkerInvites,
} from '../hooks/useWorkerAdmin';
import { WorkerError } from './WorkerError';

type Worker = components['schemas']['Worker'];
type WorkerInvite = components['schemas']['WorkerInvite'];

/** How long an ended invite stays in the list (RFC 029 §Invites). */
const ENDED_SHOWN_MS = 7 * 24 * 60 * 60 * 1000;

/**
 * How long the list waits after a worker joins before it reads the invites,
 * so workers that join together cost one read.
 */
export const JOIN_REREAD_DELAY_MS = 5000;

/** How often the list checks whether an invite expired or left the list. */
const STATE_CLOCK_MS = 60 * 1000;

// One grid for the header and the rows of a wide list. A narrow list stacks
// each invite on two lines instead.
const WIDE_GRID =
  '@3xl:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_6rem_6rem_3.5rem_6rem_5rem_2rem] @3xl:items-center';

export type InviteState = 'active' | 'used' | 'expired';

/**
 * Whether an invite still lets workers join, and when it ended: a single-use
 * invite at its use, any invite at its expiry.
 */
export function inviteState(
  invite: WorkerInvite,
  now: number
): { state: InviteState; endedAt?: string } {
  if (invite.kind === WorkerInviteKind.single_use && invite.useCount > 0) {
    return { state: 'used', endedAt: invite.lastUsedAt ?? invite.createdAt };
  }
  if (invite.expiresAt && Date.parse(invite.expiresAt) <= now) {
    return { state: 'expired', endedAt: invite.expiresAt };
  }
  return { state: 'active' };
}

function shownInvites(invites: WorkerInvite[], now: number): WorkerInvite[] {
  return invites.filter((invite) => {
    const { endedAt } = inviteState(invite, now);
    return !endedAt || now - Date.parse(endedAt) < ENDED_SHOWN_MS;
  });
}

const STATE_TEXT: Record<InviteState, string> = {
  active: 'Active',
  used: 'Used',
  expired: 'Expired',
};

export const KIND_TEXT: Record<WorkerInviteKind, string> = {
  [WorkerInviteKind.single_use]: 'Single use',
  [WorkerInviteKind.reusable]: 'Reusable',
};

/** The part of an invite ID that the list and its dialogs show. */
export function shortInviteId(id: string): string {
  return id.slice(0, 8);
}

interface WorkerInvitesProps {
  /** The page's workers list, undefined until it loads. */
  workers: Worker[] | undefined;
  /** Whether the page's auto refresh is on. */
  live: boolean;
  /** The count of the page's manual refreshes; each one reads again. */
  refreshes: number;
}

/**
 * WorkerInvites lists the invites that let workers on other hosts join, for
 * admins.
 */
function WorkerInvites(props: WorkerInvitesProps) {
  const isAdmin = useIsAdmin();
  const remoteNode = useRemoteNode();
  if (!isAdmin) return null;
  // Another node has other workers and invites: the list starts over, with
  // that node's first workers list as the baseline and one read.
  return <InviteList key={remoteNode} {...props} />;
}

// A confirmation, with its own number so that a delete that ends after its
// dialog was closed cannot close a newer one.
interface Deleting {
  invite: WorkerInvite;
  request: number;
}

function InviteList({ workers, live, refreshes }: WorkerInvitesProps) {
  const { ts } = useI18n();
  const { invites, error, isLoading, mutate } = useWorkerInvites(true);
  const [deleting, setDeleting] = React.useState<Deleting | null>(null);
  const requests = React.useRef(0);
  const remoteNode = useRemoteNode();
  // The server deletes invites on its own coordinators only, never on a
  // remote node, which still lists them.
  const local = remoteNode === 'local';
  const read = React.useCallback(
    () =>
      readWhenVisible(invitesKey(remoteNode).join(':'), () => {
        void mutate();
      }),
    [mutate, remoteNode]
  );
  useInviteReads({ workers, live, refreshes, read });
  const closeDialog = React.useCallback(
    (request: number) =>
      setDeleting((current) => (current?.request === request ? null : current)),
    []
  );

  // Rerenders when an invite expires or leaves the list, also while the
  // page's refresh is paused. The list itself is not read again for it.
  useClock(STATE_CLOCK_MS, (nowMs) =>
    shownInvites(invites ?? [], nowMs)
      .map((invite) => `${invite.id}:${inviteState(invite, nowMs).state}`)
      .join()
  );
  const now = Date.now();
  const shown = shownInvites(invites ?? [], now);

  let body: React.ReactNode;
  if (isLoading) {
    body = <ListMessage text={ts('Loading invites...')} />;
  } else if (error) {
    body = (
      <WorkerError
        className="px-3 py-4"
        action={false}
        advice={describeWorkerError(
          error,
          { notFound: listUnavailable(ts) },
          ts
        )}
      />
    );
  } else if (shown.length === 0) {
    body = <ListMessage text={ts('No invites')} />;
  } else {
    body = (
      <div className="@container">
        <div
          className={cn(
            'hidden gap-x-3 border-b px-3 py-1.5 text-xs text-muted-foreground @3xl:grid',
            WIDE_GRID
          )}
        >
          <span>
            <I18nText text="Kind" />
          </span>
          <span>
            <I18nText text="Created by" />
          </span>
          <span>
            <I18nText text="Created" />
          </span>
          <span>
            <I18nText text="Expiry" />
          </span>
          <span className="text-right">
            <I18nText text="Uses" />
          </span>
          <span>
            <I18nText text="Last use" />
          </span>
          <span>
            <I18nText text="State" />
          </span>
          <span />
        </div>
        <ul className="divide-y">
          {shown.map((invite) => (
            <InviteRow
              key={invite.id}
              invite={invite}
              now={now}
              onDelete={
                local
                  ? () => setDeleting({ invite, request: ++requests.current })
                  : undefined
              }
            />
          ))}
        </ul>
      </div>
    );
  }

  return (
    <div className="card-obsidian mt-4 flex flex-col overflow-hidden">
      <h3 className="border-b px-3 py-2 text-sm font-medium">
        <I18nText text="Invites" />
      </h3>
      {!local && (
        <p className="border-b px-3 py-2 text-xs text-muted-foreground">
          <I18nText text="Delete works on the local server only. Switch to it to delete an invite." />
        </p>
      )}
      {body}
      {deleting && (
        <DeleteInviteDialog
          key={deleting.request}
          invite={deleting.invite}
          onClose={() => closeDialog(deleting.request)}
        />
      )}
    </div>
  );
}

/**
 * Decides when the invites list reads the invites: once on open, after the
 * first workers list, so that every worker listed then joined before the
 * read; once when the page's refresh resumes or is refreshed by
 * hand, once after a delete, and once after workers join, since a join uses
 * an invite. A join is a worker ID, certificate, or invite not listed
 * before, so a worker that leaves and comes back is no join. Joins within
 * JOIN_REREAD_DELAY_MS cost one read. Nothing is read while the tab is
 * hidden; a read that falls due then waits until it shows, and reads asked
 * for meanwhile, such as by a delete, run once with it.
 */
function useInviteReads({
  workers,
  live,
  refreshes,
  read,
}: {
  workers: Worker[] | undefined;
  live: boolean;
  refreshes: number;
  read: () => void;
}) {
  const [due, setDue] = React.useState<{ delay: number } | null>({
    delay: 0,
  });
  // Whether the first workers list arrived; no read runs before it.
  const [ready, setReady] = React.useState(false);
  const requestRead = React.useCallback(() => setDue({ delay: 0 }), []);

  const seen = React.useRef<Set<string> | null>(null);

  const joins = workers
    ?.map((worker) =>
      [worker.id, worker.certificateId ?? '', worker.inviteId ?? ''].join(' ')
    )
    .join('\n');
  React.useEffect(() => {
    if (joins === undefined) return;
    const listed = joins ? joins.split('\n') : [];
    if (seen.current === null) {
      seen.current = new Set(listed);
      setReady(true);
      return;
    }
    const known = seen.current;
    const joined = listed.filter((entry) => !known.has(entry));
    if (joined.length === 0) return;
    joined.forEach((entry) => known.add(entry));
    setDue({ delay: JOIN_REREAD_DELAY_MS });
  }, [joins]);

  const wasLive = React.useRef(live);
  React.useEffect(() => {
    if (live && !wasLive.current) requestRead();
    wasLive.current = live;
  }, [live, requestRead]);

  const handled = React.useRef(refreshes);
  React.useEffect(() => {
    if (handled.current === refreshes) return;
    handled.current = refreshes;
    requestRead();
  }, [refreshes, requestRead]);

  React.useEffect(() => {
    if (!due || !ready) return;
    if (due.delay === 0) {
      setDue(null);
      read();
      return;
    }
    const timer = setTimeout(() => {
      setDue(null);
      read();
    }, due.delay);
    return () => clearTimeout(timer);
  }, [due, ready, read]);
}

/**
 * The advice for a 404 of the invites list: the selected server has no such
 * endpoint, as one of an earlier release.
 */
export function listUnavailable(
  ts: (source: string) => string
): WorkerErrorAdvice {
  return {
    cause: ts('The selected server does not offer worker invites.'),
    change: ts('Check that it runs a Dagu release with worker invites.'),
  };
}

function ListMessage({ text }: { text: string }) {
  return <p className="px-3 py-4 text-sm text-muted-foreground">{text}</p>;
}

function InviteRow({
  invite,
  now,
  onDelete,
}: {
  invite: WorkerInvite;
  now: number;
  onDelete?: () => void;
}) {
  const { ts } = useI18n();
  const { state } = inviteState(invite, now);
  const expired = !!invite.expiresAt && Date.parse(invite.expiresAt) <= now;
  const id = shortInviteId(invite.id);
  const deleteLabel = ts('Delete invite {id} ({kind})', {
    id,
    kind: ts(KIND_TEXT[invite.kind]),
  });

  return (
    <li
      aria-label={ts('Invite {id}', { id })}
      className={cn(
        'grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 px-3 py-2 text-sm',
        WIDE_GRID,
        state !== 'active' && 'text-muted-foreground'
      )}
    >
      <div className="min-w-0">
        <div className="font-medium">
          <I18nText text={KIND_TEXT[invite.kind]} />
        </div>
        {/* The address moves to its own line before it is broken. */}
        <div className="flex flex-wrap gap-x-1 font-mono text-xs text-muted-foreground">
          <span>{id}</span>{' '}
          {invite.address && (
            <span className="min-w-0 break-words">· {invite.address}</span>
          )}
        </div>
      </div>
      <div className="row-span-2 self-center @3xl:order-last @3xl:row-span-1">
        {onDelete && (
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={deleteLabel}
            title={deleteLabel}
            onClick={onDelete}
          >
            <Trash2 />
          </Button>
        )}
      </div>
      {/* On a wide list each detail is a grid cell; on a narrow one they wrap
          on one line, each with its label. */}
      <div className="flex min-w-0 flex-wrap gap-x-3 text-xs text-muted-foreground @3xl:contents @3xl:text-sm">
        <span className="min-w-0 truncate">
          <NarrowLabel text="By" />
          {invite.createdBy}
        </span>
        <span>
          <NarrowLabel text="Created" />
          <RelativeTime timestamp={invite.createdAt} compact ago />
        </span>
        <span>
          {invite.expiresAt ? (
            <>
              <NarrowLabel text="Expiry" />
              <RelativeTime
                timestamp={invite.expiresAt}
                compact
                ago={expired}
                until={!expired}
              />
            </>
          ) : (
            <I18nText text="No expiry" />
          )}
        </span>
        <span className="tabular-nums @3xl:text-right">
          {invite.useCount}
          <span className="@3xl:sr-only">
            {' '}
            <I18nText text={invite.useCount === 1 ? 'use' : 'uses'} />
          </span>
        </span>
        <span>
          <NarrowLabel text="Last use" />
          {invite.lastUsedAt ? (
            <RelativeTime timestamp={invite.lastUsedAt} compact ago />
          ) : (
            <I18nText text="Never" />
          )}
        </span>
        <span>
          <span className="sr-only">
            <I18nText text="State" />{' '}
          </span>
          <I18nText text={STATE_TEXT[state]} />
        </span>
      </div>
    </li>
  );
}

// A label that a narrow list shows before its value. A wide list shows the
// header row instead and keeps the label for screen readers, since the
// header row is not part of the list.
function NarrowLabel({ text }: { text: string }) {
  return (
    <span className="@3xl:sr-only">
      <I18nText text={text} />{' '}
    </span>
  );
}

// The advice for a delete of an invite that is already gone: the list is
// read again, and there is nothing left to delete.
function deleteGone(ts: (source: string) => string): WorkerErrorAdvice {
  return {
    cause: ts('This invite is already gone.'),
    change: ts('The list is read again. Close this dialog.'),
  };
}

function DeleteInviteDialog({
  invite,
  onClose,
}: {
  invite: WorkerInvite;
  onClose: () => void;
}) {
  const { ts } = useI18n();
  const { deleteInvite, rereadInvites } = useWorkerActions();
  const [error, setError] = React.useState<unknown>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const gone = error instanceof WorkerRequestError && error.status === 404;

  const submit = async () => {
    if (submitting || gone) return;
    setSubmitting(true);
    setError(null);
    try {
      await deleteInvite(invite.id);
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setSubmitting(false);
      // The server deletes on its own coordinators, so the local list is
      // read again, also after a switch to another node and back.
      rereadInvites('local');
    }
  };

  return (
    <ConfirmModal
      title={ts('Delete invite')}
      buttonText={ts('Delete')}
      visible
      dismissModal={onClose}
      onSubmit={() => void submit()}
      submitDisabled={submitting || gone}
      initialFocus="cancel"
    >
      <div className="space-y-2 text-sm">
        <p className="font-medium">
          <I18nText
            text="Invite {id}: {kind}, created {time}"
            values={{
              id: shortInviteId(invite.id),
              kind: ts(KIND_TEXT[invite.kind]),
              time: formatAbsoluteTime(invite.createdAt) ?? invite.createdAt,
            }}
          />
        </p>
        <p>
          <I18nText
            text={
              'No new worker can join with this invite. Workers that joined with it keep their certificates.'
            }
          />
        </p>
        {error !== null && (
          <WorkerError
            action
            advice={describeWorkerError(
              error,
              { notFound: deleteGone(ts) },
              ts
            )}
          />
        )}
      </div>
    </ConfirmModal>
  );
}

export default WorkerInvites;
