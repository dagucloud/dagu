// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, useEffect, useContext } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { components, UserAuthProvider, UserRole } from '@/api/v1/schema';
import {
  defaultWorkspaceAccess,
  emptyWorkspaceAccess,
  normalizeWorkspaceAccess,
  WorkspaceAccessEditor,
  WorkspaceAccessSummary,
} from '@/components/WorkspaceAccessEditor';
import { Badge } from '@/components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { AlertCircle, Check, UserPlus, X } from 'lucide-react';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type User = components['schemas']['User'];
type WorkspaceAccess = components['schemas']['WorkspaceAccess'];

type UserFormModalProps = {
  open: boolean;
  user?: User;
  managedRoleProviders?: UserAuthProvider[];
  managedWorkspaceAccessProviders?: UserAuthProvider[];
  onClose: () => void;
  onSuccess: () => void;
};

const ROLES = [
  {
    value: 'admin',
    label: 'Admin',
    description: 'Full access including user management',
  },
  {
    value: 'manager',
    label: 'Manager',
    description: 'DAG create/edit/delete, execution, and audit logs',
  },
  {
    value: 'developer',
    label: 'Developer',
    description: 'DAG create/edit/delete and execution',
  },
  { value: 'operator', label: 'Operator', description: 'DAG execution only' },
  { value: 'viewer', label: 'Viewer', description: 'Read-only access' },
] as const;

/**
 * Render a modal dialog that provides a form to create a new user or edit an existing one.
 *
 * @param props.open - Whether the modal is open.
 * @param props.user - Existing user to edit; when undefined the form operates in create mode.
 * @param props.onClose - Callback invoked when the modal is closed.
 * @param props.onSuccess - Callback invoked after a successful create or update operation.
 * @returns The modal JSX element containing the user form.
 */
export function UserFormModal({
  open,
  user,
  managedRoleProviders = [],
  managedWorkspaceAccessProviders = [],
  onClose,
  onSuccess,
}: UserFormModalProps) {
  const { ts } = useI18n();
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const isEditing = !!user;
  const roleManaged =
    isEditing &&
    !!user.authProvider &&
    managedRoleProviders.includes(user.authProvider);
  const workspaceAccessManaged =
    isEditing &&
    !!user.authProvider &&
    managedWorkspaceAccessProviders.includes(user.authProvider);
  const authorizationManaged = roleManaged || workspaceAccessManaged;
  const managedProviderLabel =
    user?.authProvider === UserAuthProvider.proxy ? 'Proxy' : 'SSO';
  let managedBadgeLabel = ts('Workspace access managed by {provider}', {
    provider: managedProviderLabel,
  });
  if (roleManaged) {
    managedBadgeLabel = workspaceAccessManaged
      ? ts('Managed by {provider}', { provider: managedProviderLabel })
      : ts('Role managed by {provider}', { provider: managedProviderLabel });
  }

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<string>('viewer');
  const [workspaceAccess, setWorkspaceAccess] = useState<WorkspaceAccess>(
    defaultWorkspaceAccess()
  );
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const effectiveRole = workspaceAccess.all ? role : UserRole.viewer;

  useEffect(() => {
    if (user) {
      setUsername(user.username);
      setRole(user.role);
      setWorkspaceAccess(normalizeWorkspaceAccess(user.workspaceAccess));
      setPassword('');
    } else {
      setUsername('');
      setPassword('');
      setRole('viewer');
      setWorkspaceAccess(emptyWorkspaceAccess());
    }
    setError(null);
  }, [user, open]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    if (!isEditing && password.length < 8) {
      setError('Password must be at least 8 characters');
      return;
    }
    if (
      !workspaceAccessManaged &&
      !workspaceAccess.all &&
      workspaceAccess.grants.length === 0
    ) {
      setError('Select at least one workspace');
      return;
    }

    setIsLoading(true);

    try {
      const token = localStorage.getItem('dagu_auth_token');
      if (!token) {
        throw new Error('Not authenticated');
      }
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const payload = {
        username,
        ...(!roleManaged && { role: effectiveRole }),
        ...(!workspaceAccessManaged && { workspaceAccess }),
      };
      const endpoint = user
        ? `${config.apiURL}/users/${user.id}?remoteNode=${remoteNode}`
        : `${config.apiURL}/users?remoteNode=${remoteNode}`;
      const response = await fetch(endpoint, {
        method: user ? 'PATCH' : 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify(user ? payload : { ...payload, password }),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(
          data.message || `Failed to ${user ? 'update' : 'create'} user`
        );
      }

      onSuccess();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Operation failed');
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <DialogTitle>
              {isEditing ? (
                <I18nText text={'Edit User'} />
              ) : (
                <I18nText text={'Create User'} />
              )}
            </DialogTitle>
            {authorizationManaged && (
              <Badge variant="info">{managedBadgeLabel}</Badge>
            )}
          </div>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          {error && (
            <div className="flex items-center gap-2 p-3 text-sm text-destructive bg-destructive/10 rounded-md">
              <AlertCircle className="h-4 w-4 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="username" className="text-sm">
              <I18nText text={'Username'} />
            </Label>
            <Input
              id="username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
              autoComplete="off"
              className="h-9"
            />
          </div>

          {!isEditing && (
            <div className="space-y-1.5">
              <Label htmlFor="password" className="text-sm">
                <I18nText text={'Password'} />
              </Label>
              <I18nProps>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  autoComplete="new-password"
                  className="h-9"
                  placeholder="Minimum 8 characters"
                />
              </I18nProps>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="role" className="text-sm">
              <I18nText text={'Role'} />
            </Label>
            {roleManaged ? (
              <div
                aria-label={ts('Role managed by {provider}', {
                  provider: managedProviderLabel,
                })}
                className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm capitalize"
              >
                <I18nText text={user.role} />
              </div>
            ) : (
              <Select
                value={effectiveRole}
                onValueChange={setRole}
                disabled={!workspaceAccess.all}
              >
                <SelectTrigger className="h-9">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ROLES.map((r) => (
                    <SelectItem key={r.value} value={r.value}>
                      <div className="flex flex-col">
                        <span>
                          <I18nText text={r.label} />
                        </span>
                        <span className="text-xs text-muted-foreground">
                          <I18nText text={r.description} />
                        </span>
                      </div>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {workspaceAccessManaged ? (
            <div className="space-y-1.5">
              <Label className="text-sm">
                <I18nText text={'Workspace Access'} />
              </Label>
              <WorkspaceAccessSummary
                value={workspaceAccess}
                workspaces={appBarContext.workspaces}
              />
              <p className="text-xs text-slate-500 dark:text-slate-500">
                {ts(
                  roleManaged
                    ? 'Role and workspace access are updated by {provider} at sign-in.'
                    : 'Workspace access is updated by {provider} at sign-in.',
                  { provider: managedProviderLabel }
                )}
              </p>
            </div>
          ) : (
            <WorkspaceAccessEditor
              value={workspaceAccess}
              onChange={(next) => {
                setWorkspaceAccess(next);
                if (!next.all) {
                  setRole(UserRole.viewer);
                }
              }}
              workspaces={appBarContext.workspaces ?? []}
            />
          )}

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              <X className="h-4 w-4" />
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isLoading}>
              {isEditing ? (
                <Check className="h-4 w-4" />
              ) : (
                <UserPlus className="h-4 w-4" />
              )}
              {isLoading ? (
                <I18nText text={'Saving...'} />
              ) : isEditing ? (
                <I18nText text={'Update'} />
              ) : (
                <I18nText text={'Create'} />
              )}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
