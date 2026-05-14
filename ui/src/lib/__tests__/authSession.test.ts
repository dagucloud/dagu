import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AUTH_SESSION_CHANGED_EVENT,
  AUTH_TOKEN_EXPIRES_AT_KEY,
  TOKEN_KEY,
  addAuthSessionListener,
  clearAuthSession,
  getAuthToken,
  handleAuthResponse,
  setAuthSession,
} from '../authSession';

function base64URL(value: string): string {
  return btoa(value)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

function createJWT(payload: Record<string, unknown>): string {
  return `header.${base64URL(JSON.stringify(payload))}.signature`;
}

describe('authSession', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal('getConfig', () => ({ authMode: 'builtin' }));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('clears the stored token when an authenticated request returns 401', () => {
    const events: Array<{ token: string | null; reason?: string }> = [];
    window.addEventListener(AUTH_SESSION_CHANGED_EVENT, (event) => {
      events.push((event as CustomEvent).detail);
    });

    setAuthSession('token-1', new Date(Date.now() + 60_000).toISOString());

    handleAuthResponse(new Response(null, { status: 401 }));

    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(localStorage.getItem(AUTH_TOKEN_EXPIRES_AT_KEY)).toBeNull();
    expect(events[events.length - 1]).toMatchObject({
      token: null,
      reason: 'unauthorized',
    });
  });

  it('expires a locally stale token before it can be reused', () => {
    const events: Array<{ token: string | null; reason?: string }> = [];
    window.addEventListener(AUTH_SESSION_CHANGED_EVENT, (event) => {
      events.push((event as CustomEvent).detail);
    });

    localStorage.setItem(TOKEN_KEY, 'expired-token');
    localStorage.setItem(
      AUTH_TOKEN_EXPIRES_AT_KEY,
      new Date(Date.now() - 1000).toISOString()
    );

    expect(getAuthToken()).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(events[events.length - 1]).toMatchObject({
      token: null,
      reason: 'expired',
    });
  });

  it('ignores login failures when there is no active token to invalidate', () => {
    const listener = vi.fn();
    window.addEventListener(AUTH_SESSION_CHANGED_EVENT, listener);

    handleAuthResponse(new Response(null, { status: 401 }));

    expect(listener).not.toHaveBeenCalled();
  });

  it('decodes the JWT exp claim when an explicit expiry is omitted', () => {
    const expiresAtSeconds = Math.floor((Date.now() + 60_000) / 1000);
    const token = createJWT({ exp: expiresAtSeconds });

    setAuthSession(token);

    expect(localStorage.getItem(AUTH_TOKEN_EXPIRES_AT_KEY)).toBe(
      new Date(expiresAtSeconds * 1000).toISOString()
    );
  });

  it('keeps tokens on 401 responses for non-builtin auth modes', () => {
    vi.stubGlobal('getConfig', () => ({ authMode: 'oidc' }));
    setAuthSession('token-1', new Date(Date.now() + 60_000).toISOString());

    handleAuthResponse(new Response(null, { status: 401 }));

    expect(localStorage.getItem(TOKEN_KEY)).toBe('token-1');
  });

  it('removes auth session listeners after unsubscribe', () => {
    const listener = vi.fn();
    const unsubscribe = addAuthSessionListener(listener);

    setAuthSession('token-1', new Date(Date.now() + 60_000).toISOString());
    expect(listener).toHaveBeenCalledTimes(1);

    listener.mockClear();
    unsubscribe();
    setAuthSession('token-2', new Date(Date.now() + 60_000).toISOString());

    expect(listener).not.toHaveBeenCalled();
  });

  it('ignores malformed JWT expiry payloads', () => {
    const tokens = [
      'not-a-jwt',
      'header..signature',
      'header.%%%%.signature',
      createJWT({ sub: 'user-1' }),
    ];

    for (const token of tokens) {
      localStorage.clear();

      setAuthSession(token);

      expect(localStorage.getItem(AUTH_TOKEN_EXPIRES_AT_KEY)).toBeNull();
    }
  });

  it('notifies explicit logout even when storage is already clear', () => {
    const listener = vi.fn();
    const unsubscribe = addAuthSessionListener(listener);

    clearAuthSession('logout');

    expect(listener).toHaveBeenCalledWith({
      token: null,
      expiresAt: null,
      reason: 'logout',
    });
    unsubscribe();
  });
});
