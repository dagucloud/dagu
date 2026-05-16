// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  ReactNode,
} from 'react';
import { mutate as globalMutate } from 'swr';
import { AppBarContext } from './AppBarContext';
import { useConfig } from './ConfigContext';
import { components, UserRole } from '@/api/v1/schema';
import { sseManager } from '@/hooks/SSEManager';
import {
  TOKEN_KEY,
  addAuthSessionListener,
  clearAuthSession,
  getAuthExpiresAt,
  getAuthToken,
  setAuthSession,
} from '@/lib/authSession';
import {
  effectiveWorkspaceRole,
  roleAtLeast,
  workspaceRoleTarget,
} from '@/lib/workspaceAccess';

export type { UserRole } from '@/api/v1/schema';

type User = components['schemas']['User'];

type SetupResult = {
  token: string;
  expiresAt?: string;
  user: User;
};

type AuthContextType = {
  user: User | null;
  token: string | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  setupRequired: boolean;
  login: (username: string, password: string) => Promise<void>;
  setup: (username: string, password: string) => Promise<SetupResult>;
  logout: () => void;
  refreshUser: () => Promise<void>;
  completeSetup: (result: SetupResult) => void;
};

const AuthContext = createContext<AuthContextType | null>(null);

export { TOKEN_KEY };

export function AuthProvider({ children }: { children: ReactNode }) {
  const config = useConfig();
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(() => getAuthToken());
  const [isLoading, setIsLoading] = useState(true);
  const [setupRequired, setSetupRequired] = useState(config.setupRequired);

  const clearLocalSession = useCallback(() => {
    setToken(null);
    setUser(null);
    sseManager.disposeAll();
    void globalMutate(() => true, undefined, { revalidate: false });
  }, []);

  const logout = useCallback(() => {
    clearAuthSession('logout');
    clearLocalSession();
  }, [clearLocalSession]);

  const refreshUser = useCallback(async () => {
    const storedToken = getAuthToken();
    if (!storedToken) {
      clearLocalSession();
      setIsLoading(false);
      return;
    }

    try {
      const response = await fetch(`${config.apiURL}/auth/me`, {
        headers: {
          Authorization: `Bearer ${storedToken}`,
        },
      });

      if (response.ok) {
        const data = await response.json();
        setUser(data.user);
        setToken(storedToken);
      } else if (response.status === 401 || response.status === 403) {
        clearAuthSession('unauthorized');
        clearLocalSession();
      } else {
        console.warn(
          'Failed to refresh authenticated user',
          response.status,
          response.statusText
        );
      }
    } catch (error) {
      console.warn('Failed to refresh authenticated user', error);
    } finally {
      setIsLoading(false);
    }
  }, [clearLocalSession, config.apiURL]);

  const login = useCallback(
    async (username: string, password: string) => {
      const response = await fetch(`${config.apiURL}/auth/login`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ username, password }),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Login failed');
      }

      const data = await response.json();
      setAuthSession(data.token, data.expiresAt, 'login');
      setToken(data.token);
      setUser(data.user);
    },
    [config.apiURL]
  );

  const setup = useCallback(
    async (username: string, password: string): Promise<SetupResult> => {
      const response = await fetch(
        `${config.apiURL}/auth/setup?remoteNode=local`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ username, password }),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        const err = new Error(data.message || 'Setup failed');
        (err as any).status = response.status;
        throw err;
      }

      const data = await response.json();
      return { token: data.token, expiresAt: data.expiresAt, user: data.user };
    },
    [config.apiURL]
  );

  const completeSetup = useCallback((result: SetupResult) => {
    setAuthSession(result.token, result.expiresAt, 'setup');
    setToken(result.token);
    setUser(result.user);
    setSetupRequired(false);
  }, []);

  useEffect(() => {
    return addAuthSessionListener((change) => {
      if (change.token) {
        setToken(change.token);
        setIsLoading(true);
        void refreshUser();
        return;
      }
      clearLocalSession();
      setIsLoading(false);
    });
  }, [clearLocalSession, refreshUser]);

  useEffect(() => {
    if (!token) {
      return;
    }
    const expiresAt = getAuthExpiresAt();
    if (!expiresAt) {
      return;
    }
    const delay = Date.parse(expiresAt) - Date.now();
    if (delay <= 0) {
      clearAuthSession('expired');
      return;
    }
    const timeout = window.setTimeout(() => {
      clearAuthSession('expired');
    }, delay);
    return () => window.clearTimeout(timeout);
  }, [token]);

  useEffect(() => {
    if (config.authMode === 'builtin') {
      refreshUser();
    } else {
      setIsLoading(false);
    }
  }, [config.authMode, refreshUser]);

  return (
    <AuthContext.Provider
      value={{
        user,
        token,
        isAuthenticated: !!user,
        isLoading,
        setupRequired,
        login,
        setup,
        completeSetup,
        logout,
        refreshUser,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}

export function useIsAdmin(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') return true;
  return user?.role === UserRole.admin;
}

export function useCanWrite(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const workspace = workspaceRoleTarget(appBarContext.workspaceSelection);
  if (config.authMode !== 'builtin') return config.permissions.writeDags;
  if (!user) return false;
  return roleAtLeast(
    effectiveWorkspaceRole(user, workspace),
    UserRole.developer
  );
}

export function useCanWriteForWorkspace(workspace?: string | null): boolean {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') return config.permissions.writeDags;
  if (!user) return false;
  return roleAtLeast(
    effectiveWorkspaceRole(user, workspace ?? ''),
    UserRole.developer
  );
}

export function useCanExecute(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const workspace = workspaceRoleTarget(appBarContext.workspaceSelection);
  if (config.authMode !== 'builtin') return config.permissions.runDags;
  if (!user) return false;
  return roleAtLeast(
    effectiveWorkspaceRole(user, workspace),
    UserRole.operator
  );
}

export function useCanExecuteForWorkspace(workspace?: string | null): boolean {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') return config.permissions.runDags;
  if (!user) return false;
  return roleAtLeast(
    effectiveWorkspaceRole(user, workspace ?? ''),
    UserRole.operator
  );
}

export function useCanAccessSystemStatus(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') return true;
  if (!user) return false;
  return roleAtLeast(user.role, UserRole.developer);
}

export function useCanManageWebhooks(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  // Webhooks require the builtin auth service (webhook token store).
  if (config.authMode !== 'builtin') return false;
  if (!user) return false;
  return roleAtLeast(user.role, UserRole.developer);
}

export function useCanViewAuditLogs(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') return true;
  if (!user) return false;
  return roleAtLeast(user.role, UserRole.manager);
}

function secretScopeRoleTarget(scope?: string | null): string {
  if (!scope || scope === 'global') return '';
  return scope;
}

export function useCanManageSecrets(scope?: string | null): boolean {
  const { user } = useAuth();
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const workspace =
    scope === undefined
      ? workspaceRoleTarget(appBarContext.workspaceSelection)
      : secretScopeRoleTarget(scope);
  if (config.authMode !== 'builtin') return true;
  if (!user) return false;
  return roleAtLeast(effectiveWorkspaceRole(user, workspace), UserRole.manager);
}

export function useCanViewEventLogs(): boolean {
  const { user } = useAuth();
  const config = useConfig();
  if (config.authMode !== 'builtin') return true;
  if (!user) return false;
  return roleAtLeast(user.role, UserRole.manager);
}

export function hasRole(userRole: UserRole, requiredRole: UserRole): boolean {
  return roleAtLeast(userRole, requiredRole);
}
