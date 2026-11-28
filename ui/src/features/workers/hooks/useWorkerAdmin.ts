// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Admin reads and changes of worker invites and worker certificates.
 *
 * @module features/workers/hooks
 */
import { useCallback, useEffect, useSyncExternalStore } from 'react';
import { useSWRConfig } from 'swr';
import {
  WorkerAccessReason,
  WorkerRemovalRuns,
  WorkerRemovalState,
  type components,
} from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { addAuthSessionListener } from '@/lib/authSession';
import type { StaticTranslationValues } from '@/i18n/translateStatic';

type WorkerInvitesList = components['schemas']['WorkerInvitesListResponse'];
type WorkerInvite = components['schemas']['WorkerInvite'];

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

/** A delete of an invite that failed after its dialog was closed. */
export interface LateFailure {
  invite: WorkerInvite;
  error: unknown;
}

// The invites of one node as the page last read them.
interface NodeInvites {
  /** The last settled read: its list, or its failure beside the list before. */
  list?: WorkerInvitesList;
  error?: unknown;
  /** Counts the changes to the invites; a read from before one answers nothing. */
  generation: number;
  read?: { generation: number; promise: Promise<void> };
  /** The lists that show these invites now. */
  watchers: number;
}

/**
 * The invite reads and deletes of the page, which components render from.
 * It lives as long as the page's SWR cache, so it outlives the list, a node
 * switch, and a route change, and starts over with each sign-in or
 * sign-out, as that cache does.
 */
class InviteStore {
  private readonly nodes = new Map<string, NodeInvites>();
  // The deletes under way, by invite ID; an invite has one at most.
  private readonly deletes = new Map<string, Promise<void>>();
  private readonly failures = new Map<string, LateFailure>();
  private readonly listeners = new Set<() => void>();
  private version = 0;
  // Counts the sessions; a delete from an earlier one changes nothing kept.
  private session = 0;

  /** The current auth session, to tell a request's outcome from a later one's. */
  currentSession(): number {
    return this.session;
  }

  constructor() {
    addAuthSessionListener(() => this.reset());
  }

  // Forgets what the previous session read and deleted. Its requests still
  // under way update entries no longer kept.
  private reset() {
    this.session += 1;
    this.nodes.clear();
    this.deletes.clear();
    this.failures.clear();
    this.changedState();
  }

  readonly subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  readonly snapshot = () => this.version;

  private changedState() {
    this.version += 1;
    this.listeners.forEach((listener) => listener());
  }

  node(node: string): NodeInvites {
    let entry = this.nodes.get(node);
    if (!entry) {
      entry = { generation: 0, watchers: 0 };
      this.nodes.set(node, entry);
    }
    return entry;
  }

  watch(node: string): () => void {
    this.node(node).watchers += 1;
    return () => {
      this.node(node).watchers -= 1;
    };
  }

  /** Marks the invites of a node as changed: a read under way answers nothing. */
  invalidate(node: string) {
    this.node(node).generation += 1;
  }

  /**
   * Reads the invites of a node. Reads asked for during one share it,
   * unless the invites changed after it started. A read the invites changed
   * under is dropped, and the last settled list and failure stay.
   */
  read(client: Client, node: string): Promise<void> {
    const entry = this.node(node);
    const generation = entry.generation;
    if (entry.read && entry.read.generation === generation) {
      return entry.read.promise;
    }
    const promise = (async () => {
      let list: WorkerInvitesList | undefined;
      let error: unknown;
      try {
        const answer = await client.GET('/workers/invites', {
          params: { query: { remoteNode: node } },
        });
        // An answer that is not a list, such as `{}` or a bare 502, is a
        // failure, not an empty list.
        if (!answer.response.ok || !Array.isArray(answer.data?.invites)) {
          throw requestError(answer.response.status, answer.error);
        }
        list = answer.data;
      } catch (err) {
        error = err;
      }
      if (entry.read?.generation === generation) entry.read = undefined;
      if (entry.generation !== generation) return;
      if (list) {
        entry.list = list;
        entry.error = undefined;
      } else {
        entry.error = error;
      }
      this.changedState();
    })();
    entry.read = { generation, promise };
    return promise;
  }

  /** Reads the invites of a node again, once the tab shows, if a list shows them. */
  reread(client: Client, node: string) {
    this.invalidate(node);
    readWhenVisible(invitesKey(node).join(':'), () => {
      if (this.node(node).watchers > 0) void this.read(client, node);
    });
  }

  pending(inviteId: string): boolean {
    return this.deletes.has(inviteId);
  }

  /**
   * Deletes an invite of this server. An invite that is gone, deleted now or
   * already, leaves the list at once, with any failure to delete it.
   */
  delete(client: Client, inviteId: string): Promise<void> {
    const running = this.deletes.get(inviteId);
    if (running) return running;
    const session = this.session;
    const promise = (async () => {
      try {
        const { error, response } = await client.DELETE(
          '/workers/invites/{inviteId}',
          { params: { path: { inviteId } } }
        );
        if (!response.ok && response.status !== 404) {
          throw requestError(response.status, error);
        }
        if (this.session === session) this.gone(inviteId);
        if (!response.ok) throw requestError(response.status, error);
      } finally {
        if (this.session === session) {
          this.deletes.delete(inviteId);
          this.changedState();
        }
      }
    })();
    this.deletes.set(inviteId, promise);
    this.changedState();
    return promise;
  }

  private gone(inviteId: string) {
    const local = this.node('local');
    if (local.list) {
      local.list = {
        ...local.list,
        invites: local.list.invites.filter((invite) => invite.id !== inviteId),
      };
    }
    this.invalidate('local');
    this.failures.delete(inviteId);
  }

  lateFailures(): LateFailure[] {
    return [...this.failures.values()];
  }

  failed(failure: LateFailure) {
    this.failures.delete(failure.invite.id);
    this.failures.set(failure.invite.id, failure);
    this.changedState();
  }

  dismiss(inviteId: string) {
    if (this.failures.delete(inviteId)) this.changedState();
  }
}

type Client = ReturnType<typeof useClient>;

const stores = new WeakMap<object, InviteStore>();

// The store of the page's SWR cache, rendering again on each change.
function useInviteStore(): InviteStore {
  const { cache } = useSWRConfig();
  let store = stores.get(cache);
  if (!store) {
    store = new InviteStore();
    stores.set(cache, store);
  }
  useSyncExternalStore(store.subscribe, store.snapshot);
  return store;
}

/**
 * Reads the invites of the selected node. Nothing reads them on its own: the
 * caller decides when with `mutate`, and explains a failed read itself, so
 * no page notice shows the server's words. `invalidate` marks the invites
 * as changed, so that a read under way answers nothing.
 */
export function useWorkerInvites(enabled: boolean) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const store = useInviteStore();
  useEffect(
    () => (enabled ? store.watch(remoteNode) : undefined),
    [enabled, remoteNode, store]
  );
  const entry = store.node(remoteNode);
  const mutate = useCallback(
    () => store.read(client, remoteNode),
    [client, remoteNode, store]
  );
  const invalidate = useCallback(
    () => store.invalidate(remoteNode),
    [remoteNode, store]
  );
  return {
    invites: enabled ? entry.list?.invites : undefined,
    error: enabled ? entry.error : undefined,
    isLoading: enabled && !entry.list && entry.error === undefined,
    mutate,
    invalidate,
  };
}

/**
 * The deletes of this server's invites: which are under way, and the ones
 * that failed after their dialog was closed.
 */
export function useInviteDeletes() {
  const store = useInviteStore();
  return {
    pending: (inviteId: string) => store.pending(inviteId),
    lateFailures: store.lateFailures(),
    failed: useCallback(
      (failure: LateFailure) => store.failed(failure),
      [store]
    ),
    dismiss: useCallback(
      (inviteId: string) => store.dismiss(inviteId),
      [store]
    ),
  };
}

/**
 * Deletes invites and removes workers. The server makes these changes on
 * its own coordinators only, never on a remote node, so its audit names
 * the admin who made them.
 */
export function useWorkerActions() {
  const client = useClient();
  const store = useInviteStore();
  const { mutate } = useSWRConfig();

  // Reads the invites of a node again after a change, once the tab shows,
  // also when the list that asked is gone, as after a switch to another
  // node and back. A read that started before the change does not count.
  const rereadInvites = useCallback(
    (node: string) => store.reread(client, node),
    [client, store]
  );

  // Reads the page's workers list again once the tab shows, so a change
  // shows also while the page's auto refresh is off.
  const rereadWorkers = useCallback(
    () =>
      readWhenVisible('workers', () => {
        void mutate((key) => Array.isArray(key) && key[1] === '/workers');
      }),
    [mutate]
  );

  const deleteInvite = useCallback(
    (inviteId: string) => store.delete(client, inviteId),
    [client, store]
  );

  const removeWorker = useCallback(
    async (
      worker: { id: string; certificateId: string },
      runs: WorkerRemovalRuns
    ): Promise<WorkerRemovalState> => {
      // The certificate the admin confirmed: a worker that joined again with
      // another one answers 409 instead of being removed.
      // The worker ID goes in the body as it is, so any ID, such as one
      // with a slash or one that names a route, reaches the server intact.
      const { data, error, response } = await client.POST('/workers/removals', {
        body: {
          workerId: worker.id,
          certificateId: worker.certificateId,
          runs,
        },
      });
      // Only a state the API defines counts as accepted; an answer such as
      // `{}` is a failure, so nothing that depends on the removal follows.
      if (!response.ok || !isRemovalState(data?.state)) {
        throw requestError(response.status, error);
      }
      return data.state;
    },
    [client]
  );

  // The current auth session: what a request started in an earlier one
  // reports is dropped.
  const session = useCallback(() => store.currentSession(), [store]);

  return {
    deleteInvite,
    removeWorker,
    rereadInvites,
    rereadWorkers,
    session,
  };
}

function isRemovalState(state: unknown): state is WorkerRemovalState {
  return Object.values(WorkerRemovalState).includes(
    state as WorkerRemovalState
  );
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
    // The same reason covers a server set to plaintext, one without the CA
    // certificate of the coordinator, and a network path that drops TLS.
    change: ts(
      'Clear {insecure} or {skipVerify} in the server config if set, and set {caFile} to the CA certificate of the coordinator if this server has none. If both are in order, check that the network path to the coordinator passes TLS.',
      {
        insecure: 'peer.insecure: true',
        skipVerify: 'peer.skip_tls_verify: true',
        caFile: 'peer.ca_cert_file',
      }
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
// On a remote node, the selected node calls its coordinator, so the reasons
// that name the caller name that node instead of this server.
const REMOTE_REASONS: ReasonAdvice = {
  [WorkerAccessReason.peer_token_missing]: (ts) => ({
    cause: ts('The selected node has no peer token for its coordinator.'),
    change: ts(
      'Set peer.token on the selected node to the peer token of its coordinator.'
    ),
  }),
  [WorkerAccessReason.peer_token_mismatch]: (ts) => ({
    cause: ts(
      'The selected node and its coordinator have different peer tokens.'
    ),
    change: ts(
      'Give the selected node the same peer.token as its coordinator.'
    ),
  }),
  [WorkerAccessReason.require_tls]: (ts) => ({
    cause: ts(
      'The coordinator requires TLS, and the selected node called it over plaintext.'
    ),
    change: ts(
      'Clear {insecure} or {skipVerify} in the config of the selected node if set, and set {caFile} there to the CA certificate of the coordinator if it has none. If both are in order, check that the network path from the selected node to the coordinator passes TLS.',
      {
        insecure: 'peer.insecure: true',
        skipVerify: 'peer.skip_tls_verify: true',
        caFile: 'peer.ca_cert_file',
      }
    ),
  }),
  [WorkerAccessReason.coordinator_refused]: (ts) => ({
    cause: ts('The coordinator refused the selected node.'),
    change: ts(
      'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.',
      { command: 'dagu worker --check' }
    ),
  }),
  [WorkerAccessReason.no_coordinator]: (ts) => ({
    cause: ts('No coordinator is running or reachable.'),
    change: ts(
      'Start the coordinator, or check that the selected node can reach it, then try again.'
    ),
  }),
};

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
    ? (ownAdvice(advice.reasons, reason) ??
      (advice.remoteNode ? ownAdvice(REMOTE_REASONS, reason) : undefined) ??
      ownAdvice(ACCESS_REASONS, reason))
    : undefined;
  const detail =
    reason && (!known || reason === WorkerAccessReason.coordinator_refused)
      ? failure?.serverMessage
      : undefined;
  if (known) return { ...known(ts), detail };
  if (failure?.status === 409 && advice.remoteNode) {
    // The selected node made the coordinator call, so the check runs there.
    return {
      cause: ts('The coordinator refused the request.'),
      change: ts(
        'Check the coordinator log for the reason, or run {command} on the host of the selected node, then try again.',
        { command: 'dagu worker --check' }
      ),
      detail,
    };
  }
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
