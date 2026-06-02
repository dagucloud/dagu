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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanManageProfiles, useIsAdmin } from '@/contexts/AuthContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import dayjs from '@/lib/dayjs';
import {
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

type RuntimeProfileResponse = components['schemas']['RuntimeProfileResponse'];
type RuntimeProfileEntryResponse =
  components['schemas']['RuntimeProfileEntryResponse'];

type ProfileFormState = {
  name: string;
  description: string;
  protected: boolean;
};

type EntryDialogState = {
  profile: RuntimeProfileResponse;
  kind: RuntimeProfileEntryKind;
  entry?: RuntimeProfileEntryResponse;
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

function entryLabel(entry: RuntimeProfileEntryResponse): string {
  return entry.kind === RuntimeProfileEntryKind.secret ? 'Secret' : 'Variable';
}

function entryDialogTitle(isSecret: boolean, isEditing: boolean): string {
  if (isSecret) {
    return isEditing ? 'Rotate Secret' : 'Add Secret';
  }
  return isEditing ? 'Edit Variable' : 'Add Variable';
}

export default function ProfilesPage(): React.ReactNode {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const canManageProfiles = useCanManageProfiles();
  const canManageProtectedProfiles = useIsAdmin();

  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [profileFormOpen, setProfileFormOpen] = useState(false);
  const [editingProfile, setEditingProfile] =
    useState<RuntimeProfileResponse | null>(null);
  const [entryDialog, setEntryDialog] = useState<EntryDialogState | null>(null);
  const [deletingProfile, setDeletingProfile] =
    useState<RuntimeProfileResponse | null>(null);
  const [deletingEntry, setDeletingEntry] = useState<{
    profile: RuntimeProfileResponse;
    entry: RuntimeProfileEntryResponse;
  } | null>(null);
  const [actionProfile, setActionProfile] = useState<string | null>(null);

  useEffect(() => {
    appBarContext.setTitle('Profiles');
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

  function canManageProfile(profile: RuntimeProfileResponse): boolean {
    return (
      canManageProfiles && (!profile.protected || canManageProtectedProfiles)
    );
  }

  const reload = useCallback(() => {
    void mutate();
  }, [mutate]);

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
    if (!canManageProfile(deletingEntry.profile)) return;
    setError(null);
    setSuccess(null);
    setActionProfile(deletingEntry.profile.name);
    try {
      const { error: apiError } = await client.DELETE(
        '/profiles/{profileName}/entries/{key}',
        {
          params: {
            path: {
              profileName: deletingEntry.profile.name,
              key: deletingEntry.entry.key,
            },
            query: { remoteNode },
          },
        }
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

  return (
    <div className="flex h-full min-h-0 max-w-7xl flex-col gap-4 overflow-auto">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">Profiles</h1>
          <p className="text-sm text-muted-foreground">
            Managed runtime variables and secrets.
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
          Add Profile
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

      <div className="card-obsidian min-h-0 overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[260px]">Profile</TableHead>
              <TableHead className="w-[110px]">Status</TableHead>
              <TableHead>Entries</TableHead>
              <TableHead className="w-[170px]">Updated</TableHead>
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
                  You do not have permission to manage profiles.
                </TableCell>
              </TableRow>
            ) : isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="py-8 text-center text-muted-foreground"
                >
                  Loading profiles...
                </TableCell>
              </TableRow>
            ) : profiles.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="py-8 text-center text-muted-foreground"
                >
                  No profiles found.
                </TableCell>
              </TableRow>
            ) : (
              profiles.map((profile) => {
                const profileManageDisabled = !canManageProfile(profile);
                const profileBusy = actionProfile === profile.name;

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
                              Protected
                            </Badge>
                          )}
                          {profile.protected && !canManageProtectedProfiles && (
                            <Badge
                              variant="secondary"
                              className="h-5 px-1.5 text-[10px]"
                            >
                              Admin
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
                      <div className="flex min-w-0 flex-col gap-2">
                        <div className="flex flex-wrap gap-1.5">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            disabled={profileManageDisabled || profileBusy}
                            onClick={() =>
                              setEntryDialog({
                                profile,
                                kind: RuntimeProfileEntryKind.variable,
                              })
                            }
                          >
                            <Plus className="h-3.5 w-3.5" />
                            Variable
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            disabled={profileManageDisabled || profileBusy}
                            onClick={() =>
                              setEntryDialog({
                                profile,
                                kind: RuntimeProfileEntryKind.secret,
                              })
                            }
                          >
                            <KeyRound className="h-3.5 w-3.5" />
                            Secret
                          </Button>
                        </div>
                        {profile.entries.length === 0 ? (
                          <span className="text-xs text-muted-foreground">
                            No entries
                          </span>
                        ) : (
                          <div className="flex flex-wrap gap-1.5">
                            {profile.entries.map((entry) => (
                              <div
                                key={entry.key}
                                className="flex max-w-full items-center gap-1 rounded border border-border bg-muted/40 px-2 py-1"
                              >
                                <Badge variant="outline" className="h-5 px-1.5">
                                  {entryLabel(entry)}
                                </Badge>
                                <code className="break-all text-xs">
                                  {entry.key}
                                </code>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon"
                                  className="h-5 w-5"
                                  aria-label={`Edit ${entry.key}`}
                                  disabled={
                                    profileManageDisabled || profileBusy
                                  }
                                  onClick={() =>
                                    setEntryDialog({
                                      profile,
                                      kind: entry.kind,
                                      entry,
                                    })
                                  }
                                >
                                  <Pencil className="h-3 w-3" />
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon"
                                  className="h-5 w-5 text-destructive"
                                  aria-label={`Delete ${entry.key}`}
                                  disabled={
                                    profileManageDisabled || profileBusy
                                  }
                                  onClick={() =>
                                    setDeletingEntry({ profile, entry })
                                  }
                                >
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {dayjs(profile.updatedAt).format('MMM D, YYYY HH:mm')}
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={`Actions for ${profile.name}`}
                            disabled={profileManageDisabled || profileBusy}
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
                            Edit
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => toggleStatus(profile)}
                          >
                            <Power className="mr-2 h-4 w-4" />
                            {profile.status === RuntimeProfileStatus.active
                              ? 'Disable'
                              : 'Enable'}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            className="text-destructive"
                            onClick={() => setDeletingProfile(profile)}
                          >
                            <Trash2 className="mr-2 h-4 w-4" />
                            Delete
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

      <ConfirmModal
        title="Delete Profile"
        buttonText="Delete"
        visible={!!deletingProfile}
        dismissModal={() => setDeletingProfile(null)}
        onSubmit={deleteProfile}
      >
        <span className="text-sm text-muted-foreground">
          {deletingProfile ? `Delete ${deletingProfile.name}?` : ''}
        </span>
      </ConfirmModal>

      <ConfirmModal
        title="Delete Entry"
        buttonText="Delete"
        visible={!!deletingEntry}
        dismissModal={() => setDeletingEntry(null)}
        onSubmit={deleteEntry}
      >
        <span className="text-sm text-muted-foreground">
          {deletingEntry ? `Delete ${deletingEntry.entry.key}?` : ''}
        </span>
      </ConfirmModal>
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
              {isEditing ? 'Edit Profile' : 'Add Profile'}
            </DialogTitle>
            <DialogDescription className="sr-only">
              Configure runtime profile metadata and protection.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {error && (
              <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="profile-name">Name</Label>
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
            </div>
            <div className="space-y-2">
              <Label htmlFor="profile-description">Description</Label>
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
              <Label htmlFor="profile-protected">Protected</Label>
            </div>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              disabled={isSaving}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving ? 'Saving...' : 'Save'}
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
  const profile = state?.profile;
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
    if (!profile) return;
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
      if (isSecret) {
        const { error: apiError } = await client.PUT(
          '/profiles/{profileName}/secrets/{key}',
          {
            params: {
              path: { profileName: profile.name, key: trimmedKey },
              query: { remoteNode },
            },
            body: { value },
          }
        );
        if (apiError) {
          throw new Error(apiError.message || 'Failed to save secret');
        }
      } else {
        const { error: apiError } = await client.PUT(
          '/profiles/{profileName}/variables/{key}',
          {
            params: {
              path: { profileName: profile.name, key: trimmedKey },
              query: { remoteNode },
            },
            body: { value },
          }
        );
        if (apiError) {
          throw new Error(apiError.message || 'Failed to save variable');
        }
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
              Configure a runtime profile entry.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            {error && (
              <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="profile-entry-key">Key</Label>
              <Input
                id="profile-entry-key"
                value={key}
                readOnly={isEditing}
                disabled={isEditing || isSaving}
                placeholder="LOG_LEVEL"
                onChange={(event) => setKey(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="profile-entry-value">Value</Label>
              <Textarea
                id="profile-entry-value"
                rows={3}
                value={value}
                disabled={isSaving}
                placeholder={isSecret && isEditing ? 'New value' : undefined}
                onChange={(event) => setValue(event.target.value)}
              />
            </div>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              disabled={isSaving}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving ? 'Saving...' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
