// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Layers } from 'lucide-react';
import { EmptyState } from '@/components/PageStates';
import React from 'react';
import { QueueType, type components } from '../../../api/v1/schema';
import QueueCard from './QueueCard';
import { idleServiceDAGQueue, ownsQueue } from '../lib/serviceDAGQueue';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

interface QueueListProps {
  queues: components['schemas']['Queue'][];
  isLoading?: boolean;
  /** Service DAGs; a queue that one owns is shown as that service DAG. */
  serviceDAGs?: components['schemas']['ServiceDAG'][];
}

function QueueList({ queues, isLoading, serviceDAGs = [] }: QueueListProps) {
  const { ts } = useI18n();
  const queueOwners = serviceDAGs.filter(ownsQueue);
  const serviceDAGByQueue = new Map(queueOwners.map((dag) => [dag.name, dag]));
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

  if (queues.length === 0 && queueOwners.length === 0) {
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

  const serviceDAGOf = (queue: components['schemas']['Queue']) =>
    queue.type === QueueType.dag_based
      ? serviceDAGByQueue.get(queue.name)
      : undefined;
  // The server lists a DAG queue only while a run is alive or waiting, so an
  // service DAG between two runs has no queue in the list. Its card stays,
  // drawn as an idle queue, unless a global queue of the same name is its queue.
  const serviceDAGQueues = queueOwners.flatMap((dag) => {
    const own = queues.find((queue) => queue.name === dag.name);
    if (!own) return [idleServiceDAGQueue(dag.name)];
    return own.type === QueueType.dag_based ? [own] : [];
  });
  const otherQueues = queues.filter((queue) => !serviceDAGOf(queue));
  const grid = 'grid gap-3 md:grid-cols-2 2xl:grid-cols-3';

  return (
    <div className="space-y-6">
      {otherQueues.length > 0 && (
        <section aria-label={ts('Queues')}>
          {serviceDAGQueues.length > 0 && (
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
      {serviceDAGQueues.length > 0 && (
        <section aria-label={ts('Service DAGs')}>
          <h2 className="text-sm font-semibold text-foreground">
            {ts('Service DAGs')}
          </h2>
          <p className="pb-2 text-xs text-muted-foreground">
            {ts('DAGs that Dagu restarts when their run ends.')}
          </p>
          <div className={grid}>
            {serviceDAGQueues.map((queue) => (
              <QueueCard
                key={queue.name}
                queue={queue}
                dag={serviceDAGOf(queue)}
              />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

export default QueueList;
