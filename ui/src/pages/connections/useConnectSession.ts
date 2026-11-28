// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ConnectionAuthorizationPhase,
  ConnectionRedirectMode,
  type components,
} from '@/api/v1/schema';
import { useClient } from '@/hooks/api';
import { subscribeClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { LOCAL_NODE } from './shared';

export type ConnectStart =
  components['schemas']['ConnectionAuthorizationStart'];
export type ConnectState = components['schemas']['ConnectionAuthorization'];

// Polled only while a server callback session waits for the provider's
// redirect, for at most the session's 10 minutes.
const POLL_INTERVAL_MS = 2000;

const POPUP_FEATURES = 'popup,width=520,height=680';

function messageOf(error: unknown, fallback: string): string {
  const message = (error as { message?: unknown } | undefined)?.message;
  return typeof message === 'string' && message !== '' ? message : fallback;
}

/**
 * Drives one connect or reconnect session of a connection: starts it with
 * the consent popup, follows it until the account is ready to confirm, and
 * confirms or cancels it. `start` opens the popup before any request, so the
 * browser does not block it. A server callback session is polled while it
 * waits; a paste session waits for `complete` with the address the browser
 * ended on.
 */
export function useConnectSession(name: string, workspace: string) {
  const client = useClient();
  const { ts } = useI18n();
  const [session, setSession] = useState<ConnectStart | null>(null);
  const [state, setState] = useState<ConnectState | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [popupBlocked, setPopupBlocked] = useState(false);
  // The session whose answers the hook still takes. A reset or a new start
  // drops the answers of the one before.
  const current = useRef<string | null>(null);

  const params = useMemo(
    () => ({ path: { name }, query: { workspace, remoteNode: LOCAL_NODE } }),
    [name, workspace]
  );
  const sessionParams = useCallback(
    (handle: string) => ({
      path: params.path,
      query: { ...params.query, session: handle },
    }),
    [params]
  );

  const read = useCallback(
    async (handle: string) => {
      const { data } = await client.GET('/connections/{name}/authorization', {
        params: sessionParams(handle),
      });
      if (data && current.current === handle) setState(data);
    },
    [client, sessionParams]
  );

  const waiting =
    session?.mode === ConnectionRedirectMode.server_callback &&
    state?.phase === ConnectionAuthorizationPhase.waiting;
  const handle = session?.session;
  useEffect(() => {
    if (!waiting || !handle) return;
    return subscribeClock(POLL_INTERVAL_MS, () => {
      read(handle).catch(() => {
        // A failed poll is retried on the next tick.
      });
    });
  }, [waiting, handle, read]);

  const start = useCallback(async () => {
    // Opened before any await so the browser treats it as a user action.
    const popup = window.open('about:blank', '_blank', POPUP_FEATURES);
    if (popup) popup.opener = null;
    setBusy(true);
    setError(null);
    setPopupBlocked(false);
    current.current = null;
    setSession(null);
    setState(null);
    try {
      const { data, error: apiError } = await client.POST(
        '/connections/{name}/authorization',
        { params, body: {} }
      );
      if (apiError || !data) {
        throw new Error(messageOf(apiError, ts('Could not start signing in.')));
      }
      current.current = data.session;
      setSession(data);
      setState({ phase: ConnectionAuthorizationPhase.waiting });
      if (popup) {
        popup.location.href = data.authorizationUrl;
      } else {
        setPopupBlocked(true);
      }
    } catch (err) {
      popup?.close();
      setError(messageOf(err, ts('Could not start signing in.')));
    } finally {
      setBusy(false);
    }
  }, [client, params, ts]);

  const complete = useCallback(
    async (redirectUrl: string) => {
      if (!session) return;
      setBusy(true);
      setError(null);
      try {
        const { error: apiError } = await client.POST(
          '/connections/{name}/authorization/complete',
          { params, body: { redirectUrl } }
        );
        // A refused consent fails the session, which the read reports.
        if (apiError) setError(messageOf(apiError, ts('Could not sign in.')));
        await read(session.session);
      } catch (err) {
        setError(messageOf(err, ts('Could not sign in.')));
      } finally {
        setBusy(false);
      }
    },
    [client, params, session, read, ts]
  );

  const confirm = useCallback(async () => {
    if (!session) return false;
    setBusy(true);
    setError(null);
    try {
      const { error: apiError } = await client.POST(
        '/connections/{name}/authorization/confirm',
        { params, body: { session: session.session } }
      );
      if (apiError) {
        throw new Error(messageOf(apiError, ts('Could not confirm.')));
      }
      setState({ phase: ConnectionAuthorizationPhase.connected });
      return true;
    } catch (err) {
      setError(messageOf(err, ts('Could not confirm.')));
      return false;
    } finally {
      setBusy(false);
    }
  }, [client, params, session, ts]);

  // Ends a session that is still in progress, so its tokens never go live,
  // and forgets it.
  const reset = useCallback(() => {
    const phase = state?.phase;
    if (
      session &&
      (phase === ConnectionAuthorizationPhase.waiting ||
        phase === ConnectionAuthorizationPhase.confirm)
    ) {
      void client
        .DELETE('/connections/{name}/authorization', {
          params: sessionParams(session.session),
        })
        .catch(() => {
          // The session expires on its own.
        });
    }
    current.current = null;
    setSession(null);
    setState(null);
    setError(null);
    setPopupBlocked(false);
  }, [client, session, state, sessionParams]);

  return {
    session,
    state,
    busy,
    error,
    popupBlocked,
    start,
    complete,
    confirm,
    reset,
  };
}
