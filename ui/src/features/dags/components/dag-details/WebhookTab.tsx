// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * WebhookTab component displays webhook configuration for a DAG.
 * Webhooks are managed separately from DAG configuration.
 *
 * @module features/dags/components/dag-details
 */
import {
  AlertTriangle,
  Check,
  Copy,
  Loader2,
  Plus,
  RefreshCw,
  Terminal,
  Trash2,
  Webhook,
  WebhookOff,
} from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import {
  components,
  WebhookAuthMode as WebhookAuthModeValue,
  WebhookHMACConfigureRequestAuthMode as WebhookHMACAuthModeValue,
  WebhookHMACEnforcementMode as WebhookHMACEnforcementModeValue,
} from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { TOKEN_KEY, useIsAdmin } from '../../../../contexts/AuthContext';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useClient } from '../../../../hooks/api';
import dayjs from '../../../../lib/dayjs';
import ConfirmModal from '@/components/ui/confirm-dialog';
import WebhookProfileSelectionCard from './WebhookProfileSelectionCard';
import {
  buildWebhookExamples,
  findActiveAllowedProfile,
} from './webhookProfileSelection';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type WebhookDetails = components['schemas']['WebhookDetails'];
type WebhookAuthMode = components['schemas']['WebhookAuthMode'];
type WebhookHMACAuthMode =
  components['schemas']['WebhookHMACConfigureRequest']['authMode'];
type WebhookHMACEnforcementMode =
  components['schemas']['WebhookHMACEnforcementMode'];

interface WebhookTabProps {
  fileName: string;
}

interface CopyButtonProps {
  copied: boolean;
  onCopy: () => void;
  label?: string;
}

function CopyButton({ copied, onCopy, label }: CopyButtonProps) {
  return (
    <Button
      variant="ghost"
      size="sm"
      className="h-6 px-2 text-muted-foreground hover:text-foreground"
      onClick={onCopy}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      {label && <span className="ml-1 text-xs">{label}</span>}
    </Button>
  );
}

function formatWebhookAuthMode(mode: WebhookAuthMode): string {
  switch (mode) {
    case WebhookAuthModeValue.token_and_hmac:
      return 'Token + HMAC';
    case WebhookAuthModeValue.hmac_only:
      return 'HMAC only';
    case WebhookAuthModeValue.token_only:
    default:
      return 'Token only';
  }
}

function toHMACAuthMode(mode: WebhookAuthMode): WebhookHMACAuthMode {
  switch (mode) {
    case WebhookAuthModeValue.token_and_hmac:
      return WebhookHMACAuthModeValue.token_and_hmac;
    case WebhookAuthModeValue.hmac_only:
      return WebhookHMACAuthModeValue.hmac_only;
    case WebhookAuthModeValue.token_only:
    default:
      throw new Error('HMAC auth mode cannot be token only');
  }
}

function WebhookTab({ fileName }: WebhookTabProps) {
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const client = useClient();
  const isAdmin = useIsAdmin();

  // State
  const [webhook, setWebhook] = useState<WebhookDetails | null>(null);
  const [secretReveal, setSecretReveal] = useState<{
    kind: 'token' | 'hmac';
    value: string;
  } | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isActioning, setIsActioning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [showToggleConfirm, setShowToggleConfirm] = useState(false);
  const [pendingToggleState, setPendingToggleState] = useState<boolean | null>(
    null
  );
  const [activeProfileNames, setActiveProfileNames] = useState<string[]>([]);

  // Copy states
  const [copiedUrl, setCopiedUrl] = useState(false);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [copiedCurl, setCopiedCurl] = useState(false);
  const [copiedHMACShell, setCopiedHMACShell] = useState(false);
  const [copiedHMACNode, setCopiedHMACNode] = useState(false);
  const [draftAuthMode, setDraftAuthMode] = useState<WebhookAuthMode>(
    WebhookAuthModeValue.token_only
  );
  const [draftEnforcementMode, setDraftEnforcementMode] =
    useState<WebhookHMACEnforcementMode>(
      WebhookHMACEnforcementModeValue.strict
    );

  // Construct webhook URL (include remoteNode if not local)
  const webhookUrl =
    remoteNode && remoteNode !== 'local'
      ? `${window.location.origin}/api/v1/webhooks/${encodeURIComponent(fileName)}?remoteNode=${encodeURIComponent(remoteNode)}`
      : `${window.location.origin}/api/v1/webhooks/${encodeURIComponent(fileName)}`;

  // API helpers
  const getAuthHeaders = useCallback(() => {
    const token = localStorage.getItem(TOKEN_KEY);
    return {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    };
  }, []);

  const getRemoteNodeParam = useCallback(() => {
    return remoteNode || 'local';
  }, [remoteNode]);

  const getRemoteNodeQueryValue = useCallback(() => {
    return encodeURIComponent(getRemoteNodeParam());
  }, [getRemoteNodeParam]);

  // Fetch webhook
  const fetchWebhook = useCallback(async () => {
    try {
      setIsLoading(true);
      setError(null);
      const remoteNodeParam = getRemoteNodeQueryValue();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/webhook?remoteNode=${remoteNodeParam}`,
        { headers: getAuthHeaders() }
      );

      if (response.status === 404) {
        setWebhook(null);
        return;
      }

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to fetch webhook');
      }

      const data = await response.json();
      setWebhook(data);
    } catch (err) {
      if (err instanceof Error && err.message.includes('404')) {
        setWebhook(null);
      } else {
        setError(err instanceof Error ? err.message : 'Failed to load webhook');
      }
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, fileName, getAuthHeaders, getRemoteNodeQueryValue]);

  useEffect(() => {
    fetchWebhook();
  }, [fetchWebhook]);

  useEffect(() => {
    if (!webhook) {
      setDraftAuthMode(WebhookAuthModeValue.token_only);
      setDraftEnforcementMode(WebhookHMACEnforcementModeValue.strict);
      return;
    }

    setDraftAuthMode(webhook.authMode);
    setDraftEnforcementMode(
      webhook.hmac.enforcementMode || WebhookHMACEnforcementModeValue.strict
    );
  }, [webhook]);

  // Create webhook
  const handleCreate = async () => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNodeParam = getRemoteNodeQueryValue();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/webhook?remoteNode=${remoteNodeParam}`,
        {
          method: 'POST',
          headers: getAuthHeaders(),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to create webhook');
      }

      const data = await response.json();
      setWebhook(data.webhook);
      setSecretReveal({ kind: 'token', value: data.token });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create webhook');
    } finally {
      setIsActioning(false);
    }
  };

  // Delete webhook
  const handleDelete = async () => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNodeParam = getRemoteNodeQueryValue();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/webhook?remoteNode=${remoteNodeParam}`,
        {
          method: 'DELETE',
          headers: getAuthHeaders(),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to delete webhook');
      }

      setWebhook(null);
      setSecretReveal(null);
      setShowDeleteConfirm(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete webhook');
    } finally {
      setIsActioning(false);
    }
  };

  // Regenerate token
  const handleRegenerate = async () => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNodeParam = getRemoteNodeQueryValue();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/webhook/regenerate?remoteNode=${remoteNodeParam}`,
        {
          method: 'POST',
          headers: getAuthHeaders(),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to regenerate token');
      }

      const data = await response.json();
      setWebhook(data.webhook);
      setSecretReveal({ kind: 'token', value: data.token });
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to regenerate token'
      );
    } finally {
      setIsActioning(false);
    }
  };

  // Toggle enabled - show confirmation first
  const handleToggleClick = (enabled: boolean) => {
    setPendingToggleState(enabled);
    setShowToggleConfirm(true);
  };

  const handleToggleConfirm = async () => {
    if (pendingToggleState === null) return;
    try {
      setError(null);
      const remoteNodeParam = getRemoteNodeQueryValue();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/webhook/toggle?remoteNode=${remoteNodeParam}`,
        {
          method: 'POST',
          headers: getAuthHeaders(),
          body: JSON.stringify({ enabled: pendingToggleState }),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to toggle webhook');
      }

      const data = await response.json();
      setWebhook(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to toggle webhook');
    } finally {
      setShowToggleConfirm(false);
      setPendingToggleState(null);
    }
  };

  const handleToggleCancel = () => {
    setShowToggleConfirm(false);
    setPendingToggleState(null);
  };

  // Copy handler
  const handleCopy = async (text: string, setCopied: (v: boolean) => void) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API might not be available
    }
  };

  const handleEnableHMAC = async (authMode: WebhookHMACAuthMode) => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNode = getRemoteNodeParam();
      const enforcementMode =
        authMode === WebhookHMACAuthModeValue.hmac_only
          ? WebhookHMACEnforcementModeValue.strict
          : draftEnforcementMode;
      const { data, error: apiError } = await client.POST(
        '/dags/{fileName}/webhook/hmac/enable',
        {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
          body: {
            authMode,
            enforcementMode,
          },
        }
      );

      if (apiError || !data) {
        throw new Error(apiError?.message || 'Failed to enable HMAC');
      }

      setWebhook(data.webhook);
      setDraftAuthMode(data.webhook.authMode);
      setDraftEnforcementMode(
        data.webhook.hmac.enforcementMode ||
          WebhookHMACEnforcementModeValue.strict
      );
      setSecretReveal({ kind: 'hmac', value: data.hmacSecret });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to enable HMAC');
    } finally {
      setIsActioning(false);
    }
  };

  const handleSaveHMACSettings = async () => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNode = getRemoteNodeParam();
      const { data, error: apiError } = await client.POST(
        '/dags/{fileName}/webhook/hmac/configure',
        {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
          body: {
            authMode: toHMACAuthMode(draftAuthMode),
            enforcementMode:
              draftAuthMode === WebhookAuthModeValue.hmac_only
                ? WebhookHMACEnforcementModeValue.strict
                : draftEnforcementMode,
          },
        }
      );

      if (apiError || !data) {
        throw new Error(apiError?.message || 'Failed to update HMAC settings');
      }

      setWebhook(data);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to update HMAC settings'
      );
    } finally {
      setIsActioning(false);
    }
  };

  const handleDisableHMAC = async () => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNode = getRemoteNodeParam();
      const { data, error: apiError } = await client.POST(
        '/dags/{fileName}/webhook/hmac/disable',
        {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
        }
      );

      if (apiError || !data) {
        throw new Error(apiError?.message || 'Failed to disable HMAC');
      }

      setWebhook(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to disable HMAC');
    } finally {
      setIsActioning(false);
    }
  };

  const handleRegenerateHMAC = async () => {
    try {
      setIsActioning(true);
      setError(null);
      const remoteNode = getRemoteNodeParam();
      const { data, error: apiError } = await client.POST(
        '/dags/{fileName}/webhook/hmac/regenerate',
        {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
        }
      );

      if (apiError || !data) {
        throw new Error(
          apiError?.message || 'Failed to regenerate HMAC secret'
        );
      }

      setWebhook(data.webhook);
      setSecretReveal({ kind: 'hmac', value: data.hmacSecret });
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to regenerate HMAC secret'
      );
    } finally {
      setIsActioning(false);
    }
  };

  // Dismiss secret display
  const handleDismissSecret = () => {
    setSecretReveal(null);
  };

  // Loading state
  if (isLoading) {
    return (
      <Card className="max-w-xl gap-0 py-0">
        <CardHeader className="pb-2 px-4 pt-3">
          <div className="flex items-center gap-2">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            <CardTitle className="text-sm">
              <I18nText text={'Loading webhook...'} />
            </CardTitle>
          </div>
        </CardHeader>
      </Card>
    );
  }

  // Error state
  if (error) {
    return (
      <Card className="max-w-xl gap-0 py-0 border-destructive/50">
        <CardHeader className="pb-2 px-4 pt-3">
          <div className="flex items-center gap-2">
            <AlertTriangle className="h-4 w-4 text-destructive" />
            <CardTitle className="text-sm text-destructive">
              <I18nText text={'Error'} />
            </CardTitle>
          </div>
          <CardDescription className="text-xs text-destructive">
            {error}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-4 pb-3 pt-2">
          <Button variant="outline" size="sm" onClick={fetchWebhook}>
            <I18nText text={'Retry'} />
          </Button>
        </CardContent>
      </Card>
    );
  }

  // Token reveal after create/regenerate
  if (secretReveal) {
    const secretLabel =
      secretReveal.kind === 'token' ? 'Webhook Token' : 'Webhook HMAC Secret';
    const secretWarning =
      secretReveal.kind === 'token'
        ? "Copy this token now. You won't be able to see it again!"
        : "Copy this HMAC secret now. You won't be able to see it again!";

    return (
      <Card className="max-w-xl gap-0 py-0">
        <CardHeader className="pb-2 px-4 pt-3">
          <div className="flex items-center gap-2">
            <Webhook className="h-4 w-4 text-muted-foreground" />
            <CardTitle className="text-sm">{secretLabel}</CardTitle>
          </div>
        </CardHeader>
        <CardContent className="px-4 pb-3 pt-3 space-y-3">
          <div className="p-3 bg-warning/10 border border-warning/20 rounded-md">
            <p className="text-sm text-foreground">{secretWarning}</p>
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 p-2 text-xs bg-muted rounded-md break-all font-mono">
              {secretReveal.value}
            </code>
            <Button
              variant="outline"
              size="icon"
              onClick={() => handleCopy(secretReveal.value, setCopiedSecret)}
            >
              {copiedSecret ? (
                <Check className="h-4 w-4" />
              ) : (
                <Copy className="h-4 w-4" />
              )}
            </Button>
          </div>
          <Button variant="default" size="sm" onClick={handleDismissSecret}>
            <I18nText text={'Done'} />
          </Button>
        </CardContent>
      </Card>
    );
  }

  // No webhook configured
  if (!webhook) {
    return (
      <Card className="max-w-xl gap-0 py-0">
        <CardHeader className="pb-2 px-4 pt-3">
          <div className="flex items-center gap-2">
            <WebhookOff className="h-4 w-4 text-muted-foreground" />
            <CardTitle className="text-sm">
              <I18nText text={'No Webhook Configured'} />
            </CardTitle>
          </div>
          <CardDescription className="text-xs">
            <I18nText text={'Create a webhook to trigger this DAG via HTTP'} />
          </CardDescription>
        </CardHeader>
        <CardContent className="px-4 pb-3 pt-3">
          <Button
            variant="default"
            size="sm"
            onClick={handleCreate}
            disabled={isActioning}
          >
            {isActioning ? (
              <Loader2 className="h-4 w-4 mr-1 animate-spin" />
            ) : (
              <Plus className="h-4 w-4 mr-1" />
            )}
            <I18nText text={'Create Webhook'} />
          </Button>
        </CardContent>
      </Card>
    );
  }

  const isHMACEnabled = webhook.authMode !== WebhookAuthModeValue.token_only;
  const configuredAllowedProfiles = webhook.profileSelection.allowedProfiles;
  const exampleProfile = findActiveAllowedProfile(
    configuredAllowedProfiles,
    activeProfileNames
  );
  const {
    curl: curlExample,
    hmacShell: hmacShellExample,
    hmacNode: hmacNodeExample,
  } = buildWebhookExamples({
    authMode: webhook.authMode,
    profileName: exampleProfile,
    webhookUrl,
  });

  // Webhook configured
  return (
    <div className="space-y-3 max-w-2xl">
      {/* Status Card */}
      <Card className="gap-0 py-0">
        <CardHeader className="pb-0 px-4 pt-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Webhook className="h-4 w-4 text-muted-foreground" />
              <CardTitle className="text-sm">
                <I18nText text={'Webhook'} />
              </CardTitle>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">
                {webhook.enabled ? (
                  <I18nText text={'Enabled'} />
                ) : (
                  <I18nText text={'Disabled'} />
                )}
              </span>
              <Switch
                checked={webhook.enabled}
                onCheckedChange={handleToggleClick}
              />
            </div>
          </div>
        </CardHeader>
        <CardContent className="px-4 pb-3 pt-2 space-y-3">
          {/* Endpoint */}
          <div>
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-muted-foreground">
                <I18nText text={'Endpoint'} />
              </span>
              <CopyButton
                copied={copiedUrl}
                onCopy={() => handleCopy(webhookUrl, setCopiedUrl)}
              />
            </div>
            <div className="px-3 py-2 bg-accent rounded-md text-xs font-mono border overflow-x-auto">
              <span className="text-muted-foreground">
                <I18nText text={'POST'} />
              </span>{' '}
              <span>{webhookUrl}</span>
            </div>
          </div>

          {/* Token prefix */}
          <div>
            <div className="flex items-center justify-between mb-1">
              <span className="text-xs text-muted-foreground">
                <I18nText text={'Token'} />
              </span>
            </div>
            <div className="px-3 py-2 bg-accent rounded-md text-xs font-mono border">
              <span>{webhook.tokenPrefix}</span>
              <span className="text-muted-foreground">{'*'.repeat(32)}</span>
            </div>
          </div>

          {/* Metadata */}
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <div>
              <I18nText text={'Auth:'} />{' '}
              {formatWebhookAuthMode(webhook.authMode)}
            </div>
            <div>
              <I18nText text={'Created:'} />{' '}
              {dayjs(webhook.createdAt).format('MMM D, YYYY HH:mm')}
            </div>
            {webhook.lastUsedAt && (
              <div>
                <I18nText text={'Last triggered:'} />{' '}
                {dayjs(webhook.lastUsedAt).format('MMM D, YYYY HH:mm')}
              </div>
            )}
            {webhook.createdBy && (
              <div>
                <I18nText text={'By:'} /> {webhook.createdBy}
              </div>
            )}
          </div>

          {/* Actions */}
          <div className="flex gap-2 pt-1">
            <Button
              variant="outline"
              size="sm"
              onClick={handleRegenerate}
              disabled={isActioning}
            >
              {isActioning ? (
                <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
              ) : (
                <RefreshCw className="h-3.5 w-3.5 mr-1" />
              )}
              <I18nText text={'Regenerate Token'} />
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setShowDeleteConfirm(true)}
              className="text-destructive hover:text-destructive"
              disabled={isActioning}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1" />
              <I18nText text={'Delete'} />
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* Authentication Card */}
      <Card className="gap-0 py-0">
        <CardHeader className="pb-3 px-4 pt-3">
          <div className="flex items-center gap-2">
            <CardTitle className="text-sm">
              <I18nText text={'Authentication'} />
            </CardTitle>
          </div>
          <CardDescription className="text-xs">
            <I18nTemplate
              text="Choose how requests authenticate to this webhook. If you enable HMAC, callers must send {signature} computed from the exact signature input shown below. Requests with {profile} sign {input}."
              values={{
                signature: (
                  <code className="bg-accent px-1 rounded-md border">
                    X-Dagu-Signature: sha256=&lt;hex&gt;
                  </code>
                ),
                profile: (
                  <code className="bg-accent px-1 rounded-md border">
                    X-Dagu-Profile
                  </code>
                ),
                input: (
                  <code className="bg-accent px-1 rounded-md border">
                    x-dagu-profile:&lt;profile&gt;\n&lt;body&gt;
                  </code>
                ),
              }}
            />
          </CardDescription>
        </CardHeader>
        <CardContent className="px-4 pb-3 pt-2 space-y-3">
          {isHMACEnabled ? (
            <>
              <div className="grid gap-3 md:grid-cols-2">
                <div className="space-y-1">
                  <span className="text-xs text-muted-foreground">
                    <I18nText text={'Auth mode'} />
                  </span>
                  <Select
                    value={draftAuthMode}
                    onValueChange={(value) =>
                      setDraftAuthMode(value as WebhookAuthMode)
                    }
                  >
                    <SelectTrigger className="h-9">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={WebhookAuthModeValue.token_and_hmac}>
                        <I18nText text={'Token + HMAC'} />
                      </SelectItem>
                      <SelectItem value={WebhookAuthModeValue.hmac_only}>
                        <I18nText text={'HMAC only'} />
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1">
                  <span className="text-xs text-muted-foreground">
                    <I18nText text={'HMAC enforcement'} />
                  </span>
                  <Select
                    value={
                      draftAuthMode === WebhookAuthModeValue.hmac_only
                        ? WebhookHMACEnforcementModeValue.strict
                        : draftEnforcementMode
                    }
                    onValueChange={(value) =>
                      setDraftEnforcementMode(
                        value as WebhookHMACEnforcementMode
                      )
                    }
                    disabled={draftAuthMode === WebhookAuthModeValue.hmac_only}
                  >
                    <SelectTrigger className="h-9">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem
                        value={WebhookHMACEnforcementModeValue.strict}
                      >
                        <I18nText text={'Strict'} />
                      </SelectItem>
                      <SelectItem
                        value={WebhookHMACEnforcementModeValue.observe}
                      >
                        <I18nText text={'Observe'} />
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>

              <div className="rounded-md border bg-accent/40 px-3 py-2 text-xs text-muted-foreground space-y-1">
                <div>
                  <I18nText text={'Algorithm:'} />{' '}
                  {webhook.hmac.algorithm || 'HMAC-SHA256'}
                </div>
                <div>
                  <I18nText text={'Header:'} />{' '}
                  {webhook.hmac.headerName || 'X-Dagu-Signature'}{' '}
                  {webhook.hmac.format ? (
                    `(${webhook.hmac.format})`
                  ) : (
                    <I18nText text={'(sha256=<hex>)'} />
                  )}
                </div>
                <div>
                  <I18nText text={'Last secret rotation:'} />{' '}
                  {webhook.hmac.updatedAt ? (
                    dayjs(webhook.hmac.updatedAt).format('MMM D, YYYY HH:mm')
                  ) : (
                    <I18nText text={'Not available'} />
                  )}
                </div>
              </div>

              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleSaveHMACSettings}
                  disabled={isActioning}
                >
                  <I18nText text={'Save HMAC Settings'} />
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleRegenerateHMAC}
                  disabled={isActioning}
                >
                  <I18nText text={'Regenerate HMAC Secret'} />
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleDisableHMAC}
                  disabled={isActioning}
                >
                  <I18nText text={'Disable HMAC'} />
                </Button>
              </div>
            </>
          ) : (
            <>
              <div className="rounded-md border bg-accent/40 px-3 py-2 text-xs text-muted-foreground">
                <I18nText
                  text={
                    'This webhook currently accepts the existing token only. HMAC signing is off until you enable it.'
                  }
                />
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    handleEnableHMAC(WebhookHMACAuthModeValue.token_and_hmac)
                  }
                  disabled={isActioning}
                >
                  <I18nText text={'Keep Token and Add HMAC'} />
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    handleEnableHMAC(WebhookHMACAuthModeValue.hmac_only)
                  }
                  disabled={isActioning}
                >
                  <I18nText text={'Use HMAC Only'} />
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      <WebhookProfileSelectionCard
        fileName={fileName}
        isAdmin={isAdmin}
        remoteNode={remoteNode}
        webhook={webhook}
        onActiveProfileNamesChange={setActiveProfileNames}
        onWebhookChange={setWebhook}
      />

      {isHMACEnabled && (
        <Card className="gap-0 py-0">
          <CardHeader className="pb-3 px-4 pt-3">
            <CardTitle className="text-sm">
              <I18nText text={'Generate HMAC'} />
            </CardTitle>
            <CardDescription className="text-xs">
              <I18nText
                text={
                  'Compute the HMAC from the exact signature input shown below. A selected profile is prefixed to the raw request body so the header cannot be changed without invalidating the signature.'
                }
              />
            </CardDescription>
          </CardHeader>
          <CardContent className="px-4 pb-3 pt-2 space-y-3">
            <div className="rounded-md border bg-accent/40 px-3 py-2 text-xs text-muted-foreground">
              <I18nTemplate
                text="Use your webhook HMAC secret as {variable}."
                values={{
                  variable: (
                    <code className="bg-accent px-1 rounded-md border">
                      DAGU_HMAC_SECRET
                    </code>
                  ),
                }}
              />
            </div>

            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-xs font-medium text-muted-foreground">
                  <I18nText text={'Shell (OpenSSL)'} />
                </span>
                <I18nProps>
                  <CopyButton
                    copied={copiedHMACShell}
                    onCopy={() =>
                      handleCopy(hmacShellExample, setCopiedHMACShell)
                    }
                    label="Copy"
                  />
                </I18nProps>
              </div>
              <pre className="px-3 py-2 bg-accent rounded-md text-xs font-mono border overflow-x-auto whitespace-pre-wrap">
                {hmacShellExample}
              </pre>
            </div>

            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-xs font-medium text-muted-foreground">
                  <I18nText text={'Node.js'} />
                </span>
                <I18nProps>
                  <CopyButton
                    copied={copiedHMACNode}
                    onCopy={() =>
                      handleCopy(hmacNodeExample, setCopiedHMACNode)
                    }
                    label="Copy"
                  />
                </I18nProps>
              </div>
              <pre className="px-3 py-2 bg-accent rounded-md text-xs font-mono border overflow-x-auto whitespace-pre-wrap">
                {hmacNodeExample}
              </pre>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Example Card */}
      <Card className="gap-0 py-0">
        <CardHeader className="pb-0 px-4 pt-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Terminal className="h-3.5 w-3.5 text-muted-foreground" />
              <CardTitle className="text-sm">
                <I18nText text={'Example Request'} />
              </CardTitle>
            </div>
            <I18nProps>
              <CopyButton
                copied={copiedCurl}
                onCopy={() => handleCopy(curlExample, setCopiedCurl)}
                label="Copy"
              />
            </I18nProps>
          </div>
        </CardHeader>
        <CardContent className="px-4 pb-3 pt-2">
          <pre className="px-3 py-2 bg-accent rounded-md text-xs font-mono border overflow-x-auto whitespace-pre-wrap">
            {curlExample}
          </pre>
          <ul className="mt-2 text-xs text-muted-foreground space-y-1">
            <li>
              <I18nTemplate
                text="{payload} is available as the {variable} environment variable."
                values={{
                  payload: (
                    <code className="bg-accent px-1 rounded-md border">
                      payload
                    </code>
                  ),
                  variable: (
                    <code className="bg-accent px-1 rounded-md border">
                      WEBHOOK_PAYLOAD
                    </code>
                  ),
                }}
              />
            </li>
            <li>
              <I18nTemplate
                text="Configure {setting} in the DAG YAML. It can also be inherited from {base} to expose selected request headers as {variable}."
                values={{
                  setting: (
                    <code className="bg-accent px-1 rounded-md border">
                      webhook.forward_headers
                    </code>
                  ),
                  base: (
                    <code className="bg-accent px-1 rounded-md border">
                      base.yaml
                    </code>
                  ),
                  variable: (
                    <code className="bg-accent px-1 rounded-md border">
                      WEBHOOK_HEADERS
                    </code>
                  ),
                }}
              />
            </li>
            <li>
              <code className="bg-accent px-1 rounded-md border">dagRunId</code>{' '}
              <I18nText
                text={'(optional) can be used as an idempotency key.'}
              />
            </li>
            <li>
              <I18nTemplate
                text={
                  "{header} is accepted only for profiles approved in this webhook's profile-selection policy."
                }
                values={{
                  header: (
                    <code className="bg-accent px-1 rounded-md border">
                      X-Dagu-Profile
                    </code>
                  ),
                }}
              />
            </li>
            {isHMACEnabled && (
              <li>
                <I18nTemplate
                  text="Sign the exact input shown in the HMAC examples with your secret and send the hex digest in {header}."
                  values={{
                    header: (
                      <code className="bg-accent px-1 rounded-md border">
                        X-Dagu-Signature
                      </code>
                    ),
                  }}
                />
              </li>
            )}
          </ul>
        </CardContent>
      </Card>

      {/* Delete Confirmation Modal */}
      <I18nProps>
        <ConfirmModal
          title="Delete Webhook"
          buttonText="Delete"
          visible={showDeleteConfirm}
          dismissModal={() => setShowDeleteConfirm(false)}
          onSubmit={handleDelete}
        >
          <p>
            <I18nText
              text={
                'Are you sure you want to delete this webhook? Any applications using this webhook token will immediately lose access.'
              }
            />
          </p>
        </ConfirmModal>
      </I18nProps>

      {/* Toggle Confirmation Modal */}
      <I18nProps>
        <ConfirmModal
          title={pendingToggleState ? 'Enable Webhook' : 'Disable Webhook'}
          buttonText={pendingToggleState ? 'Enable' : 'Disable'}
          visible={showToggleConfirm}
          dismissModal={handleToggleCancel}
          onSubmit={handleToggleConfirm}
        >
          <p>
            <I18nText
              text={
                pendingToggleState
                  ? 'Are you sure you want to enable this webhook?'
                  : 'Are you sure you want to disable this webhook?'
              }
            />
          </p>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}

export default WebhookTab;
