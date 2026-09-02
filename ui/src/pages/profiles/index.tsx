// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  RuntimeProfileEntryKind,
  RuntimeProfileStatus,
} from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Tab, Tabs } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  useCanManageProfiles,
  useCanWriteForWorkspace,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import dayjs from '@/lib/dayjs';
import { workspaceNameForSelection } from '@/lib/workspace';
import { SecretRefsSection } from '@/pages/secrets';
import {
  Building2,
  Globe2,
  KeyRound,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  Power,
  SlidersHorizontal,
  Trash2,
} from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type RuntimeProfileResponse = components['schemas']['RuntimeProfileResponse'];
type InheritedRuntimeProfileResponse =
  components['schemas']['InheritedRuntimeProfileResponse'];
type RuntimeProfileEntryResponse =
  components['schemas']['RuntimeProfileEntryResponse'];
type ProfileAPIError = components['schemas']['Error'];
type APIClient = ReturnType<typeof useClient>;

const NO_WORKSPACE_DEFAULT_PROFILE_VALUE = '__none__';

type ProfileFormState = {
  name: string;
  description: string;
  protected: boolean;
};

type EntryDialogState = {
  target: ProfileEntryTarget;
  kind: RuntimeProfileEntryKind;
  entry?: RuntimeProfileEntryResponse;
};

type ProfileEntryTarget = {
  kind: 'profile' | 'global' | 'workspace';
  name: string;
  title: string;
  description?: string;
  protected: boolean;
  entries: RuntimeProfileEntryResponse[];
  canManage: boolean;
  actionKey: string;
  updatedAt?: string;
  workspaceName?: string;
  loadError?: string;
};

function initialProfileForm(): ProfileFormState {
  return {
    name: '',
    description: '',
    protected: false,
  };
}

function formFromProfile(profile: RuntimeProfileResponse): ProfileFormState {
  return {
    name: profile.name,
    description: profile.description || '',
    protected: profile.protected,
  };
}

function errorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'message' in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === 'string' && message.trim() !== '') {
      return message;
    }
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}

async function saveProfileTargetEntry(
  client: APIClient,
  target: ProfileEntryTarget,
  kind: RuntimeProfileEntryKind,
  key: string,
  value: string,
  remoteNode: string
): Promise<ProfileAPIError | undefined> {
  if (kind === RuntimeProfileEntryKind.secret) {
    switch (target.kind) {
      case 'global': {
        const { error } = await client.PUT('/profiles/_global/secrets/{key}', {
          params: { path: { key }, query: { remoteNode } },
          body: { value },
        });
        return error;
      }
      case 'workspace': {
        if (!target.workspaceName) {
          throw new Error('Workspace is required');
        }
        const { error } = await client.PUT(
          '/profiles/_workspaces/{workspaceName}/secrets/{key}',
          {
            params: {
              path: { workspaceName: target.workspaceName, key },
              query: { remoteNode },
            },
            body: { value },
          }
        );
        return error;
      }
      case 'profile':
      default: {
        const { error } = await client.PUT(
          '/profiles/{profileName}/secrets/{key}',
          {
            params: {
              path: { profileName: target.name, key },
              query: { remoteNode },
            },
            body: { value },
          }
        );
        return error;
      }
    }
  }

  switch (target.kind) {
    case 'global': {
      const { error } = await client.PUT('/profiles/_global/variables/{key}', {
        params: { path: { key }, query: { remoteNode } },
        body: { value },
      });
      return error;
    }
    case 'workspace': {
      if (!target.workspaceName) {
        throw new Error('Workspace is required');
      }
      const { error } = await client.PUT(
        '/profiles/_workspaces/{workspaceName}/variables/{key}',
        {
          params: {
            path: { workspaceName: target.workspaceName, key },
            query: { remoteNode },
          },
          body: { value },
        }
      );
      return error;
    }
    case 'profile':
    default: {
      const { error } = await client.PUT(
        '/profiles/{profileName}/variables/{key}',
        {
          params: {
            path: { profileName: target.name, key },
            query: { remoteNode },
          },
          body: { value },
        }
      );
      return error;
    }
  }
}

async function deleteProfileTargetEntry(
  client: APIClient,
  target: ProfileEntryTarget,
  key: string,
  remoteNode: string
): Promise<ProfileAPIError | undefined> {
  switch (target.kind) {
    case 'global': {
      const { error } = await client.DELETE('/profiles/_global/entries/{key}', {
        params: { path: { key }, query: { remoteNode } },
      });
      return error;
    }
    case 'workspace': {
      if (!target.workspaceName) {
        throw new Error('Workspace is required');
      }
      const { error } = await client.DELETE(
        '/profiles/_workspaces/{workspaceName}/entries/{key}',
        {
          params: {
            path: { workspaceName: target.workspaceName, key },
            query: { remoteNode },
          },
        }
      );
      return error;
    }
    case 'profile':
    default: {
      const { error } = await client.DELETE(
        '/profiles/{profileName}/entries/{key}',
        {
          params: {
            path: { profileName: target.name, key },
            query: { remoteNode },
          },
        }
      );
      return error;
    }
  }
}

function entryLabel(entry: RuntimeProfileEntryResponse): string {
  return entry.kind === RuntimeProfileEntryKind.secret ? 'Secret' : 'Variable';
}

function entryDialogTitle(isSecret: boolean, isEditing: boolean): string {
  if (isSecret) {
    return isEditing ? 'Rotate Secret' : 'Add Secret';
  }
  return isEditing ? 'Edit Variable' : 'Add Variable';
}

function targetFromProfile(
  profile: RuntimeProfileResponse,
  canManage: boolean
): ProfileEntryTarget {
  return {
    kind: 'profile',
    name: profile.name,
    title: profile.name,
    description: profile.description,
    protected: profile.protected,
    entries: profile.entries,
    canManage,
    actionKey: profile.name,
    updatedAt: profile.updatedAt,
  };
}

function targetFromGlobalDefaults(
  defaults: InheritedRuntimeProfileResponse | undefined,
  canManage: boolean,
  loadError?: string
): ProfileEntryTarget {
  return {
    kind: 'global',
    name: '_global',
    title: 'Global environment',
    description: defaults?.description,
    protected: true,
    entries: defaults?.entries || [],
    canManage: canManage && !loadError,
    actionKey: '_global',
    updatedAt: defaults?.updatedAt,
    loadError,
  };
}

function targetFromWorkspaceDefaults(
  workspaceName: string,
  defaults: InheritedRuntimeProfileResponse | undefined,
  canManage: boolean,
  loadError?: string
): ProfileEntryTarget {
  return {
    kind: 'workspace',
    name: `_workspaces/${workspaceName}`,
    title: 'Workspace environment',
    description: defaults?.description,
    protected: true,
    entries: defaults?.entries || [],
    canManage: canManage && !loadError,
    actionKey: `_workspaces/${workspaceName}`,
    updatedAt: defaults?.updatedAt,
    workspaceName,
    loadError,
  };
}

export default function ProfilesPage(): React.ReactNode {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const location = useLocation();
  const navigate = useNavigate();
  const activeTab =
    location.hash === '#secret-refs' ? 'secret-refs' : 'profiles';
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const canManageProfiles = useCanManageProfiles();
  const canManageProtectedProfiles = useIsAdmin();
  const selectedWorkspaceName = workspaceNameForSelection(
    appBarContext.workspaceSelection
  );
  const canManageWorkspaceDefaults = useCanWriteForWorkspace(
    selectedWorkspaceName
  );

  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [profileFormOpen, setProfileFormOpen] = useState(false);
  const [editingProfile, setEditingProfile] =
    useState<RuntimeProfileResponse | null>(null);
  const [entryDialog, setEntryDialog] = useState<EntryDialogState | null>(null);
  const [deletingProfile, setDeletingProfile] =
    useState<RuntimeProfileResponse | null>(null);
  const [deletingEntry, setDeletingEntry] = useState<{
    target: ProfileEntryTarget;
    entry: RuntimeProfileEntryResponse;
  } | null>(null);
  const [actionProfile, setActionProfile] = useState<string | null>(null);

  useEffect(() => {
    appBarContext.setTitle('Profiles & Secrets');
  }, [appBarContext]);

  const queryInit = useMemo(
    () =>
      whenEnabled(canManageProfiles, {
        params: {
          query: { remoteNode },
        },
      }),
    [canManageProfiles, remoteNode]
  );

  const { data, mutate, isLoading } = useQuery('/profiles', queryInit);
  const profiles = data?.profiles || [];
  const activeProfiles = useMemo(
    () =>
      profiles.filter(
        (profile) => profile.status === RuntimeProfileStatus.active
      ),
    [profiles]
  );
  const {
    data: globalDefaults,
    mutate: mutateGlobalDefaults,
    isLoading: isGlobalDefaultsLoading,
    error: globalDefaultsError,
  } = useQuery(
    '/profiles/_global',
    whenEnabled(canManageProtectedProfiles, {
      params: {
        query: { remoteNode },
      },
    })
  );
  const {
    data: workspaceDefaults,
    mutate: mutateWorkspaceDefaults,
    isLoading: isWorkspaceDefaultsLoading,
    error: workspaceDefaultsError,
  } = useQuery(
    '/profiles/_workspaces/{workspaceName}',
    selectedWorkspaceName && canManageWorkspaceDefaults
      ? {
          params: {
            path: { workspaceName: selectedWorkspaceName },
            query: { remoteNode },
          },
        }
      : null
  );
  const globalDefaultsLoadError = globalDefaultsError
    ? errorMessage(globalDefaultsError, 'Failed to load global defaults')
    : undefined;
  const workspaceDefaultsLoadError = workspaceDefaultsError
    ? errorMessage(workspaceDefaultsError, 'Failed to load workspace defaults')
    : undefined;
  const globalTarget = useMemo(
    () =>
      targetFromGlobalDefaults(
        globalDefaults,
        canManageProtectedProfiles,
        globalDefaultsLoadError
      ),
    [canManageProtectedProfiles, globalDefaults, globalDefaultsLoadError]
  );
  const workspaceTarget = useMemo(
    () =>
      selectedWorkspaceName
        ? targetFromWorkspaceDefaults(
            selectedWorkspaceName,
            workspaceDefaults,
            canManageWorkspaceDefaults,
            workspaceDefaultsLoadError
          )
        : null,
    [
      canManageWorkspaceDefaults,
      selectedWorkspaceName,
      workspaceDefaults,
      workspaceDefaultsLoadError,
    ]
  );

  function canManageProfile(profile: RuntimeProfileResponse): boolean {
    return (
      canManageProfiles && (!profile.protected || canManageProtectedProfiles)
    );
  }

  const reload = useCallback(() => {
    void mutate();
    void mutateGlobalDefaults();
    void mutateWorkspaceDefaults();
  }, [mutate, mutateGlobalDefaults, mutateWorkspaceDefaults]);

  const workspaceDefaultProfileActionKey = workspaceTarget
    ? `${workspaceTarget.actionKey}:defaultProfile`
    : null;

  const openEntryDialog = useCallback(
    (target: ProfileEntryTarget, kind: RuntimeProfileEntryKind) => {
      setEntryDialog({ target, kind });
    },
    []
  );

  const editEntry = useCallback(
    (target: ProfileEntryTarget, entry: RuntimeProfileEntryResponse) => {
      setEntryDialog({ target, kind: entry.kind, entry });
    },
    []
  );

  const confirmDeleteEntry = useCallback(
    (target: ProfileEntryTarget, entry: RuntimeProfileEntryResponse) => {
      setDeletingEntry({ target, entry });
    },
    []
  );

  async function toggleStatus(profile: RuntimeProfileResponse): Promise<void> {
    if (!canManageProfile(profile)) return;
    setError(null);
    setSuccess(null);
    setActionProfile(profile.name);
    const nextStatus =
      profile.status === RuntimeProfileStatus.active
        ? RuntimeProfileStatus.disabled
        : RuntimeProfileStatus.active;
    try {
      const { error: apiError } = await client.PATCH(
        '/profiles/{profileName}',
        {
          params: {
            path: { profileName: profile.name },
            query: { remoteNode },
          },
          body: { status: nextStatus },
        }
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to update profile');
      }
      setSuccess(`${profile.name} ${nextStatus}`);
      reload();
    } catch (err) {
      setError(errorMessage(err, 'Failed to update profile'));
    } finally {
      setActionProfile(null);
    }
  }

  async function deleteProfile(): Promise<void> {
    if (!deletingProfile) return;
    if (!canManageProfile(deletingProfile)) return;
    setError(null);
    setSuccess(null);
    setActionProfile(deletingProfile.name);
    try {
      const { error: apiError } = await client.DELETE(
        '/profiles/{profileName}',
        {
          params: {
            path: { profileName: deletingProfile.name },
            query: { remoteNode },
          },
        }
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to delete profile');
      }
      setSuccess(`${deletingProfile.name} deleted`);
      setDeletingProfile(null);
      reload();
    } catch (err) {
      setError(errorMessage(err, 'Failed to delete profile'));
    } finally {
      setActionProfile(null);
    }
  }

  async function deleteEntry(): Promise<void> {
    if (!deletingEntry) return;
    if (!deletingEntry.target.canManage) return;
    setError(null);
    setSuccess(null);
    setActionProfile(deletingEntry.target.actionKey);
    try {
      const apiError = await deleteProfileTargetEntry(
        client,
        deletingEntry.target,
        deletingEntry.entry.key,
        remoteNode
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to delete entry');
      }
      setSuccess(`${deletingEntry.entry.key} removed`);
      setDeletingEntry(null);
      reload();
    } catch (err) {
      setError(errorMessage(err, 'Failed to delete entry'));
    } finally {
      setActionProfile(null);
    }
  }

  async function updateWorkspaceDefaultProfile(value: string): Promise<void> {
    if (!workspaceTarget?.workspaceName) return;
    const defaultProfile =
      value === NO_WORKSPACE_DEFAULT_PROFILE_VALUE ? '' : value;
    setError(null);
    setSuccess(null);
    setActionProfile(workspaceDefaultProfileActionKey);
    try {
      const { error: apiError } = await client.PATCH(
        '/profiles/_workspaces/{workspaceName}',
        {
          params: {
            path: { workspaceName: workspaceTarget.workspaceName },
            query: { remoteNode },
          },
          body: { defaultProfile },
        }
      );
      if (apiError) {
        throw new Error(
          apiError.message || 'Failed to update workspace default profile'
        );
      }
      setSuccess(
        defaultProfile
          ? `Workspace default profile set to ${defaultProfile}`
          : 'Workspace default profile cleared'
      );
      void mutateWorkspaceDefaults();
    } catch (err) {
      setError(errorMessage(err, 'Failed to update workspace default profile'));
    } finally {
      setActionProfile(null);
    }
  }

  function selectTab(tab: 'profiles' | 'secret-refs'): void {
    navigate(
      {
        pathname: location.pathname,
        search: location.search,
        hash: tab === 'secret-refs' ? '#secret-refs' : '',
      },
      { replace: true }
    );
  }

  function handleTabKeyDown(
    event: React.KeyboardEvent<HTMLElement>,
    currentTab: 'profiles' | 'secret-refs'
  ): void {
    let nextTab: 'profiles' | 'secret-refs';
    switch (event.key) {
      case 'ArrowLeft':
      case 'ArrowRight':
        nextTab = currentTab === 'profiles' ? 'secret-refs' : 'profiles';
        break;
      case 'Home':
        nextTab = 'profiles';
        break;
      case 'End':
        nextTab = 'secret-refs';
        break;
      default:
        return;
    }

    event.preventDefault();
    document.getElementById(`${nextTab}-tab`)?.focus();
    selectTab(nextTab);
  }

  return (
    <div className="flex h-full min-h-0 max-w-7xl flex-col gap-4 overflow-hidden">
      <div className="shrink-0">
        <h1 className="text-lg font-semibold">
          <I18nText text={'Profiles & Secrets'} />
        </h1>
        <p className="text-sm text-muted-foreground">
          <I18nText
            text={
              'Manage environment profiles and individual secret references used by DAG runs.'
            }
          />
        </p>
      </div>

      <I18nProps>
        <Tabs
          role="tablist"
          aria-label="Profiles and secrets"
          className="shrink-0"
        >
          <Tab
            id="profiles-tab"
            role="tab"
            aria-selected={activeTab === 'profiles'}
            aria-controls="profiles-panel"
            isActive={activeTab === 'profiles'}
            tabIndex={activeTab === 'profiles' ? 0 : -1}
            onClick={() => selectTab('profiles')}
            onKeyDown={(event) => handleTabKeyDown(event, 'profiles')}
            className="cursor-pointer gap-2"
          >
            <SlidersHorizontal className="h-4 w-4" />
            <I18nText text={'Profiles'} />
          </Tab>
          <Tab
            id="secret-refs-tab"
            role="tab"
            aria-selected={activeTab === 'secret-refs'}
            aria-controls="secret-refs-panel"
            isActive={activeTab === 'secret-refs'}
            tabIndex={activeTab === 'secret-refs' ? 0 : -1}
            onClick={() => selectTab('secret-refs')}
            onKeyDown={(event) => handleTabKeyDown(event, 'secret-refs')}
            className="cursor-pointer gap-2"
          >
            <KeyRound className="h-4 w-4" />
            <I18nText text={'DAG Secret Refs'} />
          </Tab>
        </Tabs>
      </I18nProps>

      <div
        id="profiles-panel"
        role="tabpanel"
        aria-labelledby="profiles-tab"
        hidden={activeTab !== 'profiles'}
        className="min-h-0 flex-1 overflow-hidden"
      >
        <section className="h-full min-h-0 overflow-auto pr-1">
          <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="text-base font-semibold">
                  <I18nText text={'Profiles'} />
                </h2>
                <p className="text-sm text-muted-foreground">
                  <I18nText
                    text={'Environment bundles selected when a run starts.'}
                  />
                </p>
              </div>
              <Button
                size="sm"
                className="h-8"
                disabled={!canManageProfiles}
                onClick={() => {
                  setEditingProfile(null);
                  setProfileFormOpen(true);
                }}
              >
                <Plus className="mr-1.5 h-4 w-4" />
                <I18nText text={'Add Profile'} />
              </Button>
            </div>

            {error && (
              <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            {success && (
              <div className="rounded-md bg-success/10 p-3 text-sm text-success">
                {success}
              </div>
            )}

            {(canManageProtectedProfiles ||
              (workspaceTarget && canManageWorkspaceDefaults)) && (
              <div className="card-obsidian min-h-0 overflow-auto">
                <Table className="text-xs">
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-[260px]">
                        <I18nText text={'Default Layer'} />
                      </TableHead>
                      <TableHead className="w-[120px]">
                        <I18nText text={'Scope'} />
                      </TableHead>
                      <TableHead>
                        <I18nText text={'Entries'} />
                      </TableHead>
                      <TableHead className="w-[170px]">
                        <I18nText text={'Updated'} />
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {canManageProtectedProfiles && (
                      <TableRow>
                        <TableCell>
                          <div className="flex min-w-0 flex-col gap-1">
                            <div className="flex min-w-0 items-center gap-2">
                              <Globe2 className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                              <span className="font-medium">
                                {globalTarget.title}
                              </span>
                              <code className="whitespace-normal break-words text-xs text-muted-foreground">
                                {globalTarget.name}
                              </code>
                            </div>
                            {globalTarget.description && (
                              <span className="text-xs text-muted-foreground">
                                {globalTarget.description}
                              </span>
                            )}
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">
                            <I18nText text={'Global'} />
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <ProfileEntriesCell
                            target={globalTarget}
                            busy={actionProfile === globalTarget.actionKey}
                            isLoading={isGlobalDefaultsLoading}
                            onAdd={openEntryDialog}
                            onEdit={editEntry}
                            onDelete={confirmDeleteEntry}
                          />
                        </TableCell>
                        <TableCell className="text-muted-foreground">
                          {globalTarget.updatedAt ? (
                            dayjs(globalTarget.updatedAt).format(
                              'MMM D, YYYY HH:mm'
                            )
                          ) : (
                            <I18nText text={'Never'} />
                          )}
                        </TableCell>
                      </TableRow>
                    )}
                    {workspaceTarget && canManageWorkspaceDefaults && (
                      <TableRow>
                        <TableCell>
                          <div className="flex min-w-0 flex-col gap-1">
                            <div className="flex min-w-0 items-center gap-2">
                              <Building2 className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                              <span className="font-medium">
                                {workspaceTarget.title}
                              </span>
                              <code className="whitespace-normal break-words text-xs text-muted-foreground">
                                {workspaceTarget.name}
                              </code>
                            </div>
                            {workspaceTarget.description && (
                              <span className="text-xs text-muted-foreground">
                                {workspaceTarget.description}
                              </span>
                            )}
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">
                            <I18nText text={'Workspace'} />
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <ProfileEntriesCell
                            target={workspaceTarget}
                            busy={actionProfile === workspaceTarget.actionKey}
                            isLoading={isWorkspaceDefaultsLoading}
                            onAdd={openEntryDialog}
                            onEdit={editEntry}
                            onDelete={confirmDeleteEntry}
                          />
                        </TableCell>
                        <TableCell className="text-muted-foreground">
                          {workspaceTarget.updatedAt ? (
                            dayjs(workspaceTarget.updatedAt).format(
                              'MMM D, YYYY HH:mm'
                            )
                          ) : (
                            <I18nText text={'Never'} />
                          )}
                        </TableCell>
                      </TableRow>
                    )}
                    {workspaceTarget && canManageWorkspaceDefaults && (
                      <TableRow>
                        <TableCell>
                          <div className="flex min-w-0 flex-col gap-1">
                            <div className="flex min-w-0 items-center gap-2">
                              <SlidersHorizontal className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                              <span className="font-medium">
                                <I18nText text={'Workspace default profile'} />
                              </span>
                            </div>
                            <span className="text-xs text-muted-foreground">
                              <I18nText
                                text={
                                  'Fallback when the run and DAG do not choose a profile.'
                                }
                              />
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">
                            <I18nText text={'Workspace'} />
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <WorkspaceDefaultProfileCell
                            profiles={activeProfiles}
                            value={workspaceDefaults?.defaultProfile || ''}
                            busy={
                              actionProfile === workspaceDefaultProfileActionKey
                            }
                            disabled={!canManageProfiles}
                            isLoading={isLoading || isWorkspaceDefaultsLoading}
                            onChange={updateWorkspaceDefaultProfile}
                          />
                        </TableCell>
                        <TableCell className="text-muted-foreground">
                          {workspaceTarget.updatedAt ? (
                            dayjs(workspaceTarget.updatedAt).format(
                              'MMM D, YYYY HH:mm'
                            )
                          ) : (
                            <I18nText text={'Never'} />
                          )}
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            )}

            <div className="card-obsidian min-h-0 overflow-auto">
              <Table className="text-xs">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-[260px]">
                      <I18nText text={'Profile'} />
                    </TableHead>
                    <TableHead className="w-[110px]">
                      <I18nText text={'Status'} />
                    </TableHead>
                    <TableHead>
                      <I18nText text={'Entries'} />
                    </TableHead>
                    <TableHead className="w-[170px]">
                      <I18nText text={'Updated'} />
                    </TableHead>
                    <TableHead className="w-[80px]"></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {!canManageProfiles ? (
                    <TableRow>
                      <TableCell
                        colSpan={5}
                        className="py-8 text-center text-muted-foreground"
                      >
                        <I18nText
                          text={
                            'You do not have permission to manage profiles.'
                          }
                        />
                      </TableCell>
                    </TableRow>
                  ) : isLoading ? (
                    <TableRow>
                      <TableCell
                        colSpan={5}
                        className="py-8 text-center text-muted-foreground"
                      >
                        <I18nText text={'Loading profiles...'} />
                      </TableCell>
                    </TableRow>
                  ) : profiles.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={5}
                        className="py-8 text-center text-muted-foreground"
                      >
                        <I18nText text={'No profiles found.'} />
                      </TableCell>
                    </TableRow>
                  ) : (
                    profiles.map((profile) => {
                      const profileManageDisabled = !canManageProfile(profile);
                      const profileBusy = actionProfile === profile.name;
                      const target = targetFromProfile(
                        profile,
                        !profileManageDisabled
                      );

                      return (
                        <TableRow key={profile.id}>
                          <TableCell>
                            <div className="flex min-w-0 flex-col gap-1">
                              <div className="flex min-w-0 items-center gap-2">
                                <SlidersHorizontal className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                                <code className="whitespace-normal break-words text-xs">
                                  {profile.name}
                                </code>
                                {profile.protected && (
                                  <Badge
                                    variant="outline"
                                    className="h-5 px-1.5 text-[10px]"
                                  >
                                    <I18nText text={'Protected'} />
                                  </Badge>
                                )}
                                {profile.protected &&
                                  !canManageProtectedProfiles && (
                                    <Badge
                                      variant="secondary"
                                      className="h-5 px-1.5 text-[10px]"
                                    >
                                      <I18nText text={'Admin'} />
                                    </Badge>
                                  )}
                              </div>
                              {profile.description && (
                                <span className="text-xs text-muted-foreground">
                                  {profile.description}
                                </span>
                              )}
                            </div>
                          </TableCell>
                          <TableCell>
                            <Badge
                              variant={
                                profile.status === RuntimeProfileStatus.active
                                  ? 'success'
                                  : 'warning'
                              }
                            >
                              {profile.status}
                            </Badge>
                          </TableCell>
                          <TableCell>
                            <ProfileEntriesCell
                              target={target}
                              busy={profileBusy}
                              onAdd={openEntryDialog}
                              onEdit={editEntry}
                              onDelete={confirmDeleteEntry}
                            />
                          </TableCell>
                          <TableCell className="text-muted-foreground">
                            {dayjs(profile.updatedAt).format(
                              'MMM D, YYYY HH:mm'
                            )}
                          </TableCell>
                          <TableCell>
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  aria-label={`Actions for ${profile.name}`}
                                  disabled={
                                    profileManageDisabled || profileBusy
                                  }
                                >
                                  {profileBusy ? (
                                    <Loader2 className="h-4 w-4 animate-spin" />
                                  ) : (
                                    <MoreHorizontal className="h-4 w-4" />
                                  )}
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end">
                                <DropdownMenuItem
                                  onClick={() => {
                                    setEditingProfile(profile);
                                    setProfileFormOpen(true);
                                  }}
                                >
                                  <Pencil className="mr-2 h-4 w-4" />
                                  <I18nText text={'Edit'} />
                                </DropdownMenuItem>
                                <DropdownMenuItem
                                  onClick={() => toggleStatus(profile)}
                                >
                                  <Power className="mr-2 h-4 w-4" />
                                  {profile.status ===
                                  RuntimeProfileStatus.active ? (
                                    <I18nText text={'Disable'} />
                                  ) : (
                                    <I18nText text={'Enable'} />
                                  )}
                                </DropdownMenuItem>
                                <DropdownMenuItem
                                  className="text-destructive"
                                  onClick={() => setDeletingProfile(profile)}
                                >
                                  <Trash2 className="mr-2 h-4 w-4" />
                                  <I18nText text={'Delete'} />
                                </DropdownMenuItem>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </TableCell>
                        </TableRow>
                      );
                    })
                  )}
                </TableBody>
              </Table>
            </div>
          </div>
        </section>
      </div>

      <div
        id="secret-refs-panel"
        role="tabpanel"
        aria-labelledby="secret-refs-tab"
        hidden={activeTab !== 'secret-refs'}
        className="min-h-0 flex-1 overflow-hidden"
      >
        <SecretRefsSection />
      </div>

      <ProfileFormDialog
        open={profileFormOpen}
        profile={editingProfile}
        remoteNode={remoteNode}
        canSetProtected={canManageProtectedProfiles}
        onClose={() => {
          setProfileFormOpen(false);
          setEditingProfile(null);
        }}
        onSaved={(message) => {
          setSuccess(message);
          setError(null);
          setProfileFormOpen(false);
          setEditingProfile(null);
          reload();
        }}
      />

      <ProfileEntryDialog
        state={entryDialog}
        remoteNode={remoteNode}
        onClose={() => setEntryDialog(null)}
        onSaved={(message) => {
          setSuccess(message);
          setError(null);
          setEntryDialog(null);
          reload();
        }}
      />

      <I18nProps>
        <ConfirmModal
          title="Delete Profile"
          buttonText="Delete"
          visible={!!deletingProfile}
          dismissModal={() => setDeletingProfile(null)}
          onSubmit={deleteProfile}
        >
          <span className="text-sm text-muted-foreground">
            {deletingProfile ? (
              <I18nText
                text="Delete {name}?"
                values={{ name: deletingProfile.name }}
              />
            ) : (
              ''
            )}
          </span>
        </ConfirmModal>
      </I18nProps>

      <I18nProps>
        <ConfirmModal
          title="Delete Entry"
          buttonText="Delete"
          visible={!!deletingEntry}
          dismissModal={() => setDeletingEntry(null)}
          onSubmit={deleteEntry}
        >
          <span className="text-sm text-muted-foreground">
            {deletingEntry ? (
              <I18nText
                text="Delete {name}?"
                values={{ name: deletingEntry.entry.key }}
              />
            ) : (
              ''
            )}
          </span>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}

function WorkspaceDefaultProfileCell({
  profiles,
  value,
  busy,
  disabled,
  isLoading,
  onChange,
}: {
  profiles: RuntimeProfileResponse[];
  value: string;
  busy: boolean;
  disabled: boolean;
  isLoading: boolean;
  onChange: (value: string) => void;
}): React.ReactElement {
  const unavailable =
    value !== '' && !profiles.some((profile) => profile.name === value);
  return (
    <div className="flex max-w-sm items-center gap-2">
      <Select
        value={value || NO_WORKSPACE_DEFAULT_PROFILE_VALUE}
        disabled={disabled || busy || isLoading}
        onValueChange={onChange}
      >
        <I18nProps>
          <SelectTrigger
            aria-label="Workspace default profile"
            className="h-7 w-full min-w-[220px]"
          >
            <I18nProps>
              <SelectValue placeholder="None" />
            </I18nProps>
          </SelectTrigger>
        </I18nProps>
        <SelectContent>
          <SelectItem value={NO_WORKSPACE_DEFAULT_PROFILE_VALUE}>
            <I18nText text={'None'} />
          </SelectItem>
          {unavailable && (
            <SelectItem value={value} disabled>
              <span className="flex w-full items-center justify-between gap-3">
                <span>{value}</span>
                <Badge variant="secondary" className="h-4 px-1.5 text-[10px]">
                  <I18nText text={'Unavailable'} />
                </Badge>
              </span>
            </SelectItem>
          )}
          {profiles.map((profile) => (
            <SelectItem key={profile.id} value={profile.name}>
              <span className="flex w-full items-center justify-between gap-3">
                <span>{profile.name}</span>
                {profile.protected && (
                  <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
                    <I18nText text={'Protected'} />
                  </Badge>
                )}
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {busy && (
        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
      )}
    </div>
  );
}

function ProfileEntriesCell({
  target,
  busy,
  isLoading,
  onAdd,
  onEdit,
  onDelete,
}: {
  target: ProfileEntryTarget;
  busy: boolean;
  isLoading?: boolean;
  onAdd: (target: ProfileEntryTarget, kind: RuntimeProfileEntryKind) => void;
  onEdit: (
    target: ProfileEntryTarget,
    entry: RuntimeProfileEntryResponse
  ) => void;
  onDelete: (
    target: ProfileEntryTarget,
    entry: RuntimeProfileEntryResponse
  ) => void;
}): React.ReactElement {
  const controlsDisabled = !target.canManage || !!target.loadError || busy;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 px-2 text-xs"
          disabled={controlsDisabled}
          onClick={() => onAdd(target, RuntimeProfileEntryKind.variable)}
        >
          <Plus className="h-3.5 w-3.5" />
          <I18nText text={'Variable'} />
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 px-2 text-xs"
          disabled={controlsDisabled}
          onClick={() => onAdd(target, RuntimeProfileEntryKind.secret)}
        >
          <KeyRound className="h-3.5 w-3.5" />
          <I18nText text={'Secret'} />
        </Button>
      </div>
      {isLoading ? (
        <span className="text-xs text-muted-foreground">
          <I18nText text={'Loading...'} />
        </span>
      ) : target.loadError ? (
        <span className="text-xs text-destructive">{target.loadError}</span>
      ) : target.entries.length === 0 ? (
        <span className="text-xs text-muted-foreground">
          <I18nText text={'No entries'} />
        </span>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {target.entries.map((entry) => (
            <div
              key={entry.key}
              className="flex max-w-full items-center gap-1 rounded border border-border bg-muted/40 px-2 py-1"
            >
              <Badge variant="outline" className="h-5 px-1.5">
                {entryLabel(entry)}
              </Badge>
              <code className="whitespace-normal break-words text-xs">
                {entry.key}
              </code>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-5 w-5"
                aria-label={`Edit ${entry.key}`}
                disabled={controlsDisabled}
                onClick={() => onEdit(target, entry)}
              >
                <Pencil className="h-3 w-3" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-5 w-5 text-destructive"
                aria-label={`Delete ${entry.key}`}
                disabled={controlsDisabled}
                onClick={() => onDelete(target, entry)}
              >
                <Trash2 className="h-3 w-3" />
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ProfileFormDialog({
  open,
  profile,
  remoteNode,
  canSetProtected,
  onClose,
  onSaved,
}: {
  open: boolean;
  profile: RuntimeProfileResponse | null;
  remoteNode: string;
  canSetProtected: boolean;
  onClose: () => void;
  onSaved: (message: string) => void;
}): React.ReactElement {
  const client = useClient();
  const isEditing = !!profile;
  const [form, setForm] = useState<ProfileFormState>(() =>
    initialProfileForm()
  );
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      setForm(initialProfileForm());
      setError(null);
      return;
    }
    setForm(profile ? formFromProfile(profile) : initialProfileForm());
    setError(null);
  }, [open, profile]);

  async function handleSubmit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    setError(null);
    if (!isEditing && form.name.trim() === '') {
      setError('Name is required');
      return;
    }

    setIsSaving(true);
    try {
      if (isEditing && profile) {
        const { error: apiError } = await client.PATCH(
          '/profiles/{profileName}',
          {
            params: {
              path: { profileName: profile.name },
              query: { remoteNode },
            },
            body: {
              description: form.description,
              protected: form.protected,
            },
          }
        );
        if (apiError) {
          throw new Error(apiError.message || 'Failed to update profile');
        }
        onSaved(`${profile.name} updated`);
      } else {
        const { error: apiError } = await client.POST('/profiles', {
          params: { query: { remoteNode } },
          body: {
            name: form.name.trim(),
            description: form.description,
            protected: form.protected,
          },
        });
        if (apiError) {
          throw new Error(apiError.message || 'Failed to create profile');
        }
        onSaved(`${form.name.trim()} created`);
      }
    } catch (err) {
      setError(errorMessage(err, 'Failed to save profile'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => !nextOpen && onClose()}>
      <DialogContent className="sm:max-w-[480px]">
        <form onSubmit={(event) => void handleSubmit(event)}>
          <DialogHeader>
            <DialogTitle>
              {isEditing ? (
                <I18nText text={'Edit Profile'} />
              ) : (
                <I18nText text={'Add Profile'} />
              )}
            </DialogTitle>
            <DialogDescription className="sr-only">
              <I18nText
                text={'Configure runtime profile metadata and protection.'}
              />
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {error && (
              <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="profile-name">
                <I18nText text={'Name'} />
              </Label>
              <I18nProps>
                <Input
                  id="profile-name"
                  value={form.name}
                  readOnly={isEditing}
                  disabled={isEditing || isSaving}
                  placeholder="local"
                  onChange={(event) =>
                    setForm((prev) => ({ ...prev, name: event.target.value }))
                  }
                />
              </I18nProps>
            </div>
            <div className="space-y-2">
              <Label htmlFor="profile-description">
                <I18nText text={'Description'} />
              </Label>
              <Textarea
                id="profile-description"
                rows={3}
                value={form.description}
                disabled={isSaving}
                onChange={(event) =>
                  setForm((prev) => ({
                    ...prev,
                    description: event.target.value,
                  }))
                }
              />
            </div>
            <div className="flex items-center gap-2">
              <Checkbox
                id="profile-protected"
                checked={form.protected}
                disabled={isSaving || !canSetProtected}
                onCheckedChange={(checked) =>
                  setForm((prev) => ({ ...prev, protected: checked === true }))
                }
              />
              <Label htmlFor="profile-protected">
                <I18nText text={'Protected'} />
              </Label>
            </div>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              disabled={isSaving}
            >
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving ? (
                <I18nText text={'Saving...'} />
              ) : (
                <I18nText text={'Save'} />
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ProfileEntryDialog({
  state,
  remoteNode,
  onClose,
  onSaved,
}: {
  state: EntryDialogState | null;
  remoteNode: string;
  onClose: () => void;
  onSaved: (message: string) => void;
}): React.ReactElement {
  const client = useClient();
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const target = state?.target;
  const kind = state?.kind ?? RuntimeProfileEntryKind.variable;
  const isEditing = !!state?.entry;
  const isSecret = kind === RuntimeProfileEntryKind.secret;

  useEffect(() => {
    if (!state) {
      setKey('');
      setValue('');
      setError(null);
      return;
    }
    setKey(state.entry?.key || '');
    setValue(
      state.entry && state.entry.kind === RuntimeProfileEntryKind.variable
        ? state.entry.value || ''
        : ''
    );
    setError(null);
  }, [state]);

  async function handleSubmit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    if (!target) return;
    setError(null);

    const trimmedKey = key.trim();
    if (trimmedKey === '') {
      setError('Key is required');
      return;
    }
    if (isSecret && value === '') {
      setError('Value is required');
      return;
    }

    setIsSaving(true);
    try {
      const apiError = await saveProfileTargetEntry(
        client,
        target,
        kind,
        trimmedKey,
        value,
        remoteNode
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to save entry');
      }
      onSaved(`${trimmedKey} saved`);
    } catch (err) {
      setError(errorMessage(err, 'Failed to save entry'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <Dialog open={!!state} onOpenChange={(nextOpen) => !nextOpen && onClose()}>
      <DialogContent className="sm:max-w-[480px]">
        <form onSubmit={(event) => void handleSubmit(event)}>
          <DialogHeader>
            <DialogTitle>{entryDialogTitle(isSecret, isEditing)}</DialogTitle>
            <DialogDescription className="sr-only">
              <I18nText text={'Configure a runtime profile entry.'} />
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {error && (
              <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="profile-entry-key">
                <I18nText text={'Key'} />
              </Label>
              <I18nProps>
                <Input
                  id="profile-entry-key"
                  value={key}
                  readOnly={isEditing}
                  disabled={isEditing || isSaving}
                  placeholder="LOG_LEVEL"
                  onChange={(event) => setKey(event.target.value)}
                />
              </I18nProps>
            </div>
            <div className="space-y-2">
              <Label htmlFor="profile-entry-value">
                <I18nText text={'Value'} />
              </Label>
              <I18nProps>
                <Textarea
                  id="profile-entry-value"
                  rows={3}
                  value={value}
                  disabled={isSaving}
                  placeholder={isSecret && isEditing ? 'New value' : undefined}
                  onChange={(event) => setValue(event.target.value)}
                />
              </I18nProps>
            </div>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              disabled={isSaving}
            >
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving ? (
                <I18nText text={'Saving...'} />
              ) : (
                <I18nText text={'Save'} />
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
