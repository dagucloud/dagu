// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Admin reads and changes of worker invites.
 *
 * @module features/workers/hooks
 */
import { useCallback } from 'react';
import { useSWRConfig } from 'swr';
import { ErrorCode } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from '@/hooks/api';
import { getQueryErrorCode, getQueryErrorMessage } from '@/lib/queryErrors';

/** A refused worker admin request, with its HTTP status. */
export class WorkerRequestError extends Error {
  constructor(
    readonly status: number,
    message: string
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
        throw new WorkerRequestError(response.status, errorMessage(error));
      }
    },
    [client]
  );

  return { deleteInvite, reread };
}

function errorMessage(error: unknown): string {
  return getQueryErrorMessage(error, '');
}

/**
 * The cause of a failed request and the change that fixes it. A 409 and a
 * 503 carry the coordinator's or the server's reason; the messages of Spec
 * 108 that name no change get one here.
 */
export function describeWorkerError(
  error: unknown,
  advice: { notFound: WorkerErrorAdvice },
  ts: (source: string) => string
): WorkerErrorAdvice {
  const status =
    error instanceof WorkerRequestError
      ? error.status
      : statusOfCode(getQueryErrorCode(error));
  const message = getQueryErrorMessage(error, '');
  const namedChange = ts('Nothing changed. Make the change the message names.');
  switch (status) {
    case 403:
      return {
        cause: ts('Only admins can manage workers and invites.'),
        change: ts('Sign in with an admin account, or ask an admin.'),
      };
    case 404:
      return advice.notFound;
    case 409:
      if (message.includes('peer.cert_file')) {
        return {
          cause: message,
          change: ts(
            'To use invites, remove peer.cert_file and peer.key_file from the coordinator config, then restart it.'
          ),
        };
      }
      return {
        cause: message || ts('The coordinator refused the request.'),
        change: namedChange,
      };
    case 503:
      // The server names the coordinator's reason after this prefix.
      return message.startsWith('the coordinator refused the server')
        ? { cause: message, change: namedChange }
        : {
            cause: message || ts('No coordinator is running or reachable.'),
            change: ts(
              'Start the coordinator, or check that this server can reach it, then try again.'
            ),
          };
    default:
      return {
        cause: message || ts('The request failed.'),
        change: ts('Please try again or check the server connection.'),
      };
  }
}

// A list read rejects with the error body, which carries a code and no
// status. The API answers 409 and 503 with these codes.
function statusOfCode(code: string | undefined): number {
  switch (code) {
    case ErrorCode.forbidden:
      return 403;
    case ErrorCode.not_found:
      return 404;
    case ErrorCode.conflict:
      return 409;
    case ErrorCode.internal_error:
      return 503;
    default:
      return 0;
  }
}
