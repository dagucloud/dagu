// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Admin reads and changes of worker invites.
 *
 * @module features/workers/hooks
 */
import { useCallback } from 'react';
import { useSWRConfig } from 'swr';
import { ErrorCode, WorkerAccessReason } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from '@/hooks/api';
import type { StaticTranslationValues } from '@/i18n/translateStatic';
import { getQueryErrorCode, getQueryErrorMessage } from '@/lib/queryErrors';

/**
 * A refused worker admin request, with its HTTP status and, for a 409 or a
 * 503, the reason the API names in `details.reason`.
 */
export class WorkerRequestError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly reason?: string
  ) {
    super(message);
  }
}

/** What went wrong, and what the admin can change about it. */
export interface WorkerErrorAdvice {
  cause: string;
  change: string;
}

/**
 * Reads the invites. Nothing reads them on its own: no read on mount, no
 * retry, and no read on focus or reconnect. The caller decides when to read
 * with `mutate`.
 */
export function useWorkerInvites(enabled: boolean) {
  const remoteNode = useRemoteNode();
  const { data, error, mutate } = useQuery(
    '/workers/invites',
    enabled ? { params: { query: { remoteNode } } } : null,
    {
      shouldRetryOnError: false,
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      revalidateIfStale: false,
      revalidateOnMount: false,
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

  // Rereads what a change touched once, so it shows at once, also while the
  // page's auto refresh is off.
  const reread = useCallback(
    (path: '/workers/invites') =>
      mutate((key) => Array.isArray(key) && key[1] === path).then(
        () => undefined
      ),
    [mutate]
  );

  const deleteInvite = useCallback(
    async (inviteId: string) => {
      const { error, response } = await client.DELETE(
        '/workers/invites/{inviteId}',
        { params: { path: { inviteId } } }
      );
      if (error) {
        throw requestError(response.status, error);
      }
    },
    [client]
  );

  return { deleteInvite, reread };
}

function requestError(status: number, body: unknown): WorkerRequestError {
  return new WorkerRequestError(
    status,
    getQueryErrorMessage(body, ''),
    reasonOf(body)
  );
}

// The API names why a worker access call failed in `details.reason`.
function reasonOf(error: unknown): string | undefined {
  if (error instanceof WorkerRequestError) return error.reason;
  const reason = (error as { details?: { reason?: unknown } } | undefined)
    ?.details?.reason;
  return typeof reason === 'string' ? reason : undefined;
}

type Translate = (source: string, values?: StaticTranslationValues) => string;

/** The advice for each reason, by the reason the API names. */
export type ReasonAdvice = Partial<
  Record<WorkerAccessReason, (ts: Translate) => WorkerErrorAdvice>
>;

// The reasons any worker access call can fail with. The coordinator's own
// words matter only for coordinator_refused, which names no fixed cause.
const ACCESS_REASONS: ReasonAdvice = {
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
    change: ts('Remove {setting} from the server config, then restart it.', {
      setting: 'peer.insecure: true',
    }),
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
};

/**
 * The cause of a failed request and the change that fixes it, chosen by the
 * reason the API names. `reasons` adds the reasons of one call, such as a
 * removal's. Without a known reason, the server's message is the cause.
 */
export function describeWorkerError(
  error: unknown,
  advice: { notFound: WorkerErrorAdvice; reasons?: ReasonAdvice },
  ts: Translate
): WorkerErrorAdvice {
  const message = getQueryErrorMessage(error, '');
  const reason = reasonOf(error) as WorkerAccessReason | undefined;
  if (reason === WorkerAccessReason.coordinator_refused) {
    return {
      cause: message || ts('The coordinator refused the server.'),
      change: ts('Make the change the message names, then try again.'),
    };
  }
  const known = reason
    ? (advice.reasons?.[reason] ?? ACCESS_REASONS[reason])
    : undefined;
  if (known) return known(ts);

  const status =
    error instanceof WorkerRequestError
      ? error.status
      : statusOfCode(getQueryErrorCode(error));
  if (status === 403) {
    return {
      cause: ts('Only admins can manage workers and invites.'),
      change: ts('Sign in with an admin account, or ask an admin.'),
    };
  }
  if (status === 404) return advice.notFound;
  return {
    cause: message || ts('The request failed.'),
    change: ts('Please try again or check the server connection.'),
  };
}

// A list read rejects with the error body, which carries a code and no
// status.
function statusOfCode(code: string | undefined): number {
  switch (code) {
    case ErrorCode.forbidden:
      return 403;
    case ErrorCode.not_found:
      return 404;
    default:
      return 0;
  }
}
