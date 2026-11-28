// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Admin reads and changes of worker invites.
 *
 * @module features/workers/hooks
 */
import { useCallback } from 'react';
import useSWR, { useSWRConfig } from 'swr';
import { WorkerAccessReason, type components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import type { StaticTranslationValues } from '@/i18n/translateStatic';

type WorkerInvitesList = components['schemas']['WorkerInvitesListResponse'];

/**
 * A failed worker admin request: its HTTP status, 0 when no answer came,
 * for a 409 or a 503 the reason the API names in `details.reason`, and the
 * server's message, which the page shows only as a detail of a refusal it
 * cannot explain itself.
 */
export class WorkerRequestError extends Error {
  constructor(
    readonly status: number,
    readonly reason?: string,
    readonly serverMessage?: string
  ) {
    super(`worker admin request failed with ${status}`);
  }
}

/**
 * What went wrong, and what the admin can change about it. `detail` is the
 * server's message, for a refusal whose cause only the server names.
 */
export interface WorkerErrorAdvice {
  cause: string;
  change: string;
  detail?: string;
}

/** The cache key of the invites of one node. */
export function invitesKey(node: string): readonly [string, string] {
  return ['worker-invites', node];
}

// The reads waiting for the tab to show, by cache key: the reads asked for
// one key while the tab was hidden become one read.
const waitingReads = new Map<string, () => void>();

/**
 * Runs a read of `key` now, or once the tab shows when it is hidden, so that
 * no read leaves a hidden tab. Reads of one key asked for while hidden run
 * once.
 */
export function readWhenVisible(key: string, run: () => void) {
  if (document.visibilityState !== 'hidden') {
    run();
    return;
  }
  const queued = waitingReads.has(key);
  waitingReads.set(key, run);
  if (queued) return;
  const onShow = () => {
    if (document.visibilityState === 'hidden') return;
    document.removeEventListener('visibilitychange', onShow);
    const next = waitingReads.get(key);
    waitingReads.delete(key);
    next?.();
  };
  document.addEventListener('visibilitychange', onShow);
}

// The invite reads in flight, by node: reads asked for while one is under
// way share its answer, unless the invites changed after it started.
const readsInFlight = new Map<
  string,
  { read: Promise<WorkerInvitesList>; generation: number }
>();

// The last read started for each node. A read that a later one superseded
// answers with the later one's outcome, so it can neither show its old list
// nor its failure over a newer list.
const latestReads = new Map<string, Promise<WorkerInvitesList>>();

// Counts the changes to the invites of each node. A read that started
// before a change cannot answer for after it.
const inviteGenerations = new Map<string, number>();

function inviteGeneration(node: string): number {
  return inviteGenerations.get(node) ?? 0;
}

/**
 * Marks the invites of a node as changed, as by a delete: the next read
 * starts afresh instead of sharing one that started before the change.
 */
export function invitesChanged(node: string) {
  inviteGenerations.set(node, inviteGeneration(node) + 1);
}

/**
 * Reads the invites of the selected node. Nothing reads them on its own: no
 * read on mount, no retry, and no read on focus or reconnect. The caller
 * decides when to read with `mutate`, and explains a failed read itself:
 * the page's notice of a failed background read would show the server's
 * words. Any answer other than a 2xx with a list is a failure. When reads
 * overlap, the one that started last wins.
 */
export function useWorkerInvites(enabled: boolean) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { data, error, mutate } = useSWR<WorkerInvitesList, unknown>(
    enabled ? invitesKey(remoteNode) : null,
    () => {
      const generation = inviteGeneration(remoteNode);
      const running = readsInFlight.get(remoteNode);
      if (running && running.generation === generation) return running.read;
      const read: Promise<WorkerInvitesList> = (async () => {
        const { data, error, response } = await client.GET('/workers/invites', {
          params: { query: { remoteNode } },
        });
        // An answer that is not a list, such as `{}` or a bare 502, is a
        // failure, not an empty list.
        if (!response.ok || !Array.isArray(data?.invites)) {
          throw requestError(response.status, error);
        }
        return data;
      })()
        .catch((err: unknown) => {
          const latest = latestReads.get(remoteNode);
          if (latest && latest !== read) return latest;
          throw err;
        })
        .finally(() => {
          if (readsInFlight.get(remoteNode)?.read === read) {
            readsInFlight.delete(remoteNode);
          }
        });
      readsInFlight.set(remoteNode, { read, generation });
      latestReads.set(remoteNode, read);
      return read;
    },
    {
      shouldRetryOnError: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      revalidateIfStale: false,
      revalidateOnMount: false,
      onError: () => undefined,
    }
  );
  return {
    invites: data?.invites,
    error,
    isLoading: enabled && !data && !error,
    mutate,
  };
}

/**
 * Deletes invites. The server makes this change on its own coordinators
 * only, never on a remote node, so its audit names the admin who made it.
 */
export function useWorkerActions() {
  const client = useClient();
  const { mutate } = useSWRConfig();

  // Reads the invites of a node again after a change, once the tab shows,
  // also when the list that asked is gone, as after a switch to another
  // node and back. A read that started before the change does not count.
  const rereadInvites = useCallback(
    (node: string) => {
      invitesChanged(node);
      readWhenVisible(invitesKey(node).join(':'), () => {
        void mutate(invitesKey(node));
      });
    },
    [mutate]
  );

  const deleteInvite = useCallback(
    async (inviteId: string) => {
      const { error, response } = await client.DELETE(
        '/workers/invites/{inviteId}',
        { params: { path: { inviteId } } }
      );
      if (!response.ok) throw requestError(response.status, error);
    },
    [client]
  );

  return { deleteInvite, rereadInvites };
}

// Only a 409 and a 503 carry a worker access reason (Spec 108); the
// details of any other status are not read.
const REASON_STATUSES: ReadonlySet<number> = new Set([409, 503]);

function requestError(status: number, body: unknown): WorkerRequestError {
  if (!REASON_STATUSES.has(status)) return new WorkerRequestError(status);
  const answer = body as
    | { message?: unknown; details?: { reason?: unknown } }
    | undefined;
  const reason = answer?.details?.reason;
  const message = answer?.message;
  return new WorkerRequestError(
    status,
    typeof reason === 'string' ? reason : undefined,
    typeof message === 'string' && message ? message : undefined
  );
}

type Translate = (source: string, values?: StaticTranslationValues) => string;

type Advise = (ts: Translate) => WorkerErrorAdvice;

/** Advice that replaces the shared text of some reasons for one call. */
export type ReasonAdvice = Partial<Record<WorkerAccessReason, Advise>>;

// The reasons any worker access call can fail with.
// Every reason has its own text; a reason the API adds fails the type check
// until it has one.
const ACCESS_REASONS: Record<WorkerAccessReason, Advise> = {
  [WorkerAccessReason.own_certificates]: (ts) => ({
    cause: ts(
      'The coordinator uses its own certificates, so it has no built-in CA for invites.'
    ),
    change: ts(
      'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.'
    ),
  }),
  [WorkerAccessReason.ca_unavailable]: (ts) => ({
    cause: ts('The coordinator could not create or read its CA.'),
    change: ts(
      'See the coordinator start warning, fix its CA, then restart it.'
    ),
  }),
  [WorkerAccessReason.peer_token_missing]: (ts) => ({
    cause: ts('This server has no peer token for the coordinator.'),
    change: ts(
      'Set peer.token on this server to the peer token of the coordinator.'
    ),
  }),
  [WorkerAccessReason.peer_token_mismatch]: (ts) => ({
    cause: ts('This server and the coordinator have different peer tokens.'),
    change: ts('Give the server the same peer.token as the coordinator.'),
  }),
  [WorkerAccessReason.require_tls]: (ts) => ({
    cause: ts(
      'The coordinator requires TLS, and this server called it over plaintext.'
    ),
    change: ts(
      'Remove {setting} from the server config if it is set, and check that the network path to the coordinator passes TLS.',
      { setting: 'peer.insecure: true' }
    ),
  }),
  [WorkerAccessReason.coordinator_refused]: (ts) => ({
    cause: ts('The coordinator refused the server.'),
    change: ts(
      'Check the coordinator log for the reason, or run {command} on this host, then try again.',
      { command: 'dagu worker --check' }
    ),
  }),
  [WorkerAccessReason.coordinator_conflict]: (ts) => ({
    cause: ts('The coordinator could not do this in its current state.'),
    change: ts(
      'Check the coordinator log for the state that conflicts, then try again once it is resolved.'
    ),
  }),
  [WorkerAccessReason.retry_later]: (ts) => ({
    cause: ts('The coordinator answered but cannot do this yet.'),
    change: ts('Wait a moment, then try again.'),
  }),
  [WorkerAccessReason.older_coordinator]: (ts) => ({
    cause: ts(
      'The coordinator runs an earlier Dagu release without worker invites.'
    ),
    change: ts('Upgrade the coordinator.'),
  }),
  [WorkerAccessReason.no_coordinator]: (ts) => ({
    cause: ts('No coordinator is running or reachable.'),
    change: ts(
      'Start the coordinator, or check that this server can reach it, then try again.'
    ),
  }),
  [WorkerAccessReason.peer_token_worker]: (ts) => ({
    cause: ts(
      'This worker connects with the peer token, so it would connect again at once.'
    ),
    change: ts('Stop it on its host, or change the peer token.'),
  }),
  [WorkerAccessReason.certificate_changed]: (ts) => ({
    cause: ts(
      'This worker no longer holds the certificate the list showed, as after it joined again.'
    ),
    change: ts(
      'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.'
    ),
  }),
};

/**
 * The cause of a failed request and the change that fixes it, chosen by the
 * reason the API names, else by the HTTP status. `reasons` adds the reasons
 * of one call, such as a removal's. The server's message is never the
 * cause; it is the detail of a coordinator refusal and of a reason the page
 * does not know, whose cause only the server names.
 */
export function describeWorkerError(
  error: unknown,
  advice: {
    notFound: WorkerErrorAdvice;
    reasons?: ReasonAdvice;
    /** Whether the request went to a remote node, with its credentials. */
    remoteNode?: boolean;
  },
  ts: Translate
): WorkerErrorAdvice {
  const failure = error instanceof WorkerRequestError ? error : undefined;
  const reason = failure?.reason;
  const known = reason
    ? (ownAdvice(advice.reasons, reason) ?? ownAdvice(ACCESS_REASONS, reason))
    : undefined;
  const detail =
    reason && (!known || reason === WorkerAccessReason.coordinator_refused)
      ? failure?.serverMessage
      : undefined;
  if (known) return { ...known(ts), detail };
  if (failure?.status === 403 && advice.remoteNode) {
    // The server calls a remote node with the credentials configured for
    // it, so signing in again here changes nothing.
    return {
      cause: ts(
        'The credentials this server uses for the selected node lack admin access there.'
      ),
      change: ts('Give those credentials the admin role on the selected node.'),
      detail,
    };
  }
  return { ...statusAdvice(failure?.status, advice.notFound, ts), detail };
}

function statusAdvice(
  status: number | undefined,
  notFound: WorkerErrorAdvice,
  ts: Translate
): WorkerErrorAdvice {
  switch (status) {
    case 403:
      return {
        cause: ts('Only admins can manage workers and invites.'),
        change: ts('Sign in with an admin account, or ask an admin.'),
      };
    case 404:
      return notFound;
    case 409:
      return {
        cause: ts('The coordinator refused the request.'),
        change: ts(
          'Check the coordinator log for the reason, or run {command} on this host, then try again.',
          { command: 'dagu worker --check' }
        ),
      };
    default:
      return {
        cause: ts('The request failed.'),
        change: ts('Please try again or check the server connection.'),
      };
  }
}

// Looks a reason up among a table's own entries only, so that a name such as
// "toString" is an unknown reason, not an inherited function.
function ownAdvice(
  table: ReasonAdvice | undefined,
  reason: string
): ((ts: Translate) => WorkerErrorAdvice) | undefined {
  return table && Object.prototype.hasOwnProperty.call(table, reason)
    ? table[reason as WorkerAccessReason]
    : undefined;
}
