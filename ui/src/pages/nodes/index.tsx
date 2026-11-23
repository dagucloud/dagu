// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { EmptyState, InlineLoadError } from '@/components/PageStates';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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
import { useQuery } from '@/hooks/api';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { Server } from 'lucide-react';
import React, { useContext, useEffect, useMemo, useState } from 'react';
import { NodeDetailsDrawer } from './NodeDetailsDrawer';

type InventoryNode = components['schemas']['InventoryNode'];

const ALL_INVENTORIES = 'all';
const NODES_PER_PAGE = 50;
const NAME_DEBOUNCE_MS = 300;
const COLUMN_COUNT = 5;

/**
 * Lists the nodes of this server's inventories, filtered by inventory,
 * labels, and name. Remote nodes do not serve their inventories, so the page
 * lists only this server's nodes.
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

  // A change of filter starts again from the first page.
  useEffect(() => {
    setPage(1);
  }, [inventory, labels, searchName]);

  const inventories = useQuery('/inventories', {});
  const nodeLabels = useQuery('/node-labels', {});
  const { data, error, isLoading, isValidating, mutate } = useQuery(
    '/nodes',
    {
      params: {
        query: {
          page,
          perPage: NODES_PER_PAGE,
          labels: labels.length > 0 ? labels.join(',') : undefined,
          inventory: inventory === ALL_INVENTORIES ? undefined : inventory,
          name: searchName || undefined,
        },
      },
    },
    { keepPreviousData: true }
  );

  const inventoryList = inventories.data?.inventories ?? [];
  const totalNodes = inventoryList.reduce(
    (sum, item) => sum + item.nodeCount,
    0
  );
  const nodes = data?.nodes ?? [];
  // A failed request still returns the previous filter's data, whose count
  // and pages do not describe the current filter.
  const pagination = error ? undefined : data?.pagination;
  // While a new filter loads, the rows and pages shown are the previous
  // request's, so paging from them would ask for the wrong page.
  const canPage =
    !!pagination && pagination.currentPage === page && !isValidating;
  const availableLabels = useMemo(
    () => nodeLabels.data?.labels ?? [],
    [nodeLabels.data?.labels]
  );

  if (inventories.data && inventoryList.length === 0) {
    return (
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
      />
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-[-0.01em]">
          <I18nText text={'Nodes'} />
        </h1>
        <p className="text-sm text-muted-foreground">
          <I18nText
            text={
              "SSH hosts from this server's inventories. Steps select them by label."
            }
          />
        </p>
      </div>

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
        {pagination && (
          <span className="ml-auto text-xs text-muted-foreground">
            {inventories.data ? (
              <I18nTemplate
                text="{matched} of {total} nodes"
                values={{ matched: pagination.totalRecords, total: totalNodes }}
              />
            ) : (
              <I18nTemplate
                text="{count} nodes"
                values={{ count: pagination.totalRecords }}
              />
            )}
          </span>
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

      <div className="card-obsidian min-h-0 flex-1 !overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
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
                <TableCell colSpan={COLUMN_COUNT} className="py-8">
                  <InlineLoadError
                    error={error}
                    onRetry={() => void mutate()}
                  />
                </TableCell>
              </TableRow>
            ) : isLoading && !data ? (
              <TableRow>
                <TableCell
                  colSpan={COLUMN_COUNT}
                  className="py-8 text-center text-muted-foreground"
                >
                  <I18nText text={'Loading nodes...'} />
                </TableCell>
              </TableRow>
            ) : nodes.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={COLUMN_COUNT}
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

      <NodeDetailsDrawer
        node={selected}
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
  onOpen,
}: {
  node: InventoryNode;
  onOpen: () => void;
}): React.ReactElement {
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
      <TableCell>
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="truncate font-medium">{node.name}</span>
          {node.disabled && (
            <Badge variant="outline">
              <I18nText text={'Disabled'} />
            </Badge>
          )}
          {!node.strictHostKey && (
            <Badge variant="warning">
              <I18nText text={'Any host key'} />
            </Badge>
          )}
        </div>
      </TableCell>
      <TableCell className="truncate">{node.inventory.name}</TableCell>
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
export function hostOf(node: InventoryNode): string {
  return node.port && node.port !== 22
    ? `${node.host}:${node.port}`
    : node.host;
}

export default NodesPage;
