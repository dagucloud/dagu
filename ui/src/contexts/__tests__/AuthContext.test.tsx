// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { UserRole } from '@/api/v1/schema';
import {
  AuthProvider,
  TOKEN_KEY,
  useAuth,
  useCanExecute,
  useCanExecuteForWorkspace,
} from '../AuthContext';
import { type Config, ConfigContext } from '../ConfigContext';

function renderExecuteChecks(runDags: boolean) {
  const config = {
    apiURL: '/api/v1',
    authMode: 'builtin',
    setupRequired: false,
    permissions: { writeDags: true, runDags },
  } as unknown as Config;
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <ConfigContext.Provider value={config}>
      <AuthProvider>{children}</AuthProvider>
    </ConfigContext.Provider>
  );
  return renderHook(
    () => ({
      user: useAuth().user,
      canExecute: useCanExecute(),
      canExecuteForWorkspace: useCanExecuteForWorkspace(''),
    }),
    { wrapper }
  );
}

describe('execute permission hooks', () => {
  afterEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  // The server refuses runs when the run_dags permission is off, whatever
  // the user's role, so execute controls must follow it in builtin auth too.
  it.each([
    { runDags: true, expected: true },
    { runDags: false, expected: false },
  ])(
    'returns $expected for an operator when runDags is $runDags',
    async ({ runDags, expected }) => {
      localStorage.setItem(TOKEN_KEY, 'token');
      vi.stubGlobal(
        'fetch',
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              user: { id: 'user-1', username: 'op', role: UserRole.operator },
            }),
            { status: 200 }
          )
        )
      );

      const { result } = renderExecuteChecks(runDags);

      await waitFor(() => expect(result.current.user).not.toBeNull());
      expect(result.current.canExecute).toBe(expected);
      expect(result.current.canExecuteForWorkspace).toBe(expected);
    }
  );
});
