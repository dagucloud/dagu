import { components, RemoteNodeResponseSource } from '@/api/v1/schema';
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
import { TOKEN_KEY } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  CheckCircle2,
  Globe,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  Trash2,
  Unplug,
  XCircle,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useState } from 'react';
import { RemoteNodeFormModal } from './RemoteNodeFormModal';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type RemoteNodeResponse = components['schemas']['RemoteNodeResponse'];
type TestResult = { success: boolean; message?: string; error?: string };

export default function RemoteNodesPage() {
  const { ts } = useI18n();
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const [remoteNodes, setRemoteNodes] = useState<RemoteNodeResponse[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Modal states
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingNode, setEditingNode] = useState<RemoteNodeResponse | null>(
    null
  );
  const [deletingNode, setDeletingNode] = useState<RemoteNodeResponse | null>(
    null
  );
  const [testingNodeId, setTestingNodeId] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Map<string, TestResult>>(
    new Map()
  );

  useEffect(() => {
    appBarContext.setTitle('Remote Nodes');
  }, [appBarContext]);

  const fetchRemoteNodes = useCallback(async () => {
    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/remote-nodes?remoteNode=${remoteNode}`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to fetch remote nodes');
      }

      const data = await response.json();
      setRemoteNodes(data.remoteNodes || []);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to load remote nodes'
      );
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, appBarContext.selectedRemoteNode]);

  useEffect(() => {
    fetchRemoteNodes();
  }, [fetchRemoteNodes]);

  const refreshRemoteNodeNames = useCallback(async () => {
    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/remote-nodes?remoteNode=${remoteNode}`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );
      if (response.ok) {
        const data = await response.json();
        const nodes: RemoteNodeResponse[] = data.remoteNodes || [];
        const names = ['local', ...nodes.map((n) => n.name)];
        const unique = [...new Set(names)];
        appBarContext.setRemoteNodes(unique);
      }
    } catch {
      // ignore
    }
  }, [config.apiURL, appBarContext]);

  const handleDeleteNode = async () => {
    if (!deletingNode) return;

    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/remote-nodes/${deletingNode.id}?remoteNode=${remoteNode}`,
        {
          method: 'DELETE',
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to delete remote node');
      }

      setError(null);
      setDeletingNode(null);
      fetchRemoteNodes();
      refreshRemoteNodeNames();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to delete remote node'
      );
    }
  };

  const handleTestConnection = async (node: RemoteNodeResponse) => {
    setTestingNodeId(node.id);
    setTestResults((prev) => {
      const next = new Map(prev);
      next.delete(node.id);
      return next;
    });

    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/remote-nodes/${node.id}/test-connection?remoteNode=${remoteNode}`,
        {
          method: 'POST',
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      const data = await response.json().catch(() => ({
        success: false,
        error: 'Failed to parse response',
      }));

      setTestResults((prev) => new Map(prev).set(node.id, data));
    } catch (err) {
      setTestResults((prev) =>
        new Map(prev).set(node.id, {
          success: false,
          error: err instanceof Error ? err.message : 'Connection failed',
        })
      );
    } finally {
      setTestingNodeId(null);
    }
  };

  const isStoreNode = (node: RemoteNodeResponse) =>
    node.source === RemoteNodeResponseSource.store;

  return (
    <div className="flex flex-col gap-4 max-w-7xl">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">
            <I18nText text={'Remote Nodes'} />
          </h1>
          <p className="text-sm text-muted-foreground">
            <I18nText text={'Manage connections to remote Dagu instances'} />
          </p>
        </div>
        <Button
          onClick={() => setShowCreateModal(true)}
          size="sm"
          className="h-8"
        >
          <Plus className="h-4 w-4 mr-1.5" />
          <I18nText text={'Add Node'} />
        </Button>
      </div>

      {error && (
        <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md">
          {error}
        </div>
      )}

      <div className="card-obsidian overflow-auto min-h-0">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[180px]">
                <I18nText text={'Name'} />
              </TableHead>
              <TableHead className="w-[280px]">
                <I18nText text={'API URL'} />
              </TableHead>
              <TableHead className="w-[80px]">
                <I18nText text={'Auth'} />
              </TableHead>
              <TableHead className="w-[80px]">
                <I18nText text={'Source'} />
              </TableHead>
              <TableHead className="w-[150px]">
                <I18nText text={'Created'} />
              </TableHead>
              <TableHead className="w-[120px]">
                <I18nText text={'Status'} />
              </TableHead>
              <TableHead className="w-[80px]"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="text-center text-muted-foreground py-8"
                >
                  <I18nText text={'Loading remote nodes...'} />
                </TableCell>
              </TableRow>
            ) : remoteNodes.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="text-center text-muted-foreground py-8"
                >
                  <I18nText text={'No remote nodes configured'} />
                </TableCell>
              </TableRow>
            ) : (
              remoteNodes.map((node) => {
                const result = testResults.get(node.id);
                const isTesting = testingNodeId === node.id;

                return (
                  <TableRow key={node.id}>
                    <TableCell className="font-medium">
                      <div className="flex items-center gap-2">
                        <Globe className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                        <div className="min-w-0">
                          <div className="truncate">{node.name}</div>
                          {node.description && (
                            <div className="text-muted-foreground font-normal text-[11px] whitespace-normal break-words">
                              {node.description}
                            </div>
                          )}
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground font-mono text-xs max-w-[280px] truncate">
                      {node.apiBaseUrl}
                    </TableCell>
                    <TableCell>
                      <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                        {node.authType}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-xs px-1.5 py-0.5 rounded ${
                          isStoreNode(node)
                            ? 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400'
                            : 'bg-muted text-muted-foreground'
                        }`}
                      >
                        {node.source}
                      </span>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {node.createdAt
                        ? dayjs(node.createdAt).format('MMM D, YYYY HH:mm')
                        : '-'}
                    </TableCell>
                    <TableCell>
                      {isTesting ? (
                        <span className="flex items-center gap-1 text-muted-foreground">
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                          <I18nText text={'Testing...'} />
                        </span>
                      ) : result ? (
                        result.success ? (
                          <span className="flex items-center gap-1 text-green-600 dark:text-green-400">
                            <CheckCircle2 className="h-3.5 w-3.5" />
                            <I18nText text={'OK'} />
                          </span>
                        ) : (
                          <span
                            className="flex items-center gap-1 text-red-600 dark:text-red-400"
                            title={result.error || result.message}
                          >
                            <XCircle className="h-3.5 w-3.5" />
                            <I18nText text={'Failed'} />
                          </span>
                        )
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={ts('Actions')}
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onClick={() => handleTestConnection(node)}
                            disabled={isTesting}
                          >
                            <Unplug className="h-4 w-4 mr-2" />
                            <I18nText text={'Test Connection'} />
                          </DropdownMenuItem>
                          {isStoreNode(node) && (
                            <DropdownMenuItem
                              onClick={() => setEditingNode(node)}
                            >
                              <Pencil className="h-4 w-4 mr-2" />
                              <I18nText text={'Edit'} />
                            </DropdownMenuItem>
                          )}
                          {isStoreNode(node) && (
                            <DropdownMenuItem
                              onClick={() => setDeletingNode(node)}
                              className="text-destructive"
                            >
                              <Trash2 className="h-4 w-4 mr-2" />
                              <I18nText text={'Delete'} />
                            </DropdownMenuItem>
                          )}
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

      {/* Create Modal */}
      <RemoteNodeFormModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSuccess={() => {
          setShowCreateModal(false);
          fetchRemoteNodes();
          refreshRemoteNodeNames();
        }}
      />

      {/* Edit Modal */}
      <RemoteNodeFormModal
        open={!!editingNode}
        node={editingNode || undefined}
        onClose={() => setEditingNode(null)}
        onSuccess={() => {
          setEditingNode(null);
          fetchRemoteNodes();
          refreshRemoteNodeNames();
        }}
      />

      {/* Delete Confirmation */}
      <I18nProps>
        <ConfirmModal
          title="Delete Remote Node"
          buttonText="Delete"
          visible={!!deletingNode}
          dismissModal={() => setDeletingNode(null)}
          onSubmit={handleDeleteNode}
        >
          <p>
            {ts(
              'Are you sure you want to delete remote node "{name}"? This action cannot be undone.',
              { name: deletingNode?.name ?? '' }
            )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}
