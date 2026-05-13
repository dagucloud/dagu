// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export const TOKEN_KEY = 'dagu_auth_token';
export const AUTH_TOKEN_EXPIRES_AT_KEY = 'dagu_auth_token_expires_at';
export const AUTH_SESSION_CHANGED_EVENT = 'dagu:auth-session-changed';

declare const getConfig: undefined | (() => { authMode?: string });

type AuthSessionReason =
  | 'login'
  | 'logout'
  | 'setup'
  | 'oidc'
  | 'expired'
  | 'unauthorized';

export type AuthSessionChange = {
  token: string | null;
  expiresAt: string | null;
  reason: AuthSessionReason;
};

function dispatchSessionChange(change: AuthSessionChange): void {
  window.dispatchEvent(
    new CustomEvent<AuthSessionChange>(AUTH_SESSION_CHANGED_EVENT, {
      detail: change,
    })
  );
}

function readRuntimeAuthMode(): string | undefined {
  try {
    if (typeof getConfig === 'function') {
      return getConfig().authMode;
    }
  } catch {
    return undefined;
  }
  return undefined;
}

export function isBuiltinAuthMode(): boolean {
  return readRuntimeAuthMode() === 'builtin';
}

function parseExpiresAt(value: string | null): number | null {
  if (!value) {
    return null;
  }
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? null : timestamp;
}

function base64URLDecode(value: string): string {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64.padEnd(
    base64.length + ((4 - (base64.length % 4)) % 4),
    '='
  );
  return globalThis.atob(padded);
}

function decodeJWTExpiresAt(token: string): string | null {
  const [, payload] = token.split('.');
  if (!payload) {
    return null;
  }

  try {
    const parsed = JSON.parse(base64URLDecode(payload)) as { exp?: unknown };
    if (typeof parsed.exp !== 'number') {
      return null;
    }
    return new Date(parsed.exp * 1000).toISOString();
  } catch {
    return null;
  }
}

export function setAuthSession(
  token: string,
  expiresAt?: string | null,
  reason: AuthSessionReason = 'login'
): void {
  const resolvedExpiresAt = expiresAt ?? decodeJWTExpiresAt(token);
  localStorage.setItem(TOKEN_KEY, token);
  if (resolvedExpiresAt) {
    localStorage.setItem(AUTH_TOKEN_EXPIRES_AT_KEY, resolvedExpiresAt);
  } else {
    localStorage.removeItem(AUTH_TOKEN_EXPIRES_AT_KEY);
  }
  dispatchSessionChange({
    token,
    expiresAt: resolvedExpiresAt,
    reason,
  });
}

export function clearAuthSession(
  reason: AuthSessionReason = 'logout'
): void {
  const hadToken = localStorage.getItem(TOKEN_KEY) !== null;
  const hadExpiresAt = localStorage.getItem(AUTH_TOKEN_EXPIRES_AT_KEY) !== null;
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(AUTH_TOKEN_EXPIRES_AT_KEY);
  if (hadToken || hadExpiresAt) {
    dispatchSessionChange({ token: null, expiresAt: null, reason });
  }
}

export function getAuthExpiresAt(): string | null {
  return localStorage.getItem(AUTH_TOKEN_EXPIRES_AT_KEY);
}

export function isAuthSessionExpired(now: number = Date.now()): boolean {
  const expiresAt = parseExpiresAt(getAuthExpiresAt());
  return expiresAt !== null && expiresAt <= now;
}

export function getAuthToken(): string | null {
  const token = localStorage.getItem(TOKEN_KEY);
  if (!token) {
    return null;
  }
  if (isAuthSessionExpired()) {
    clearAuthSession('expired');
    return null;
  }
  return token;
}

export function handleAuthResponse(response: Response): void {
  if (response.status !== 401 || !isBuiltinAuthMode()) {
    return;
  }
  if (localStorage.getItem(TOKEN_KEY)) {
    clearAuthSession('unauthorized');
  }
}

export function addAuthSessionListener(
  listener: (change: AuthSessionChange) => void
): () => void {
  const handler = (event: Event) => {
    listener((event as CustomEvent<AuthSessionChange>).detail);
  };
  window.addEventListener(AUTH_SESSION_CHANGED_EVENT, handler);
  return () => window.removeEventListener(AUTH_SESSION_CHANGED_EVENT, handler);
}
