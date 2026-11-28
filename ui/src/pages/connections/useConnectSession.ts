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
// redirect, until the session's expiresAt at the latest.
const POLL_INTERVAL_MS = 2000;

// The reason the server gives a session that outlived its expiry.
const EXPIRED_REASON = 'expired';

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
  // Advances on each start, reset, and unmount. A start whose request
  // returns under a later generation cancels the session it created.
  const generation = useRef(0);
  // The session still in progress, which a reset or an unmount cancels.
  const inProgress = useRef<string | null>(null);

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

  const cancel = useCallback(
    (handle: string) => {
      void client
        .DELETE('/connections/{name}/authorization', {
          params: sessionParams(handle),
        })
        .catch(() => {
          // The session expires on its own.
        });
    },
    [client, sessionParams]
  );

  // Reads the session's state. A refusal, such as a 404 once the connection
  // is removed, ends the session here, since no later read can succeed.
  const read = useCallback(
    async (handle: string) => {
      const { data, error, response } = await client.GET(
        '/connections/{name}/authorization',
        { params: sessionParams(handle) }
      );
      if (current.current !== handle) return;
      if (data) {
        setState(data);
      } else if (response.status >= 400 && response.status < 500) {
        setState({
          phase: ConnectionAuthorizationPhase.failed,
          reason: messageOf(error, ts('Could not sign in.')),
        });
      }
    },
    [client, sessionParams, ts]
  );

  const phase = state?.phase;
  const handle = session?.session;
  const active =
    phase === ConnectionAuthorizationPhase.waiting ||
    phase === ConnectionAuthorizationPhase.confirm;
  useEffect(() => {
    inProgress.current = active && handle ? handle : null;
  }, [active, handle]);

  const cancelRef = useRef(cancel);
  useEffect(() => {
    cancelRef.current = cancel;
  }, [cancel]);
  useEffect(
    () => () => {
      generation.current += 1;
      if (inProgress.current) cancelRef.current(inProgress.current);
    },
    []
  );

  const polling =
    session?.mode === ConnectionRedirectMode.server_callback &&
    phase === ConnectionAuthorizationPhase.waiting;
  const expiresAt = session?.expiresAt;
  useEffect(() => {
    if (!polling || !handle || !expiresAt) return;
    const deadline = Date.parse(expiresAt);
    // One read at a time, so an older answer never replaces a newer one.
    let reading = false;
    return subscribeClock(POLL_INTERVAL_MS, () => {
      // The phase change ends this subscription.
      if (Date.now() >= deadline) {
        setState({
          phase: ConnectionAuthorizationPhase.failed,
          reason: EXPIRED_REASON,
        });
        return;
      }
      if (reading) return;
      reading = true;
      read(handle)
        .catch(() => {
          // A failed request is retried on the next tick.
        })
        .finally(() => {
          reading = false;
        });
    });
  }, [polling, handle, expiresAt, read]);

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
    generation.current += 1;
    const started = generation.current;
    try {
      const { data, error: apiError } = await client.POST(
        '/connections/{name}/authorization',
        { params, body: {} }
      );
      if (started !== generation.current) {
        // Closed while the request was in flight.
        popup?.close();
        if (data) cancel(data.session);
        return;
      }
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
      if (started === generation.current) {
        setError(messageOf(err, ts('Could not start signing in.')));
      }
    } finally {
      if (started === generation.current) setBusy(false);
    }
  }, [client, params, cancel, ts]);

  const complete = useCallback(
    async (redirectUrl: string) => {
      if (!session) return;
      const started = generation.current;
      setBusy(true);
      setError(null);
      try {
        const { error: apiError } = await client.POST(
          '/connections/{name}/authorization/complete',
          { params, body: { redirectUrl } }
        );
        if (started !== generation.current) return;
        // A refused consent fails the session, which the read reports.
        if (apiError) setError(messageOf(apiError, ts('Could not sign in.')));
        await read(session.session);
      } catch (err) {
        if (started === generation.current) {
          setError(messageOf(err, ts('Could not sign in.')));
        }
      } finally {
        if (started === generation.current) setBusy(false);
      }
    },
    [client, params, session, read, ts]
  );

  const confirm = useCallback(async () => {
    if (!session) return false;
    const started = generation.current;
    setBusy(true);
    setError(null);
    try {
      const { error: apiError } = await client.POST(
        '/connections/{name}/authorization/confirm',
        { params, body: { session: session.session } }
      );
      // A reset or a new start while the request ran owns the dialog now.
      if (started !== generation.current) return false;
      if (apiError) {
        throw new Error(messageOf(apiError, ts('Could not confirm.')));
      }
      setState({ phase: ConnectionAuthorizationPhase.connected });
      return true;
    } catch (err) {
      if (started !== generation.current) return false;
      setError(messageOf(err, ts('Could not confirm.')));
      // A session that ended, such as one past its expiry, then reads as
      // failed, which offers another start.
      await read(session.session).catch(() => {});
      return false;
    } finally {
      if (started === generation.current) setBusy(false);
    }
  }, [client, params, session, read, ts]);

  // Ends a session that is still in progress, so its tokens never go live,
  // and forgets it.
  const reset = useCallback(() => {
    generation.current += 1;
    if (inProgress.current) cancel(inProgress.current);
    inProgress.current = null;
    current.current = null;
    setSession(null);
    setState(null);
    setError(null);
    setPopupBlocked(false);
    setBusy(false);
  }, [cancel]);

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
