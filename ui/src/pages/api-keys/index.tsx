import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AppBarContext } from '@/contexts/AppBarContext';
import { TOKEN_KEY, useIsAdmin } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  AlertTriangle,
  KeyRound,
  MoreHorizontal,
  Pencil,
  Plus,
  Trash2,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useState } from 'react';
import { APIKeyFormModal } from './APIKeyFormModal';

type APIKey = components['schemas']['APIKey'];
const COMMUNITY_API_KEY_LIMIT = 2;

export default function APIKeysPage() {
  const config = useConfig();
  const isAdmin = useIsAdmin();
  const appBarContext = useContext(AppBarContext);
  const [apiKeys, setApiKeys] = useState<APIKey[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Modal states
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKey | null>(null);
  const [deletingKey, setDeletingKey] = useState<APIKey | null>(null);
  const hasActiveLicense = config.license.valid || config.license.gracePeriod;
  const communityLimitReached =
    !hasActiveLicense && apiKeys.length >= COMMUNITY_API_KEY_LIMIT;

  // Set page title
  useEffect(() => {
    appBarContext.setTitle('API Keys');
  }, [appBarContext]);

  const fetchAPIKeys = useCallback(async () => {
    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = appBarContext.selectedRemoteNode || 'local';
      const response = await fetch(
        `${config.apiURL}/api-keys?remoteNode=${remoteNode}`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to fetch API keys');
      }

      const data = await response.json();
      setApiKeys(data.apiKeys || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load API keys');
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, appBarContext.selectedRemoteNode]);

  useEffect(() => {
    fetchAPIKeys();
  }, [fetchAPIKeys]);

  const handleDeleteKey = async () => {
    if (!deletingKey) return;

    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = appBarContext.selectedRemoteNode || 'local';
      const response = await fetch(
        `${config.apiURL}/api-keys/${deletingKey.id}?remoteNode=${remoteNode}`,
        {
          method: 'DELETE',
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to delete API key');
      }

      setError(null);
      setDeletingKey(null);
      fetchAPIKeys();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete API key');
    }
  };

  if (!isAdmin) {
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
          <h1 className="text-lg font-semibold">API Keys</h1>
          <p className="text-sm text-muted-foreground">
            Manage API keys for programmatic access
          </p>
        </div>
        <Button
          onClick={() => setShowCreateModal(true)}
          size="sm"
          className="h-8"
          disabled={communityLimitReached}
        >
          <Plus className="h-4 w-4 mr-1.5" />
          Create API Key
        </Button>
      </div>

      {error && (
        <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md">
          {error}
        </div>
      )}

      {communityLimitReached && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 p-3 text-sm text-warning-foreground"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <div>
            <p className="font-medium">
              Community installs can manage up to 2 API keys.
            </p>
            <p className="mt-1">
              Existing keys remain active, but new key creation is blocked until
              extra keys are revoked or a license is configured.
            </p>
          </div>
        </div>
      )}

      <div className="card-obsidian overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[200px]">Name</TableHead>
              <TableHead className="w-[120px]">Role</TableHead>
              <TableHead className="w-[100px]">Key Prefix</TableHead>
              <TableHead className="w-[180px]">Created</TableHead>
              <TableHead className="w-[180px]">Last Used</TableHead>
              <TableHead className="w-[80px]"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="text-center text-muted-foreground py-8"
                >
                  Loading API keys...
                </TableCell>
              </TableRow>
            ) : apiKeys.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className="text-center text-muted-foreground py-8"
                >
                  No API keys found. Create one to get started.
                </TableCell>
              </TableRow>
            ) : (
              apiKeys.map((key) => (
                <TableRow key={key.id}>
                  <TableCell className="font-medium">
                    <div className="flex flex-col">
                      <div className="flex items-center gap-2">
                        <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
                        {key.name}
                      </div>
                      {key.description && (
                        <span className="text-xs text-muted-foreground ml-5">
                          {key.description}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground capitalize">
                      {key.role}
                    </span>
                  </TableCell>
                  <TableCell>
                    <code className="text-xs bg-muted px-1.5 py-0.5 rounded">
                      {key.keyPrefix}...
                    </code>
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {dayjs(key.createdAt).format('MMM D, YYYY HH:mm')}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {key.lastUsedAt
                      ? dayjs(key.lastUsedAt).format('MMM D, YYYY HH:mm')
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
                        <DropdownMenuItem onClick={() => setEditingKey(key)}>
                          <Pencil className="h-4 w-4 mr-2" />
                          Edit
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={() => setDeletingKey(key)}
                          className="text-destructive"
                        >
                          <Trash2 className="h-4 w-4 mr-2" />
                          Revoke
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

      {/* Create API Key Modal */}
      <APIKeyFormModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSuccess={() => {
          setShowCreateModal(false);
          fetchAPIKeys();
        }}
      />

      {/* Edit API Key Modal */}
      <APIKeyFormModal
        open={!!editingKey}
        apiKey={editingKey || undefined}
        onClose={() => setEditingKey(null)}
        onSuccess={() => {
          setEditingKey(null);
          fetchAPIKeys();
        }}
      />

      {/* Delete Confirmation */}
      <ConfirmModal
        title="Revoke API Key"
        buttonText="Revoke"
        visible={!!deletingKey}
        dismissModal={() => setDeletingKey(null)}
        onSubmit={handleDeleteKey}
      >
        <p>
          Are you sure you want to revoke the API key &quot;{deletingKey?.name}
          &quot;? Any applications using this key will immediately lose access.
        </p>
      </ConfirmModal>
    </div>
  );
}
