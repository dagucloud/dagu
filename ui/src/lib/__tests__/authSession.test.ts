import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AUTH_SESSION_CHANGED_EVENT,
  AUTH_TOKEN_EXPIRES_AT_KEY,
  TOKEN_KEY,
  getAuthToken,
  handleAuthResponse,
  setAuthSession,
} from '../authSession';

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
});
