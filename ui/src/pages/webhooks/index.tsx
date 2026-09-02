import {
  components,
  WebhookAuthMode as WebhookAuthModeValue,
  WebhookHMACEnforcementMode as WebhookHMACEnforcementModeValue,
} from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AppBarContext } from '@/contexts/AppBarContext';
import { TOKEN_KEY, useCanManageWebhooks } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  Check,
  Copy,
  ExternalLink,
  MoreHorizontal,
  RefreshCw,
  Trash2,
  Webhook,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

type WebhookDetails = components['schemas']['WebhookDetails'];
type WebhookAuthMode = components['schemas']['WebhookAuthMode'];

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

function authModeVariant(
  webhook: WebhookDetails
): React.ComponentProps<typeof Badge>['variant'] {
  if (webhook.authMode === WebhookAuthModeValue.hmac_only) {
    return 'warning';
  }
  if (webhook.authMode === WebhookAuthModeValue.token_and_hmac) {
    return webhook.hmac.enforcementMode ===
      WebhookHMACEnforcementModeValue.observe
      ? 'info'
      : 'primary';
  }
  return 'default';
}

export default function WebhooksPage() {
  const config = useConfig();
  const canManageWebhooks = useCanManageWebhooks();
  const navigate = useNavigate();
  const appBarContext = useContext(AppBarContext);
  const [webhooks, setWebhooks] = useState<WebhookDetails[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Modal states
  const [deletingWebhook, setDeletingWebhook] = useState<WebhookDetails | null>(
    null
  );
  const [regeneratingWebhook, setRegeneratingWebhook] =
    useState<WebhookDetails | null>(null);
  const [togglingWebhook, setTogglingWebhook] = useState<{
    webhook: WebhookDetails;
    enabled: boolean;
  } | null>(null);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  // Set page title
  useEffect(() => {
    appBarContext.setTitle('Webhooks');
  }, [appBarContext]);

  const getAuthHeaders = useCallback(() => {
    const token = localStorage.getItem(TOKEN_KEY);
    return {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    };
  }, []);

  const getRemoteNodeParam = useCallback(() => {
    return appBarContext.selectedRemoteNode || 'local';
  }, [appBarContext.selectedRemoteNode]);

  const fetchWebhooks = useCallback(async () => {
    try {
      const remoteNode = getRemoteNodeParam();
      const response = await fetch(
        `${config.apiURL}/webhooks?remoteNode=${remoteNode}`,
        { headers: getAuthHeaders() }
      );

      if (!response.ok) {
        throw new Error('Failed to fetch webhooks');
      }

      const data = await response.json();
      setWebhooks(data.webhooks || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load webhooks');
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, getAuthHeaders, getRemoteNodeParam]);

  useEffect(() => {
    fetchWebhooks();
  }, [fetchWebhooks]);

  const handleToggleClick = (webhook: WebhookDetails, enabled: boolean) => {
    setTogglingWebhook({ webhook, enabled });
  };

  const handleToggleConfirm = async () => {
    if (!togglingWebhook) return;
    try {
      setError(null);
      const remoteNode = getRemoteNodeParam();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(togglingWebhook.webhook.dagName)}/webhook/toggle?remoteNode=${remoteNode}`,
        {
          method: 'POST',
          headers: getAuthHeaders(),
          body: JSON.stringify({ enabled: togglingWebhook.enabled }),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to toggle webhook');
      }

      fetchWebhooks();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to toggle webhook');
    } finally {
      setTogglingWebhook(null);
    }
  };

  const handleRegenerate = async () => {
    if (!regeneratingWebhook) return;

    try {
      setError(null);
      const remoteNode = getRemoteNodeParam();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(regeneratingWebhook.dagName)}/webhook/regenerate?remoteNode=${remoteNode}`,
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
      setNewToken(data.token);
      fetchWebhooks();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to regenerate token'
      );
      setRegeneratingWebhook(null);
    }
  };

  const handleDelete = async () => {
    if (!deletingWebhook) return;

    try {
      const remoteNode = getRemoteNodeParam();
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(deletingWebhook.dagName)}/webhook?remoteNode=${remoteNode}`,
        {
          method: 'DELETE',
          headers: getAuthHeaders(),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to delete webhook');
      }

      setError(null);
      setDeletingWebhook(null);
      fetchWebhooks();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete webhook');
    }
  };

  const handleCopyToken = async () => {
    if (!newToken) return;
    try {
      await navigator.clipboard.writeText(newToken);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard API may be unavailable in some contexts (e.g., HTTP, permissions)
    }
  };

  const handleCloseTokenModal = () => {
    setNewToken(null);
    setRegeneratingWebhook(null);
    setCopied(false);
  };

  const navigateToDAG = (dagName: string) => {
    navigate(`/dags/${encodeURIComponent(dagName)}?tab=webhook`);
  };

  if (!canManageWebhooks) {
    return (
      <div className="flex items-center justify-center h-64">
        <p className="text-muted-foreground">
          You do not have permission to access this page.
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4 max-w-7xl">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Webhooks</h1>
          <p className="text-sm text-muted-foreground">
            Manage webhooks across all DAGs
          </p>
        </div>
      </div>

      {error && (
        <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md">
          {error}
        </div>
      )}

      <div className="card-obsidian overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[250px]">DAG</TableHead>
              <TableHead className="w-[150px]">Token</TableHead>
              <TableHead className="w-[160px]">Auth</TableHead>
              <TableHead className="w-[140px]">Profiles</TableHead>
              <TableHead className="w-[100px]">Status</TableHead>
              <TableHead className="w-[180px]">Created</TableHead>
              <TableHead className="w-[180px]">Last Triggered</TableHead>
              <TableHead className="w-[80px]"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={8}
                  className="text-center text-muted-foreground py-8"
                >
                  Loading webhooks...
                </TableCell>
              </TableRow>
            ) : webhooks.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={8}
                  className="text-center text-muted-foreground py-8"
                >
                  No webhooks found. Create webhooks from individual DAG pages.
                </TableCell>
              </TableRow>
            ) : (
              webhooks.map((webhook) => (
                <TableRow key={webhook.id}>
                  <TableCell className="font-medium">
                    <button
                      onClick={() => navigateToDAG(webhook.dagName)}
                      className="flex items-center gap-2 hover:underline text-left"
                    >
                      <Webhook className="h-3.5 w-3.5 text-muted-foreground" />
                      {webhook.dagName}
                    </button>
                  </TableCell>
                  <TableCell>
                    <code className="text-xs bg-muted px-1.5 py-0.5 rounded">
                      {webhook.tokenPrefix}...
                    </code>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col gap-1">
                      <Badge variant={authModeVariant(webhook)}>
                        {formatWebhookAuthMode(webhook.authMode)}
                      </Badge>
                      {webhook.authMode ===
                        WebhookAuthModeValue.token_and_hmac &&
                        webhook.hmac.enforcementMode ===
                          WebhookHMACEnforcementModeValue.observe && (
                          <span className="text-[11px] text-muted-foreground">
                            Observe mode
                          </span>
                        )}
                    </div>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {webhook.profileSelection.allowedProfiles.length === 0
                      ? 'Default only'
                      : `${webhook.profileSelection.allowedProfiles.length} selectable`}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Switch
                        checked={webhook.enabled}
                        onCheckedChange={(checked) =>
                          handleToggleClick(webhook, checked)
                        }
                      />
                      <span className="text-xs text-muted-foreground">
                        {webhook.enabled ? 'Enabled' : 'Disabled'}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {dayjs(webhook.createdAt).format('MMM D, YYYY HH:mm')}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {webhook.lastUsedAt
                      ? dayjs(webhook.lastUsedAt).format('MMM D, YYYY HH:mm')
                      : 'Never'}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon">
                          <MoreHorizontal className="h-4 w-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          onClick={() => navigateToDAG(webhook.dagName)}
                        >
                          <ExternalLink className="h-4 w-4 mr-2" />
                          View DAG
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          onClick={() => setRegeneratingWebhook(webhook)}
                        >
                          <RefreshCw className="h-4 w-4 mr-2" />
                          Regenerate Token
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={() => setDeletingWebhook(webhook)}
                          className="text-destructive"
                        >
                          <Trash2 className="h-4 w-4 mr-2" />
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

      {/* Regenerate Token Confirmation / Token Display */}
      {regeneratingWebhook && !newToken && (
        <ConfirmModal
          title="Regenerate Token"
          buttonText="Regenerate"
          visible={true}
          dismissModal={() => setRegeneratingWebhook(null)}
          onSubmit={handleRegenerate}
        >
          <p>
            Are you sure you want to regenerate the token for &quot;
            {regeneratingWebhook.dagName}&quot;? The old token will immediately
            stop working.
          </p>
        </ConfirmModal>
      )}

      {/* New Token Display Modal */}
      <Dialog open={!!newToken} onOpenChange={() => handleCloseTokenModal()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Token Regenerated</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="p-3 bg-warning/10 border border-warning/20 rounded-md">
              <p className="text-sm text-foreground">
                Copy this token now. You won&apos;t be able to see it again!
              </p>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 p-2 text-sm bg-muted rounded-md break-all font-mono">
                {newToken}
              </code>
              <Button variant="outline" size="icon" onClick={handleCopyToken}>
                {copied ? (
                  <Check className="h-4 w-4" />
                ) : (
                  <Copy className="h-4 w-4" />
                )}
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={handleCloseTokenModal}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Confirmation */}
      <ConfirmModal
        title="Delete Webhook"
        buttonText="Delete"
        visible={!!deletingWebhook}
        dismissModal={() => setDeletingWebhook(null)}
        onSubmit={handleDelete}
      >
        <p>
          Are you sure you want to delete the webhook for &quot;
          {deletingWebhook?.dagName}&quot;? Any applications using this token
          will immediately lose access.
        </p>
      </ConfirmModal>

      {/* Toggle Confirmation */}
      <ConfirmModal
        title={togglingWebhook?.enabled ? 'Enable Webhook' : 'Disable Webhook'}
        buttonText={togglingWebhook?.enabled ? 'Enable' : 'Disable'}
        visible={!!togglingWebhook}
        dismissModal={() => setTogglingWebhook(null)}
        onSubmit={handleToggleConfirm}
      >
        <p>
          Are you sure you want to{' '}
          {togglingWebhook?.enabled ? 'enable' : 'disable'} the webhook for
          &quot;{togglingWebhook?.webhook.dagName}&quot;?
        </p>
      </ConfirmModal>
    </div>
  );
}
