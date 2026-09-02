// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, UserRole } from '@/api/v1/schema';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { I18nText } from '@/i18n/I18nText';

type WorkspaceAccess = components['schemas']['WorkspaceAccess'];
type WorkspaceGrant = components['schemas']['WorkspaceGrant'];
type Workspace = components['schemas']['WorkspaceResponse'];

const GRANT_ROLES = [
  { value: UserRole.manager, label: 'Manager' },
  { value: UserRole.developer, label: 'Developer' },
  { value: UserRole.operator, label: 'Operator' },
  { value: UserRole.viewer, label: 'Viewer' },
] as const;

export function defaultWorkspaceAccess(): WorkspaceAccess {
  return { all: true, grants: [] };
}

export function emptyWorkspaceAccess(): WorkspaceAccess {
  return { all: false, grants: [] };
}

export function normalizeWorkspaceAccess(
  access?: WorkspaceAccess
): WorkspaceAccess {
  if (!access || access.all) {
    return defaultWorkspaceAccess();
  }
  return {
    all: false,
    grants: [...access.grants].sort((a, b) =>
      a.workspace.localeCompare(b.workspace)
    ),
  };
}

export function WorkspaceAccessSummary({
  value,
  workspaces,
}: {
  value: WorkspaceAccess;
  workspaces?: Workspace[];
}) {
  const access = normalizeWorkspaceAccess(value);

  if (access.all) {
    return (
      <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm">
        <I18nText text={'All workspaces'} />
      </div>
    );
  }

  if (access.grants.length === 0) {
    return (
      <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
        <I18nText text={'No named workspace grants'} />
      </div>
    );
  }

  const knownWorkspaces = new Set(
    workspaces?.map((workspace) => workspace.name)
  );

  return (
    <div className="max-h-56 overflow-auto rounded-md border border-border">
      {access.grants.map((grant) => {
        const unavailable =
          workspaces !== undefined && !knownWorkspaces.has(grant.workspace);
        return (
          <div
            key={grant.workspace}
            className="flex items-center justify-between gap-3 border-b border-border px-3 py-2 text-sm last:border-b-0"
          >
            <div className="min-w-0">
              <div className="truncate">{grant.workspace}</div>
              {unavailable && (
                <div className="text-xs text-muted-foreground">
                  <I18nText text={'Workspace not currently available'} />
                </div>
              )}
            </div>
            <span className="rounded bg-muted px-1.5 py-0.5 text-xs capitalize text-muted-foreground">
              <I18nText
                text={
                  GRANT_ROLES.find((role) => role.value === grant.role)
                    ?.label ?? grant.role
                }
              />
            </span>
          </div>
        );
      })}
    </div>
  );
}

export function WorkspaceAccessEditor({
  value,
  onChange,
  workspaces,
}: {
  value: WorkspaceAccess;
  onChange: (next: WorkspaceAccess) => void;
  workspaces: Workspace[];
}) {
  const selected = new Map(
    value.grants.map((grant) => [grant.workspace, grant])
  );

  const setMode = (mode: string) => {
    if (mode === 'all') {
      onChange(defaultWorkspaceAccess());
      return;
    }
    onChange({ all: false, grants: value.grants });
  };

  const setGrant = (workspace: string, checked: boolean) => {
    if (!checked) {
      onChange({
        all: false,
        grants: value.grants.filter((grant) => grant.workspace !== workspace),
      });
      return;
    }
    onChange({
      all: false,
      grants: [...value.grants, { workspace, role: UserRole.viewer }].sort(
        (a, b) => a.workspace.localeCompare(b.workspace)
      ),
    });
  };

  const setGrantRole = (workspace: string, role: UserRole) => {
    const grants = value.grants.map(
      (grant): WorkspaceGrant =>
        grant.workspace === workspace ? { ...grant, role } : grant
    );
    onChange({ all: false, grants });
  };

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label className="text-sm">
          <I18nText text={'Workspace Access'} />
        </Label>
        <Select value={value.all ? 'all' : 'scoped'} onValueChange={setMode}>
          <SelectTrigger className="h-7">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">
              <I18nText text={'All workspaces'} />
            </SelectItem>
            <SelectItem value="scoped">
              <I18nText text={'Selected workspaces'} />
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      {!value.all && (
        <div className="max-h-56 overflow-auto rounded-md border border-border">
          {workspaces.length === 0 ? (
            <div className="px-3 py-2 text-sm text-muted-foreground">
              <I18nText text={'No workspaces available'} />
            </div>
          ) : (
            workspaces.map((workspace) => {
              const grant = selected.get(workspace.name);
              return (
                <div
                  key={workspace.id}
                  className="grid grid-cols-[1fr_132px] items-center gap-3 border-b border-border px-3 py-2 last:border-b-0"
                >
                  <label className="flex min-w-0 items-center gap-2 text-sm">
                    <Checkbox
                      checked={!!grant}
                      onCheckedChange={(checked) =>
                        setGrant(workspace.name, checked === true)
                      }
                    />
                    <span className="truncate">{workspace.name}</span>
                  </label>
                  <Select
                    value={grant?.role ?? UserRole.viewer}
                    onValueChange={(role) =>
                      setGrantRole(workspace.name, role as UserRole)
                    }
                    disabled={!grant}
                  >
                    <SelectTrigger className="h-7 text-xs">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {GRANT_ROLES.map((role) => (
                        <SelectItem key={role.value} value={role.value}>
                          <I18nText text={role.label} />
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}
