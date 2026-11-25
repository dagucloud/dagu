// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { InventoryNodeKind, type components } from '@/api/v1/schema';
import { EmptyState, InlineLoadError } from '@/components/PageStates';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { LabelCombobox } from '@/components/ui/label-combobox';
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
import { AppBarContext } from '@/contexts/AppBarContext';
import { Tab, Tabs } from '@/components/ui/tabs';
import {
  useCanEditInventories,
  useCanTestNodes,
  useCanTrustNodeHostKeys,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useClient, useQuery } from '@/hooks/api';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { Server } from 'lucide-react';
import React, { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { AddNodeDialog } from './AddNodeDialog';
import { InventoriesView } from './inventories/InventoriesView';
import { ImportDialog } from './inventories/ImportDialog';
import { InventoryDialog } from './inventories/InventoryDialog';
import { NodeDetailsDrawer } from './NodeDetailsDrawer';
import { NodeTestPanel } from './NodeTestPanel';
import { nodeKey, useNodeTests } from './useNodeTests';

type InventoryNode = components['schemas']['InventoryNode'];
type ConnectionTest = components['schemas']['InventoryNodeConnectionTest'];

const ALL_INVENTORIES = 'all';
const NODES_PER_PAGE = 50;
const NAME_DEBOUNCE_MS = 300;
const COLUMN_COUNT = 5;
// The most nodes one test takes, as the API does.
const MAX_TEST_NODES = 1000;
// The most nodes GET /nodes returns per page.
const MATCHED_PAGE_SIZE = 200;

/**
 * Lists the nodes of this server's inventories and its worker nodes,
 * filtered by inventory, labels, and name. Remote nodes do not serve their
 * inventories, so the page lists only this server's nodes.
 */
function NodesPage(): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode;
  const isRemote = !!remoteNode && remoteNode !== 'local';

  useEffect(() => {
    appBarContext.setTitle('Nodes');
  }, [appBarContext]);

  if (isRemote) {
    return (
      <EmptyState
        headingLevel={1}
        icon={<Server />}
        title={<I18nText text={'Nodes are kept on each server'} />}
        description={
          <I18nText
            text={
              'Remote nodes do not serve their inventories. Select the local server to see its nodes.'
            }
          />
        }
      />
    );
  }
  return <NodeList />;
}

function NodeList(): React.ReactElement {
  const [inventory, setInventory] = useState(ALL_INVENTORIES);
  const [labels, setLabels] = useState<string[]>([]);
  const [name, setName] = useState('');
  const searchName = useDebouncedValue(name.trim(), NAME_DEBOUNCE_MS);
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<InventoryNode | null>(null);
  // Counts the reads of the drawer's node, so only the latest is kept.
  const reloadsRef = useRef(0);
  // Names of the nodes checked for a test.
  const [checked, setChecked] = useState<ReadonlySet<string>>(new Set());
  // Whether the names of the matched nodes are being read for a test.
  const [listingMatched, setListingMatched] = useState(false);
  const { ts } = useI18n();
  const config = useConfig();
  const client = useClient();
  const canTest = useCanTestNodes();
  const canTrust = useCanTrustNodeHostKeys();
  const canEdit = useCanEditInventories();
  const canForget = useIsAdmin();
  const tests = useNodeTests(config.apiURL);
  const [view, setView] = useState<'nodes' | 'inventories'>('nodes');
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const [addingNode, setAddingNode] = useState(false);

  // A change of filter starts again from the first page, with no node
  // checked.
  useEffect(() => {
    setPage(1);
    setChecked(new Set());
  }, [inventory, labels, searchName]);

  const filter = {
    labels: labels.length > 0 ? labels.join(',') : undefined,
    inventory: inventory === ALL_INVENTORIES ? undefined : inventory,
    name: searchName || undefined,
  };
  const inventories = useQuery('/inventories', {});
  const nodeLabels = useQuery('/node-labels', {});
  const { data, error, isLoading, isValidating, mutate } = useQuery(
    '/nodes',
    { params: { query: { page, perPage: NODES_PER_PAGE, ...filter } } },
    { keepPreviousData: true }
  );

  const inventoryList = inventories.data?.inventories ?? [];
  const nodes = data?.nodes ?? [];
  // Only SSH nodes are tested over a connection.
  const testable = nodes.filter((node) => node.kind === InventoryNodeKind.ssh);
  // A failed request still returns the previous filter's data, whose count
  // and pages do not describe the current filter.
  const pagination = error ? undefined : data?.pagination;
  // While a new filter loads, the rows and pages shown are the previous
  // request's, so paging from them would ask for the wrong page.
  const canPage =
    !!pagination && pagination.currentPage === page && !isValidating;
  /**
   * Reads the drawer's node again after a change, since the filters may no
   * longer list it.
   */
  const reloadSelected = async () => {
    if (!selected) {
      return;
    }
    const reload = ++reloadsRef.current;
    try {
      const { data } = await client.GET('/nodes/{nodeName}', {
        params: { path: { nodeName: selected.name } },
      });
      // A later reload, a closed drawer, or another node supersedes this
      // read.
      if (data && reload === reloadsRef.current) {
        setSelected((current) =>
          current && nodeKey(current.name) === nodeKey(data.name)
            ? data
            : current
        );
      }
    } catch {
      // The drawer keeps the node as it was read before.
    }
  };

  // The drawer shows the node as the list now holds it, so an edit shows at
  // once; a node the list no longer holds shows as it was last read.
  const shown =
    selected &&
    (nodes.find((node) => nodeKey(node.name) === nodeKey(selected.name)) ??
      selected);
  const availableLabels = useMemo(
    () => nodeLabels.data?.labels ?? [],
    [nodeLabels.data?.labels]
  );
  const columnCount = canTest ? COLUMN_COUNT + 1 : COLUMN_COUNT;
  const pageChecked = testable.filter((node) => checked.has(node.name)).length;

  const setPageChecked = (value: boolean) => {
    setChecked((prev) => {
      const next = new Set(prev);
      for (const node of testable) {
        if (value) {
          next.add(node.name);
        } else {
          next.delete(node.name);
        }
      }
      return next;
    });
  };

  const toggleChecked = (name: string, value: boolean) => {
    setChecked((prev) => {
      const next = new Set(prev);
      if (value) {
        next.add(name);
      } else {
        next.delete(name);
      }
      return next;
    });
  };

  /** Tests the checked nodes, or else every node the filters match. */
  const startTest = async () => {
    if (checked.size > 0) {
      await tests.test([...checked]);
      return;
    }
    setListingMatched(true);
    try {
      const names = await listMatched();
      if (names) {
        await tests.test(names);
      }
    } finally {
      setListingMatched(false);
    }
  };

  /**
   * Returns the names of every SSH node the filters match, a page at a time,
   * or null after it reports why they cannot be tested.
   */
  const listMatched = async (): Promise<string[] | null> => {
    const names: string[] = [];
    for (let next = 1; ; next++) {
      const matched = await client.GET('/nodes', {
        params: {
          query: { page: next, perPage: MATCHED_PAGE_SIZE, ...filter },
        },
      });
      if (!matched.data) {
        tests.refuse(
          matched.error?.message ?? ts("Couldn't list the matched nodes.")
        );
        return null;
      }
      if (matched.data.pagination.totalRecords > MAX_TEST_NODES) {
        tests.refuse(
          ts(
            'A test takes at most 1,000 nodes. Narrow the filters, or check the nodes to test.'
          )
        );
        return null;
      }
      names.push(
        ...matched.data.nodes
          .filter((node) => node.kind === InventoryNodeKind.ssh)
          .map((node) => node.name)
      );
      if (!matched.data.pagination.hasNextPage || !matched.data.nodes.length) {
        if (names.length === 0) {
          tests.refuse(ts('No SSH node matches these filters.'));
          return null;
        }
        return names;
      }
    }
  };

  /** Reads again what a change to the inventories affects. */
  const refresh = () => {
    void inventories.mutate();
    void nodeLabels.mutate();
    void mutate();
  };

  if (inventories.data && inventoryList.length === 0 && data?.nodeCount === 0) {
    return (
      <>
        <EmptyState
          headingLevel={1}
          icon={<Server />}
          title={<I18nText text={'No nodes yet'} />}
          description={
            <I18nTemplate
              text="Import an inventory of SSH hosts with {command}."
              values={{
                command: <code className="text-xs">dagu nodes import</code>,
              }}
            />
          }
          action={
            canEdit ? (
              <div className="flex gap-2">
                <Button size="sm" onClick={() => setCreating(true)}>
                  <I18nText text={'New inventory'} />
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setImporting(true)}
                >
                  <I18nText text={'Import'} />
                </Button>
              </div>
            ) : undefined
          }
        />
        {creating && (
          <InventoryDialog
            onClose={() => setCreating(false)}
            onSaved={refresh}
          />
        )}
        {importing && (
          <ImportDialog
            onClose={() => setImporting(false)}
            onImported={refresh}
          />
        )}
      </>
    );
  }

  const heading = (
    <div>
      <h1 className="text-2xl font-semibold tracking-[-0.01em]">
        <I18nText text={'Nodes'} />
      </h1>
      <p className="text-sm text-muted-foreground">
        <I18nText
          text={
            "SSH hosts from this server's inventories, and workers that set an ID. Steps select them by label."
          }
        />
      </p>
      <Tabs className="mt-2">
        <Tab isActive={view === 'nodes'} onClick={() => setView('nodes')}>
          <I18nText text={'Nodes'} />
        </Tab>
        <Tab
          isActive={view === 'inventories'}
          onClick={() => setView('inventories')}
        >
          <I18nText text={'Inventories'} />
        </Tab>
      </Tabs>
    </div>
  );

  if (view === 'inventories') {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-4 p-4">
        {heading}
        {inventories.error && (
          <FilterLoadError
            what="Couldn't load inventories."
            error={inventories.error}
            onRetry={() => void inventories.mutate()}
          />
        )}
        <InventoriesView
          inventories={inventoryList}
          canEdit={canEdit}
          onChanged={refresh}
        />
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-4">
      {heading}

      <div className="flex flex-wrap items-center gap-2">
        <Select value={inventory} onValueChange={setInventory}>
          <I18nProps>
            <SelectTrigger className="h-8 w-[200px]" aria-label="Inventory">
              <SelectValue />
            </SelectTrigger>
          </I18nProps>
          <SelectContent>
            <SelectItem value={ALL_INVENTORIES}>
              <I18nText text={'All inventories'} />
            </SelectItem>
            {inventoryList.map((item) => (
              <SelectItem key={item.id} value={item.id}>
                {item.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <LabelCombobox
          className="w-[280px]"
          selectedLabels={labels}
          onLabelsChange={setLabels}
          availableLabels={availableLabels}
        />
        <I18nProps>
          <Input
            className="h-8 w-[200px]"
            placeholder="Search by name..."
            aria-label="Search by name..."
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </I18nProps>
        {pagination && data && (
          <span className="ml-auto text-xs text-muted-foreground">
            <I18nTemplate
              text="{matched} of {total} nodes"
              values={{
                matched: pagination.totalRecords,
                total: data.nodeCount,
              }}
            />
          </span>
        )}
        {canTest && (
          <Button
            size="sm"
            variant="outline"
            className={pagination ? 'h-8' : 'ml-auto h-8'}
            disabled={
              listingMatched ||
              tests.running ||
              tests.trusting ||
              (checked.size === 0 && !pagination?.totalRecords)
            }
            onClick={() => void startTest()}
          >
            {checked.size > 0 ? (
              <I18nTemplate
                text="Test {count} checked"
                values={{ count: checked.size }}
              />
            ) : (
              <I18nText text={'Test matched nodes'} />
            )}
          </Button>
        )}
        {canEdit && inventoryList.length > 0 && (
          <Button size="sm" className="h-8" onClick={() => setAddingNode(true)}>
            <I18nText text={'Add node'} />
          </Button>
        )}
      </div>
      {inventories.error && (
        <FilterLoadError
          what="Couldn't load inventories."
          error={inventories.error}
          onRetry={() => void inventories.mutate()}
        />
      )}
      {nodeLabels.error && (
        <FilterLoadError
          what="Couldn't load node labels."
          error={nodeLabels.error}
          onRetry={() => void nodeLabels.mutate()}
        />
      )}
      {data?.workerConflicts.map((conflict) => (
        <p key={conflict.worker} className="text-xs text-warning">
          <I18nTemplate
            text="Worker {worker} is not a node: inventory {inventory} has a node with that name."
            values={{
              worker: <code>{conflict.worker}</code>,
              inventory: conflict.inventory.name,
            }}
          />
        </p>
      ))}
      {tests.run && (
        <NodeTestPanel
          run={tests.run}
          results={tests.runResults}
          trusts={tests.trusts}
          trusting={tests.trusting}
          canTrust={canTrust}
          onTrust={(offers) => void tests.trust(offers, tests.run!.names)}
          onClose={tests.close}
        />
      )}

      <div className="card-obsidian min-h-0 flex-1 !overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              {canTest && (
                <TableHead className="w-8">
                  <I18nProps>
                    <Checkbox
                      aria-label="Check the nodes of this page"
                      checked={
                        pageChecked === 0
                          ? false
                          : pageChecked === testable.length
                            ? true
                            : 'indeterminate'
                      }
                      disabled={testable.length === 0 || isValidating}
                      onCheckedChange={(value) =>
                        setPageChecked(value === true)
                      }
                    />
                  </I18nProps>
                </TableHead>
              )}
              <TableHead className="w-[220px]">
                <I18nText text={'Name'} />
              </TableHead>
              <TableHead className="w-[180px]">
                <I18nText text={'Inventory'} />
              </TableHead>
              <TableHead className="w-[70px]">
                <I18nText text={'Kind'} />
              </TableHead>
              <TableHead className="w-[200px]">
                <I18nText text={'Host'} />
              </TableHead>
              <TableHead>
                <I18nText text={'Labels'} />
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {error ? (
              <TableRow>
                <TableCell colSpan={columnCount} className="py-8">
                  <InlineLoadError
                    error={error}
                    onRetry={() => void mutate()}
                  />
                </TableCell>
              </TableRow>
            ) : isLoading && !data ? (
              <TableRow>
                <TableCell
                  colSpan={columnCount}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText text={'Loading nodes...'} />
                </TableCell>
              </TableRow>
            ) : nodes.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={columnCount}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText text={'No nodes match these filters.'} />
                </TableCell>
              </TableRow>
            ) : (
              nodes.map((node) => (
                <NodeRow
                  key={node.name}
                  node={node}
                  test={
                    tests.run?.pending.has(nodeKey(node.name))
                      ? 'pending'
                      : tests.results[nodeKey(node.name)]
                  }
                  checked={canTest ? checked.has(node.name) : undefined}
                  // The rows of the previous filter stay while a new one
                  // loads; a node checked then would be tested unseen.
                  checkDisabled={isValidating}
                  onCheckedChange={(value) => toggleChecked(node.name, value)}
                  onOpen={() => setSelected(node)}
                />
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {pagination && pagination.totalPages > 1 && (
        <div className="flex items-center justify-end gap-2 text-xs">
          <Button
            size="sm"
            variant="outline"
            disabled={!canPage || pagination.currentPage <= 1}
            onClick={() => setPage(pagination.prevPage)}
          >
            <I18nText text={'Previous'} />
          </Button>
          <span className="text-muted-foreground">
            <I18nTemplate
              text="Page {current} of {total}"
              values={{
                current: pagination.currentPage,
                total: pagination.totalPages,
              }}
            />
          </span>
          <Button
            size="sm"
            variant="outline"
            disabled={!canPage || !pagination.hasNextPage}
            onClick={() => setPage(pagination.nextPage)}
          >
            <I18nText text={'Next'} />
          </Button>
        </div>
      )}

      {addingNode && (
        <AddNodeDialog
          inventories={inventoryList}
          onClose={() => setAddingNode(false)}
          onAdded={refresh}
        />
      )}
      <NodeDetailsDrawer
        node={shown}
        lastTest={selected ? tests.results[nodeKey(selected.name)] : undefined}
        canEdit={canEdit}
        canForget={canForget}
        onChanged={() => {
          refresh();
          void reloadSelected();
        }}
        onRemoved={() => {
          setSelected(null);
          refresh();
        }}
        open={selected !== null}
        onOpenChange={(open) => !open && setSelected(null)}
      />
    </div>
  );
}

/** A filter's choices failed to load. The nodes stay listed. */
function FilterLoadError({
  what,
  error,
  onRetry,
}: {
  what: string;
  error: unknown;
  onRetry: () => void;
}): React.ReactElement {
  return (
    <div className="flex flex-wrap items-center gap-x-2 text-xs text-error">
      <I18nText text={what} />
      <InlineLoadError error={error} onRetry={onRetry} />
    </div>
  );
}

function NodeRow({
  node,
  test,
  checked,
  checkDisabled,
  onCheckedChange,
  onOpen,
}: {
  node: InventoryNode;
  /** The node's last test result, or 'pending' while it is tested. */
  test?: ConnectionTest | 'pending';
  /** Whether the node is checked, or undefined when nodes cannot be. */
  checked?: boolean;
  checkDisabled: boolean;
  onCheckedChange: (checked: boolean) => void;
  onOpen: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const isSSH = node.kind === InventoryNodeKind.ssh;
  return (
    <TableRow
      className="cursor-pointer"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          onOpen();
        }
      }}
    >
      {checked !== undefined && (
        // The row opens the drawer; checking a node does not.
        <TableCell
          onClick={(event) => event.stopPropagation()}
          onKeyDown={(event) => event.stopPropagation()}
        >
          {isSSH && (
            <Checkbox
              aria-label={ts('Check {name}', { name: node.name })}
              checked={checked}
              disabled={checkDisabled}
              onCheckedChange={(value) => onCheckedChange(value === true)}
            />
          )}
        </TableCell>
      )}
      <TableCell>
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="truncate font-medium">{node.name}</span>
          {test === 'pending' ? (
            <Badge variant="outline">
              <I18nText text={'Testing...'} />
            </Badge>
          ) : test ? (
            <Badge variant={test.ok ? 'success' : 'error'}>
              {test.ok ? (
                <I18nText text={'Passed'} />
              ) : (
                <I18nText text={'Failed'} />
              )}
            </Badge>
          ) : null}
          {node.disabled && (
            <Badge variant="outline">
              <I18nText text={'Disabled'} />
            </Badge>
          )}
          {isSSH && !node.strictHostKey && (
            <Badge variant="warning">
              <I18nText text={'Any host key'} />
            </Badge>
          )}
          {!isSSH && !node.online && (
            <Badge variant="outline">
              <I18nText text={'Offline'} />
            </Badge>
          )}
        </div>
      </TableCell>
      <TableCell className="truncate">{node.inventory?.name ?? '—'}</TableCell>
      <TableCell className="font-mono">{node.kind}</TableCell>
      <TableCell className="truncate font-mono">{hostOf(node)}</TableCell>
      <TableCell>
        <div className="flex flex-wrap gap-1">
          {node.labels.map((label) => (
            <Badge key={label} variant="outline" className="font-mono">
              {label}
            </Badge>
          ))}
        </div>
      </TableCell>
    </TableRow>
  );
}

/** The node's host, with its port when it is not the SSH default. */
function hostOf(node: InventoryNode): string {
  return node.port && node.port !== 22
    ? `${node.host}:${node.port}`
    : node.host;
}

export default NodesPage;
