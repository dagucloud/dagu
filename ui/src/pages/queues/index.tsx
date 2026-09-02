// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Layers, Search } from 'lucide-react';
import React from 'react';
import { components } from '../../api/v1/schema';
import { Input } from '@/components/ui/input';
import { RefreshButton } from '@/components/ui/refresh-button';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useSearchState } from '../../contexts/SearchStateContext';
import QueueList from '../../features/queues/components/QueueList';
import { useQuery } from '../../hooks/api';
import { useQueuesListSSE } from '../../hooks/useQueuesListSSE';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../hooks/useSSECacheSync';
import Title from '@/components/ui/title';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

function Queues() {
  const appBarContext = React.useContext(AppBarContext);
  const searchState = useSearchState();
  const remoteKey = appBarContext.selectedRemoteNode || 'local';

  type QueueFilters = {
    searchText: string;
  };

  function areQueueFiltersEqual(a: QueueFilters, b: QueueFilters): boolean {
    return a.searchText === b.searchText;
  }

  const defaultFilters = React.useMemo<QueueFilters>(
    () => ({
      searchText: '',
    }),
    []
  );

  const [searchText, setSearchText] = React.useState(defaultFilters.searchText);

  const currentFilters = React.useMemo<QueueFilters>(
    () => ({
      searchText,
    }),
    [searchText]
  );

  const currentFiltersRef = React.useRef(currentFilters);
  React.useEffect(() => {
    currentFiltersRef.current = currentFilters;
  }, [currentFilters]);

  const lastPersistedFiltersRef = React.useRef<QueueFilters | null>(null);

  React.useEffect(() => {
    const stored = searchState.readState<QueueFilters>('queues', remoteKey);
    const next = stored
      ? {
          searchText: stored.searchText ?? defaultFilters.searchText,
        }
      : defaultFilters;

    const current = currentFiltersRef.current;
    if (current && areQueueFiltersEqual(current, next)) {
      if (!stored) {
        searchState.writeState('queues', remoteKey, next);
      }
      lastPersistedFiltersRef.current = next;
      return;
    }

    setSearchText(next.searchText);
    lastPersistedFiltersRef.current = next;
    searchState.writeState('queues', remoteKey, next);
  }, [defaultFilters, remoteKey, searchState]);

  React.useEffect(() => {
    const persisted = lastPersistedFiltersRef.current;
    if (persisted && areQueueFiltersEqual(persisted, currentFilters)) {
      return;
    }
    lastPersistedFiltersRef.current = currentFilters;
    searchState.writeState('queues', remoteKey, currentFilters);
  }, [currentFilters, remoteKey, searchState]);

  React.useEffect(() => {
    appBarContext.setTitle('Queue Dashboard');
  }, [appBarContext]);

  const queuesSSE = useQueuesListSSE();

  const { data, error, isLoading, mutate } = useQuery(
    '/queues',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    sseFallbackOptions(queuesSSE, 3000)
  );
  useSSECacheSync(queuesSSE, mutate);

  async function handleRefresh(): Promise<void> {
    await mutate();
  }

  const filteredQueues = React.useMemo(() => {
    if (!data?.queues) return [];

    const search = searchText.toLowerCase();

    return data.queues
      .filter((queue) => {
        if (searchText && !queue.name.toLowerCase().includes(search)) {
          return false;
        }
        return true;
      })
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [data?.queues, searchText]);

  if (error) {
    const errorData = error as components['schemas']['Error'];
    return (
      <div className="flex h-full items-center justify-center">
        <div className="space-y-2 text-center">
          <Layers className="mx-auto h-12 w-12 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            {errorData?.message || <I18nText text={"Failed to load queue information"} />}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full max-w-7xl flex-col gap-4 overflow-hidden">
      <div className="flex flex-col gap-1">
        <Title><I18nText text={"Queues"} /></Title>
        <p className="text-sm text-muted-foreground">
          <I18nText text={"One card per queue, with running activity and backlog status."} />
        </p>
      </div>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="relative w-full max-w-sm">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <I18nProps><Input
            type="text"
            placeholder="Search queues..."
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            className="h-9 pl-9 text-sm"
          /></I18nProps>
        </div>
        <div className="flex items-center gap-2">
          {data?.queues && (
            <span className="text-xs text-muted-foreground">
              {filteredQueues.length}
              {filteredQueues.length !== data.queues.length &&
                ` of ${data.queues.length}`}{' '}
              <I18nText text={"queues"} />
            </span>
          )}
          <RefreshButton onRefresh={handleRefresh} />
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-auto pr-1">
        <QueueList queues={filteredQueues} isLoading={isLoading && !data} />
      </div>
    </div>
  );
}

export default Queues;
