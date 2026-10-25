// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Layers } from 'lucide-react';
import { EmptyState } from '@/components/PageStates';
import React from 'react';
import { QueueType, type components } from '../../../api/v1/schema';
import QueueCard from './QueueCard';
import { idleAlwaysOnQueue } from '../lib/idleAlwaysOnQueue';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

interface QueueListProps {
  queues: components['schemas']['Queue'][];
  isLoading?: boolean;
  /** Always-on DAGs; a queue that one owns is shown as that always-on DAG. */
  alwaysOnDAGs?: components['schemas']['AlwaysOnDAG'][];
}

function QueueList({ queues, isLoading, alwaysOnDAGs = [] }: QueueListProps) {
  const { ts } = useI18n();
  // A DAG without a queue of its own uses a queue named after it.
  const alwaysOnByQueue = new Map(
    alwaysOnDAGs.map((dag) => [dag.name, dag])
  );
  if (isLoading) {
    return (
      <div className="flex h-32 items-center justify-center">
        <div className="space-y-2 text-center">
          <Layers className="mx-auto h-8 w-8 animate-pulse text-muted-foreground" />
          <p className="text-sm text-muted-foreground"><I18nText text={"Loading queues..."} /></p>
        </div>
      </div>
    );
  }

  if (queues.length === 0 && alwaysOnDAGs.length === 0) {
    return (
      <EmptyState
        icon={<Layers />}
        title={<I18nText text={'No active queues'} />}
        description={
          <I18nText
            text={
              'Queues defined in the server configuration appear here, along with any queue that has running or waiting runs.'
            }
          />
        }
      />
    );
  }

  const alwaysOnOf = (queue: components['schemas']['Queue']) =>
    queue.type === QueueType.dag_based
      ? alwaysOnByQueue.get(queue.name)
      : undefined;
  // The server lists a DAG queue only while a run is alive or waiting, so an
  // always-on DAG between two runs has no queue in the list. Its card stays,
  // drawn as an idle queue, unless a global queue of the same name is its queue.
  const alwaysOnQueues = alwaysOnDAGs.flatMap((dag) => {
    const own = queues.find((queue) => queue.name === dag.name);
    if (!own) return [idleAlwaysOnQueue(dag.name)];
    return own.type === QueueType.dag_based ? [own] : [];
  });
  const otherQueues = queues.filter((queue) => !alwaysOnOf(queue));
  const grid = 'grid gap-3 md:grid-cols-2 2xl:grid-cols-3';

  return (
    <div className="space-y-6">
      {otherQueues.length > 0 && (
        <section aria-label={ts('Queues')}>
          {alwaysOnQueues.length > 0 && (
            <h2 className="pb-2 text-sm font-semibold text-foreground">
              {ts('Queues')}
            </h2>
          )}
          <div className={grid}>
            {otherQueues.map((queue) => (
              <QueueCard key={queue.name} queue={queue} />
            ))}
          </div>
        </section>
      )}
      {alwaysOnQueues.length > 0 && (
        <section aria-label={ts('Always on')}>
          <h2 className="text-sm font-semibold text-foreground">
            {ts('Always on')}
          </h2>
          <p className="pb-2 text-xs text-muted-foreground">
            {ts('DAGs that Dagu restarts when their run ends.')}
          </p>
          <div className={grid}>
            {alwaysOnQueues.map((queue) => (
              <QueueCard
                key={queue.name}
                queue={queue}
                dag={alwaysOnOf(queue)}
              />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

export default QueueList;
