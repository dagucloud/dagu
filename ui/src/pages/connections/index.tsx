// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { InlineLoadError } from '@/components/PageStates';
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
import Title from '@/components/ui/title';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import React, { useContext, useEffect } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  ConnectionStatusBadge,
  GLOBAL_WORKSPACE,
  LOCAL_NODE,
  PROVIDER_LABELS,
  RemoteNodeNotice,
  connectionPath,
  workspaceForSelection,
} from './shared';

const COLUMN_COUNT = 4;

/**
 * Lists the connections of one workspace with their provider, account, and
 * status. The workspace comes from the address, else the app bar.
 */
export default function ConnectionsPage(): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const { t } = useI18n();
  const remote =
    !!appBarContext.selectedRemoteNode &&
    appBarContext.selectedRemoteNode !== LOCAL_NODE;

  useEffect(() => {
    appBarContext.setTitle(t('navigation.connections'));
  }, [appBarContext, t]);

  if (remote) return <RemoteNodeNotice />;
  return <ConnectionList />;
}

function ConnectionList(): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const { t, ts } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const workspace =
    searchParams.get('workspace') ||
    workspaceForSelection(appBarContext.workspaceSelection);
  const { data, error, isLoading, mutate } = useQuery('/connections', {
    params: { query: { workspace, remoteNode: LOCAL_NODE } },
  });
  const connections = data?.connections ?? [];

  function message(text: string): React.ReactElement {
    return (
      <TableRow>
        <TableCell
          colSpan={COLUMN_COUNT}
          className="py-8 text-center text-muted-foreground"
        >
          {text}
        </TableCell>
      </TableRow>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-hidden">
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 flex-1 basis-64">
          <Title>{t('navigation.connections')}</Title>
          <p className="text-sm text-muted-foreground">
            {ts(
              'Accounts that DAGs in this workspace use by name. Dagu keeps and refreshes their tokens.'
            )}
          </p>
        </div>
        <Select
          value={workspace}
          onValueChange={(value) => setSearchParams({ workspace: value })}
        >
          <SelectTrigger className="h-7 w-[180px]" aria-label={ts('Workspace')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={GLOBAL_WORKSPACE}>{ts('Global')}</SelectItem>
            {(appBarContext.workspaces ?? []).map((item) => (
              <SelectItem key={item.id} value={item.name}>
                {item.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="card-obsidian min-h-0 flex-1 !overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[260px]">{ts('Name')}</TableHead>
              <TableHead className="w-[180px]">{ts('Provider')}</TableHead>
              <TableHead>{ts('Account')}</TableHead>
              <TableHead className="w-[140px]">{ts('Status')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              message(ts('Loading connections…'))
            ) : error && !data ? (
              <TableRow>
                <TableCell colSpan={COLUMN_COUNT} className="py-8">
                  <InlineLoadError
                    error={error}
                    onRetry={() => void mutate()}
                  />
                </TableCell>
              </TableRow>
            ) : connections.length === 0 ? (
              message(ts('No connections in this workspace.'))
            ) : (
              connections.map((connection) => (
                <TableRow key={connection.name}>
                  <TableCell>
                    <Link
                      to={connectionPath(connection.name, connection.workspace)}
                      className="font-medium text-primary hover:underline"
                    >
                      {connection.name}
                    </Link>
                  </TableCell>
                  <TableCell>{PROVIDER_LABELS[connection.provider]}</TableCell>
                  <TableCell className="whitespace-normal break-words">
                    {connection.account || (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <ConnectionStatusBadge status={connection.status} />
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
