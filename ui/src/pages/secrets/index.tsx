// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, SecretProviderType, SecretStatus } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  Dialog,
  DialogContent,
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
import { Textarea } from '@/components/ui/textarea';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanManageSecrets } from '@/contexts/AuthContext';
import { useClient, useQuery } from '@/hooks/api';
import dayjs from '@/lib/dayjs';
import {
  WorkspaceKind,
  workspaceNameForSelection,
  workspaceSelectionQuery,
} from '@/lib/workspace';
import {
  KeyRound,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Trash2,
} from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';

type SecretResponse = components['schemas']['SecretResponse'];
type CreateSecretRequest = components['schemas']['CreateSecretRequest'];
type UpdateSecretRequest = components['schemas']['UpdateSecretRequest'];

type SecretFormState = {
  workspace: string;
  ref: string;
  description: string;
  providerType: SecretProviderType;
  providerConnectionId: string;
  providerRef: string;
  value: string;
};

const PROVIDER_LABELS: Record<SecretProviderType, string> = {
  [SecretProviderType.dagu_managed]: 'Dagu Managed',
  [SecretProviderType.vault]: 'Vault',
  [SecretProviderType.kubernetes]: 'Kubernetes',
  [SecretProviderType.gcp_secret_manager]: 'Google Secret Manager',
  [SecretProviderType.aws_secrets_manager]: 'AWS Secrets Manager',
  [SecretProviderType.azure_key_vault]: 'Azure Key Vault',
};

const PROVIDERS = Object.values(SecretProviderType);

function initialFormState(workspace: string): SecretFormState {
  return {
    workspace,
    ref: '',
    description: '',
    providerType: SecretProviderType.dagu_managed,
    providerConnectionId: '',
    providerRef: '',
    value: '',
  };
}

function formStateFromSecret(secret: SecretResponse): SecretFormState {
  return {
    workspace: secret.workspace || 'default',
    ref: secret.ref,
    description: secret.description || '',
    providerType: secret.providerType,
    providerConnectionId: secret.providerConnectionId || '',
    providerRef: secret.providerRef || '',
    value: '',
  };
}

function optionalString(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
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

export default function SecretsPage(): React.ReactNode {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const canManageSecrets = useCanManageSecrets();
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const defaultWorkspace = useMemo(() => {
    const selection = appBarContext.workspaceSelection;
    if (selection?.kind === WorkspaceKind.workspace) {
      return workspaceNameForSelection(selection);
    }
    return 'default';
  }, [appBarContext.workspaceSelection]);
  const workspaceQuery = useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );

  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [isFormOpen, setIsFormOpen] = useState(false);
  const [editingSecret, setEditingSecret] = useState<SecretResponse | null>(
    null
  );
  const [rotatingSecret, setRotatingSecret] = useState<SecretResponse | null>(
    null
  );
  const [deletingSecret, setDeletingSecret] = useState<SecretResponse | null>(
    null
  );
  const [actionSecretId, setActionSecretId] = useState<string | null>(null);

  useEffect(() => {
    appBarContext.setTitle('Secrets');
  }, [appBarContext]);

  const { data, mutate, isLoading } = useQuery('/secrets', {
    params: {
      query: {
        remoteNode,
        ...workspaceQuery,
        limit: 500,
      },
    },
  });

  const secrets = data?.secrets || [];

  const reload = useCallback(() => {
    void mutate();
  }, [mutate]);

  async function toggleStatus(secret: SecretResponse): Promise<void> {
    setError(null);
    setSuccess(null);
    setActionSecretId(secret.id);
    try {
      if (secret.status === SecretStatus.active) {
        const { error: apiError } = await client.POST(
          '/secrets/{secretId}/disable',
          {
            params: { path: { secretId: secret.id }, query: { remoteNode } },
          }
        );
        if (apiError)
          throw new Error(apiError.message || 'Failed to disable secret');
        setSuccess(`${secret.ref} disabled`);
      } else {
        const { error: apiError } = await client.POST(
          '/secrets/{secretId}/enable',
          {
            params: { path: { secretId: secret.id }, query: { remoteNode } },
          }
        );
        if (apiError)
          throw new Error(apiError.message || 'Failed to enable secret');
        setSuccess(`${secret.ref} enabled`);
      }
      reload();
    } catch (err) {
      setError(errorMessage(err, 'Failed to update secret'));
    } finally {
      setActionSecretId(null);
    }
  }

  async function deleteSecret(): Promise<void> {
    if (!deletingSecret) return;
    setError(null);
    setSuccess(null);
    setActionSecretId(deletingSecret.id);
    try {
      const { error: apiError } = await client.DELETE('/secrets/{secretId}', {
        params: {
          path: { secretId: deletingSecret.id },
          query: { remoteNode },
        },
      });
      if (apiError)
        throw new Error(apiError.message || 'Failed to delete secret');
      setSuccess(`${deletingSecret.ref} deleted`);
      setDeletingSecret(null);
      reload();
    } catch (err) {
      setError(errorMessage(err, 'Failed to delete secret'));
    } finally {
      setActionSecretId(null);
    }
  }

  if (!canManageSecrets) {
    return (
      <div className="flex h-64 items-center justify-center text-sm text-muted-foreground">
        You do not have permission to access this page.
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 max-w-7xl flex-col gap-4 overflow-auto">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">Secrets</h1>
          <p className="text-sm text-muted-foreground">
            Manage secret refs and values.
          </p>
        </div>
        <Button
          size="sm"
          className="h-8"
          onClick={() => {
            setEditingSecret(null);
            setIsFormOpen(true);
          }}
        >
          <Plus className="mr-1.5 h-4 w-4" />
          Add Secret
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
              <TableHead className="w-[320px]">Ref</TableHead>
              <TableHead className="w-[180px]">Provider</TableHead>
              <TableHead className="w-[110px]">Status</TableHead>
              <TableHead className="w-[90px]">Version</TableHead>
              <TableHead className="w-[170px]">Rotated</TableHead>
              <TableHead className="w-[80px]"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="py-8 text-center text-muted-foreground"
                >
                  Loading secrets...
                </TableCell>
              </TableRow>
            ) : secrets.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="py-8 text-center text-muted-foreground"
                >
                  No secrets found.
                </TableCell>
              </TableRow>
            ) : (
              secrets.map((secret) => (
                <TableRow key={secret.id}>
                  <TableCell>
                    <div className="flex min-w-0 flex-col gap-1">
                      <div className="flex min-w-0 items-center gap-2">
                        <KeyRound className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                        <code className="truncate text-xs">
                          {secret.workspace}/{secret.ref}
                        </code>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <span>{PROVIDER_LABELS[secret.providerType]}</span>
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        secret.status === SecretStatus.active
                          ? 'success'
                          : 'warning'
                      }
                    >
                      {secret.status}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {secret.currentVersion > 0 ? (
                      <code className="rounded bg-muted px-1.5 py-0.5 text-xs">
                        v{secret.currentVersion}
                      </code>
                    ) : (
                      <span className="text-muted-foreground">None</span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {secret.lastRotatedAt
                      ? dayjs(secret.lastRotatedAt).format('MMM D, YYYY HH:mm')
                      : 'Never'}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Actions for ${secret.workspace}/${secret.ref}`}
                          disabled={actionSecretId === secret.id}
                        >
                          {actionSecretId === secret.id ? (
                            <Loader2 className="h-4 w-4 animate-spin" />
                          ) : (
                            <MoreHorizontal className="h-4 w-4" />
                          )}
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          onClick={() => {
                            setEditingSecret(secret);
                            setIsFormOpen(true);
                          }}
                        >
                          <Pencil className="mr-2 h-4 w-4" />
                          Edit
                        </DropdownMenuItem>
                        {secret.providerType ===
                          SecretProviderType.dagu_managed && (
                          <DropdownMenuItem
                            onClick={() => setRotatingSecret(secret)}
                          >
                            <RefreshCw className="mr-2 h-4 w-4" />
                            Rotate
                          </DropdownMenuItem>
                        )}
                        <DropdownMenuItem onClick={() => toggleStatus(secret)}>
                          <Power className="mr-2 h-4 w-4" />
                          {secret.status === SecretStatus.active
                            ? 'Disable'
                            : 'Enable'}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          className="text-destructive"
                          onClick={() => setDeletingSecret(secret)}
                        >
                          <Trash2 className="mr-2 h-4 w-4" />
                          Delete
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <SecretFormDialog
        open={isFormOpen}
        secret={editingSecret}
        defaultWorkspace={defaultWorkspace}
        remoteNode={remoteNode}
        onClose={() => {
          setIsFormOpen(false);
          setEditingSecret(null);
        }}
        onSaved={(message) => {
          setSuccess(message);
          setError(null);
          setIsFormOpen(false);
          setEditingSecret(null);
          reload();
        }}
      />

      <RotateSecretDialog
        secret={rotatingSecret}
        remoteNode={remoteNode}
        onClose={() => setRotatingSecret(null)}
        onSaved={(message) => {
          setSuccess(message);
          setError(null);
          setRotatingSecret(null);
          reload();
        }}
      />

      <ConfirmModal
        title="Delete Secret"
        buttonText="Delete"
        visible={!!deletingSecret}
        dismissModal={() => setDeletingSecret(null)}
        onSubmit={deleteSecret}
      >
        <span className="text-sm text-muted-foreground">
          {deletingSecret
            ? `Delete ${deletingSecret.workspace}/${deletingSecret.ref}?`
            : ''}
        </span>
      </ConfirmModal>
    </div>
  );
}

function SecretFormDialog({
  open,
  secret,
  defaultWorkspace,
  remoteNode,
  onClose,
  onSaved,
}: {
  open: boolean;
  secret: SecretResponse | null;
  defaultWorkspace: string;
  remoteNode: string;
  onClose: () => void;
  onSaved: (message: string) => void;
}): React.ReactElement {
  const client = useClient();
  const isEditing = !!secret;
  const [form, setForm] = useState<SecretFormState>(() =>
    initialFormState(defaultWorkspace)
  );
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      setForm(initialFormState(defaultWorkspace));
      setError(null);
      return;
    }
    setForm(
      secret ? formStateFromSecret(secret) : initialFormState(defaultWorkspace)
    );
    setError(null);
  }, [defaultWorkspace, open, secret]);

  const isDaguManaged = form.providerType === SecretProviderType.dagu_managed;

  async function handleSubmit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    setError(null);

    if (!isEditing && form.ref.trim() === '') {
      setError('Ref is required');
      return;
    }
    if (!isEditing && isDaguManaged && form.value === '') {
      setError('Value is required');
      return;
    }
    if (!isDaguManaged && form.providerRef.trim() === '') {
      setError('Provider ref is required');
      return;
    }

    setIsSaving(true);
    try {
      if (isEditing && secret) {
        const body: UpdateSecretRequest = {
          description: form.description,
        };
        if (!isDaguManaged) {
          body.providerConnectionId = form.providerConnectionId;
          body.providerRef = form.providerRef;
        }
        const { error: apiError } = await client.PATCH('/secrets/{secretId}', {
          params: { path: { secretId: secret.id }, query: { remoteNode } },
          body,
        });
        if (apiError)
          throw new Error(apiError.message || 'Failed to save secret');
        onSaved(`${secret.ref} updated`);
      } else {
        const body: CreateSecretRequest = {
          workspace: form.workspace || 'default',
          ref: form.ref.trim(),
          description: optionalString(form.description),
          providerType: form.providerType,
          providerConnectionId: optionalString(form.providerConnectionId),
          providerRef: optionalString(form.providerRef),
          value: isDaguManaged ? form.value : undefined,
        };
        const { error: apiError } = await client.POST('/secrets', {
          params: { query: { remoteNode } },
          body,
        });
        if (apiError)
          throw new Error(apiError.message || 'Failed to create secret');
        onSaved(`${body.ref} created`);
      }
      setForm(initialFormState(defaultWorkspace));
    } catch (err) {
      setError(errorMessage(err, 'Failed to save secret'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{isEditing ? 'Edit Secret' : 'Add Secret'}</DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="mt-2 space-y-4">
          {error && (
            <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}

          <div className="grid gap-4 md:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="secret-workspace">Workspace</Label>
              <Input
                id="secret-workspace"
                value={form.workspace}
                disabled={isEditing}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    workspace: event.target.value,
                  }))
                }
                autoComplete="off"
                className="h-9"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="secret-provider">Provider</Label>
              <Select
                value={form.providerType}
                disabled={isEditing}
                onValueChange={(value) =>
                  setForm((current) => ({
                    ...current,
                    providerType: value as SecretProviderType,
                    value: '',
                    providerRef: '',
                    providerConnectionId: '',
                  }))
                }
              >
                <SelectTrigger id="secret-provider" className="h-9 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PROVIDERS.map((provider) => (
                    <SelectItem key={provider} value={provider}>
                      {PROVIDER_LABELS[provider]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="secret-ref">Ref</Label>
            <Input
              id="secret-ref"
              value={form.ref}
              disabled={isEditing}
              onChange={(event) =>
                setForm((current) => ({ ...current, ref: event.target.value }))
              }
              placeholder="prod/db-password"
              autoComplete="off"
              className="h-9"
            />
          </div>

          {isDaguManaged && !isEditing ? (
            <div className="space-y-1.5">
              <Label htmlFor="secret-value">Value</Label>
              <Input
                id="secret-value"
                value={form.value}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    value: event.target.value,
                  }))
                }
                type="password"
                autoComplete="new-password"
                className="h-9"
              />
            </div>
          ) : null}

          {!isDaguManaged ? (
            <div className="grid gap-4 md:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="secret-provider-connection">Connection</Label>
                <Input
                  id="secret-provider-connection"
                  value={form.providerConnectionId}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      providerConnectionId: event.target.value,
                    }))
                  }
                  autoComplete="off"
                  className="h-9"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="secret-provider-ref">Provider Ref</Label>
                <Input
                  id="secret-provider-ref"
                  value={form.providerRef}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      providerRef: event.target.value,
                    }))
                  }
                  autoComplete="off"
                  className="h-9"
                />
              </div>
            </div>
          ) : null}

          <div className="space-y-1.5">
            <Label htmlFor="secret-description">Description</Label>
            <Textarea
              id="secret-description"
              value={form.description}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  description: event.target.value,
                }))
              }
              className="min-h-20"
            />
          </div>

          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RotateSecretDialog({
  secret,
  remoteNode,
  onClose,
  onSaved,
}: {
  secret: SecretResponse | null;
  remoteNode: string;
  onClose: () => void;
  onSaved: (message: string) => void;
}): React.ReactElement {
  const client = useClient();
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    setValue('');
    setError(null);
  }, [secret]);

  async function handleSubmit(event: React.FormEvent): Promise<void> {
    event.preventDefault();
    if (!secret) return;
    if (value === '') {
      setError('Value is required');
      return;
    }

    setIsSaving(true);
    setError(null);
    try {
      const { error: apiError } = await client.POST(
        '/secrets/{secretId}/versions',
        {
          params: { path: { secretId: secret.id }, query: { remoteNode } },
          body: { value },
        }
      );
      if (apiError)
        throw new Error(apiError.message || 'Failed to rotate secret');
      setValue('');
      onSaved(`${secret.ref} rotated`);
    } catch (err) {
      setError(errorMessage(err, 'Failed to rotate secret'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <Dialog open={!!secret} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Rotate Secret</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="mt-2 space-y-4">
          {error && (
            <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="rotate-secret-value">Value</Label>
            <Input
              id="rotate-secret-value"
              value={value}
              onChange={(event) => setValue(event.target.value)}
              type="password"
              autoComplete="new-password"
              className="h-9"
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              Rotate
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
