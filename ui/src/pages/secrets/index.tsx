// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  CreateSecretRequestProviderType,
  SecretProviderType,
  SecretStatus,
} from '@/api/v1/schema';
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
import { whenEnabled } from '@/hooks/queryUtils';
import dayjs from '@/lib/dayjs';
import {
  WorkspaceKind,
  workspaceNameForSelection,
  sanitizeWorkspaceSelection,
} from '@/lib/workspace';
import {
  ExternalLink,
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
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type SecretResponse = components['schemas']['SecretResponse'];
type CreateSecretRequest = components['schemas']['CreateSecretRequest'];
type UpdateSecretRequest = components['schemas']['UpdateSecretRequest'];

type SecretFormState = {
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
  [SecretProviderType.gcp]: 'Google Secret Manager',
  [SecretProviderType.aws]: 'AWS Secrets Manager',
  [SecretProviderType.azure]: 'Azure Key Vault',
  [SecretProviderType.alibaba]: 'Alibaba Cloud KMS',
};

const PROVIDERS = Object.values(SecretProviderType);
const EXTERNAL_SECRET_REQUEST_URL = 'https://dagu.sh/contact';
const SECRET_GLOBAL_SCOPE = 'global';

function isRequestBasedProvider(provider: SecretProviderType): boolean {
  return provider !== SecretProviderType.dagu_managed;
}

function providerOptionLabel(provider: SecretProviderType): React.ReactElement {
  const isRequestBased = isRequestBasedProvider(provider);
  return (
    <span className="flex w-full items-center justify-between gap-3">
      <span>{PROVIDER_LABELS[provider]}</span>
      {isRequestBased && (
        <Badge variant="outline" className="h-4 px-1.5 text-[10px]">
          <I18nText text={'By request'} />
        </Badge>
      )}
    </span>
  );
}

function initialFormState(): SecretFormState {
  return {
    ref: '',
    description: '',
    providerType: SecretProviderType.dagu_managed,
    providerConnectionId: '',
    providerRef: '',
    value: '',
  };
}

function secretScopeForSelection(
  selection: React.ContextType<typeof AppBarContext>['workspaceSelection']
): string {
  const sanitized = sanitizeWorkspaceSelection(selection);
  if (sanitized.kind === WorkspaceKind.workspace) {
    return workspaceNameForSelection(sanitized);
  }
  return SECRET_GLOBAL_SCOPE;
}

function formStateFromSecret(secret: SecretResponse): SecretFormState {
  return {
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

function isProfileBackingSecret(secret: SecretResponse): boolean {
  return (
    secret.ref.startsWith('runtime-profiles/') ||
    secret.ref.startsWith('runtime-profile-defaults/')
  );
}

export function SecretRefsSection(): React.ReactNode {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelectionScope = useMemo(
    () => secretScopeForSelection(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
  const [selectedScope, setSelectedScope] = useState(workspaceSelectionScope);
  const canManageSecrets = useCanManageSecrets(selectedScope);
  const workspaceQuery = useMemo(
    () => ({ workspace: selectedScope }),
    [selectedScope]
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
    setSelectedScope(workspaceSelectionScope);
  }, [workspaceSelectionScope]);

  const { data, mutate, isLoading } = useQuery(
    '/secrets',
    whenEnabled(canManageSecrets, {
      params: {
        query: {
          remoteNode,
          ...workspaceQuery,
          limit: 500,
        },
      },
    })
  );

  const secrets = (data?.secrets || []).filter(
    (secret) => !isProfileBackingSecret(secret)
  );

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

  return (
    <section
      id="secret-refs"
      aria-labelledby="secret-refs-heading"
      className="flex h-full min-h-0 flex-col gap-4 overflow-hidden"
    >
      <div className="flex shrink-0 items-center justify-between gap-3">
        <div>
          <h2 id="secret-refs-heading" className="text-base font-semibold">
            <I18nText text={'DAG Secret Refs'} />
          </h2>
          <p className="text-sm text-muted-foreground">
            <I18nTemplate
              text="Individual secrets that DAGs reference with {reference}."
              values={{
                reference: <code className="text-xs">secrets[].ref</code>,
              }}
            />
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={selectedScope} onValueChange={setSelectedScope}>
            <I18nProps>
              <SelectTrigger
                className="h-7 w-[180px]"
                aria-label="Secret scope"
              >
                <SelectValue />
              </SelectTrigger>
            </I18nProps>
            <SelectContent>
              <SelectItem value={SECRET_GLOBAL_SCOPE}>
                <I18nText text={'Global'} />
              </SelectItem>
              {(appBarContext.workspaces ?? []).map((workspace) => (
                <SelectItem key={workspace.id} value={workspace.name}>
                  {workspace.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            size="sm"
            className="h-8"
            disabled={!canManageSecrets}
            onClick={() => {
              setEditingSecret(null);
              setIsFormOpen(true);
            }}
          >
            <Plus className="mr-1.5 h-4 w-4" />
            <I18nText text={'Add Secret Ref'} />
          </Button>
        </div>
      </div>

      {error && (
        <div className="shrink-0 rounded-md bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}
      {success && (
        <div className="shrink-0 rounded-md bg-success/10 p-3 text-sm text-success">
          {success}
        </div>
      )}

      <div className="card-obsidian min-h-0 flex-1 !overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[320px]">
                <I18nText text={'Ref'} />
              </TableHead>
              <TableHead className="w-[180px]">
                <I18nText text={'Provider'} />
              </TableHead>
              <TableHead className="w-[110px]">
                <I18nText text={'Status'} />
              </TableHead>
              <TableHead className="w-[90px]">
                <I18nText text={'Version'} />
              </TableHead>
              <TableHead className="w-[170px]">
                <I18nText text={'Rotated'} />
              </TableHead>
              <TableHead className="w-[80px]"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {!canManageSecrets ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText
                    text={
                      'You do not have permission to access this secret scope.'
                    }
                  />
                </TableCell>
              </TableRow>
            ) : isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText text={'Loading secret refs...'} />
                </TableCell>
              </TableRow>
            ) : secrets.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText text={'No secret refs found.'} />
                </TableCell>
              </TableRow>
            ) : (
              secrets.map((secret) => (
                <TableRow key={secret.id}>
                  <TableCell>
                    <div className="flex min-w-0 flex-col gap-1">
                      <div className="flex min-w-0 items-center gap-2">
                        <KeyRound className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                        <code className="whitespace-normal break-words text-xs">
                          {secret.ref}
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
                      <span className="text-muted-foreground">
                        <I18nText text={'None'} />
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {secret.lastRotatedAt ? (
                      dayjs(secret.lastRotatedAt).format('MMM D, YYYY HH:mm')
                    ) : (
                      <I18nText text={'Never'} />
                    )}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Actions for ${secret.ref}`}
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
                          <I18nText text={'Edit'} />
                        </DropdownMenuItem>
                        {secret.providerType ===
                          SecretProviderType.dagu_managed && (
                          <DropdownMenuItem
                            onClick={() => setRotatingSecret(secret)}
                          >
                            <RefreshCw className="mr-2 h-4 w-4" />
                            <I18nText text={'Rotate'} />
                          </DropdownMenuItem>
                        )}
                        <DropdownMenuItem onClick={() => toggleStatus(secret)}>
                          <Power className="mr-2 h-4 w-4" />
                          {secret.status === SecretStatus.active ? (
                            <I18nText text={'Disable'} />
                          ) : (
                            <I18nText text={'Enable'} />
                          )}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          className="text-destructive"
                          onClick={() => setDeletingSecret(secret)}
                        >
                          <Trash2 className="mr-2 h-4 w-4" />
                          <I18nText text={'Delete'} />
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
        selectedScope={selectedScope}
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

      <I18nProps>
        <ConfirmModal
          title="Delete Secret Ref"
          buttonText="Delete"
          visible={!!deletingSecret}
          dismissModal={() => setDeletingSecret(null)}
          onSubmit={deleteSecret}
        >
          <span className="text-sm text-muted-foreground">
            {deletingSecret ? (
              <I18nText
                text="Delete {name}?"
                values={{ name: deletingSecret.ref }}
              />
            ) : (
              ''
            )}
          </span>
        </ConfirmModal>
      </I18nProps>
    </section>
  );
}

function SecretFormDialog({
  open,
  secret,
  selectedScope,
  remoteNode,
  onClose,
  onSaved,
}: {
  open: boolean;
  secret: SecretResponse | null;
  selectedScope: string;
  remoteNode: string;
  onClose: () => void;
  onSaved: (message: string) => void;
}): React.ReactElement {
  const client = useClient();
  const isEditing = !!secret;
  const [form, setForm] = useState<SecretFormState>(() => initialFormState());
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      setForm(initialFormState());
      setError(null);
      return;
    }
    setForm(secret ? formStateFromSecret(secret) : initialFormState());
    setError(null);
  }, [open, secret]);

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
    if (!isEditing && isRequestBasedProvider(form.providerType)) {
      setError('External secret providers are available by request');
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
          workspace: selectedScope,
          ref: form.ref.trim(),
          description: optionalString(form.description),
          providerType: CreateSecretRequestProviderType.dagu_managed,
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
      setForm(initialFormState());
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
          <DialogTitle>
            {isEditing ? (
              <I18nText text={'Edit Secret Ref'} />
            ) : (
              <I18nText text={'Add Secret Ref'} />
            )}
          </DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="mt-2 space-y-4">
          {error && (
            <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="secret-provider">
              <I18nText text={'Provider'} />
            </Label>
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
                  <SelectItem
                    key={provider}
                    value={provider}
                    disabled={isRequestBasedProvider(provider)}
                  >
                    {providerOptionLabel(provider)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {!isEditing && (
              <p className="text-xs text-muted-foreground">
                <I18nText
                  text={'External providers are available by request.'}
                />{' '}
                <a
                  href={EXTERNAL_SECRET_REQUEST_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center font-medium text-primary underline-offset-4 hover:underline"
                >
                  <I18nText text={'Request access'} />
                  <ExternalLink className="ml-1 h-3 w-3" aria-hidden />
                </a>
              </p>
            )}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="secret-ref">
              <I18nText text={'Ref'} />
            </Label>
            <I18nProps>
              <Input
                id="secret-ref"
                value={form.ref}
                disabled={isEditing}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    ref: event.target.value,
                  }))
                }
                placeholder="prod/db-password"
                autoComplete="off"
                className="h-9"
              />
            </I18nProps>
          </div>

          {isDaguManaged && !isEditing ? (
            <div className="space-y-1.5">
              <Label htmlFor="secret-value">
                <I18nText text={'Value'} />
              </Label>
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
                <Label htmlFor="secret-provider-connection">
                  <I18nText text={'Connection'} />
                </Label>
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
                <Label htmlFor="secret-provider-ref">
                  <I18nText text={'Provider Ref'} />
                </Label>
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
            <Label htmlFor="secret-description">
              <I18nText text={'Description'} />
            </Label>
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
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              <I18nText text={'Save'} />
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
          <DialogTitle>
            <I18nText text={'Rotate Secret'} />
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="mt-2 space-y-4">
          {error && (
            <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="rotate-secret-value">
              <I18nText text={'Value'} />
            </Label>
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
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isSaving}>
              {isSaving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              <I18nText text={'Rotate'} />
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
