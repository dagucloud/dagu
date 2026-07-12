// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertCircle, WifiOff } from 'lucide-react';
import React from 'react';
import { SWRConfig } from 'swr';

// Suppress repeats of the same error message within this window.
const ERROR_COOLDOWN_MS = 30_000;
const NOTICE_TTL_MS = 6_000;
const MAX_NOTICES = 3;

type Notice = {
  id: number;
  message: string;
};

function getErrorStatus(error: unknown): number | undefined {
  const err = error as { status?: number; response?: { status?: number } };
  return err?.status ?? err?.response?.status;
}

function getErrorCode(error: unknown): string | undefined {
  const code = (error as { code?: unknown })?.code;
  return typeof code === 'string' ? code : undefined;
}

function getErrorMessage(error: unknown): string {
  const message = (error as { message?: string })?.message;
  return typeof message === 'string' && message.length > 0
    ? message
    : 'Request failed';
}

function isAbortLike(error: unknown): boolean {
  const name = (error as { name?: string })?.name;
  return name === 'AbortError' || name === 'RequestAbortError';
}

// API error codes that are expected states, not failures worth a notice:
// auth errors trigger the login redirect, and missing resources (e.g. a step
// log that has not been written yet) are rendered by the requesting component.
const IGNORED_ERROR_CODES = new Set([
  'not_found',
  'unauthorized',
  'auth.unauthorized',
]);

/**
 * Reports whether a query error represents an expected condition that should
 * not be surfaced as a global notice. Handles both error shapes seen in the
 * app: `FetchError` carrying an HTTP response, and the parsed API error body
 * (`{code, message}`) thrown by the typed query hooks.
 */
export function isIgnorableQueryError(error: unknown): boolean {
  if (isAbortLike(error)) return true;
  const status = getErrorStatus(error);
  if (status === 401 || status === 404) return true;
  const code = getErrorCode(error);
  return code !== undefined && IGNORED_ERROR_CODES.has(code);
}

/**
 * Surfaces background query failures and connectivity loss. Wraps children in
 * an SWRConfig that reports fetch errors as unobtrusive corner notices, and
 * shows a banner while the browser is offline.
 */
export function QueryFeedback({ children }: { children: React.ReactNode }) {
  const [notices, setNotices] = React.useState<Notice[]>([]);
  const [isOffline, setIsOffline] = React.useState(
    typeof navigator !== 'undefined' && !navigator.onLine
  );
  const lastShownRef = React.useRef<Map<string, number>>(new Map());
  const idRef = React.useRef(0);
  const timeoutsRef = React.useRef<Set<ReturnType<typeof setTimeout>>>(
    new Set()
  );

  React.useEffect(() => {
    const timeouts = timeoutsRef.current;
    return () => {
      timeouts.forEach(clearTimeout);
      timeouts.clear();
    };
  }, []);

  React.useEffect(() => {
    const handleOnline = () => setIsOffline(false);
    const handleOffline = () => setIsOffline(true);
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    return () => {
      window.removeEventListener('online', handleOnline);
      window.removeEventListener('offline', handleOffline);
    };
  }, []);

  const handleError = React.useCallback((error: unknown) => {
    console.error(error);

    if (isIgnorableQueryError(error)) return;

    const message = getErrorMessage(error);
    const now = Date.now();
    const lastShown = lastShownRef.current.get(message);
    if (lastShown && now - lastShown < ERROR_COOLDOWN_MS) return;
    lastShownRef.current.set(message, now);

    const id = ++idRef.current;
    setNotices((current) => [
      ...current.slice(-(MAX_NOTICES - 1)),
      { id, message },
    ]);
    const timeout = setTimeout(() => {
      timeoutsRef.current.delete(timeout);
      setNotices((current) => current.filter((notice) => notice.id !== id));
    }, NOTICE_TTL_MS);
    timeoutsRef.current.add(timeout);
  }, []);

  return (
    <SWRConfig value={{ onError: handleError }}>
      {children}

      {isOffline && (
        <div className="fixed top-2 left-1/2 -translate-x-1/2 z-[120] flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive px-3 py-1.5 text-xs font-medium text-destructive-foreground shadow-md">
          <WifiOff className="h-3.5 w-3.5" />
          You are offline — data may be stale
        </div>
      )}

      {notices.length > 0 && (
        <div className="fixed bottom-3 right-3 z-[110] flex flex-col gap-2">
          {notices.map((notice) => (
            <div
              key={notice.id}
              className="flex max-w-sm items-start gap-2 rounded-md border border-destructive/40 bg-card px-3 py-2 text-xs shadow-md"
            >
              <AlertCircle className="h-3.5 w-3.5 flex-shrink-0 text-destructive" />
              <div className="min-w-0">
                <div className="break-words text-foreground">
                  {notice.message}
                </div>
                <div className="text-muted-foreground">
                  Background request failed
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </SWRConfig>
  );
}

export default QueryFeedback;
