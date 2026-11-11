// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown } from 'lucide-react';
import React, { useState } from 'react';
import { Link } from 'react-router-dom';
import { components } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useServiceDAGs } from '../hooks/useServiceDAGs';
import { ServiceDAGRuns } from './ServiceDAGRuns';
import {
  ServiceDAGFailure,
  ServiceDAGStateBadge,
  ServiceDAGStatusText,
} from './ServiceDAGState';

type ServiceDAG = components['schemas']['ServiceDAG'];

function ServiceDAGCard({
  dag,
  expanded,
  onToggle,
}: {
  dag: ServiceDAG;
  /** Set when the card can open the list of runs of its DAG. */
  expanded?: boolean;
  onToggle?: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <div className="flex min-w-0 items-stretch rounded-md border border-border bg-card transition-colors focus-within:ring-2 focus-within:ring-ring">
      <Link
        to={`/dags/${encodeURIComponent(dag.fileName)}`}
        title={ts('Restarts {seconds}s after a run ends', {
          seconds: dag.restartWaitSec,
        })}
        className="flex min-w-0 flex-1 items-center justify-between gap-3 rounded-md px-2.5 py-1.5 transition-colors hover:bg-accent/50 focus-visible:outline-none"
      >
        <span className="min-w-0">
          <span className="block truncate text-xs font-medium leading-tight">
            {dag.name}
          </span>
          <ServiceDAGStatusText
            dag={dag}
            className="block truncate text-[11px] text-muted-foreground"
          />
          <ServiceDAGFailure
            dag={dag}
            className="block truncate text-[11px] text-destructive"
          />
        </span>
        <ServiceDAGStateBadge state={dag.state} />
      </Link>
      {onToggle && (
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          aria-label={ts(
            expanded ? 'Hide runs of {name}' : 'Show runs of {name}',
            {
              name: dag.name,
            }
          )}
          className="flex w-7 shrink-0 items-center justify-center rounded-r-md border-l border-border text-muted-foreground transition-colors hover:bg-accent/50 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <ChevronDown
            className={cn(
              'h-3.5 w-3.5 transition-transform',
              expanded && 'rotate-180'
            )}
          />
        </button>
      )}
    </div>
  );
}

/**
 * Shows the DAGs that a restart policy keeps alive, one card for each. Their
 * runs are left out of the boards and lists, since a service DAG restarts without
 * end. With `expandable`, a card opens the latest runs of its DAG.
 * Renders nothing when no DAG sets a restart policy.
 */
export function ServiceDAGStrip({
  expandable = false,
  className,
}: {
  expandable?: boolean;
  className?: string;
}): React.ReactElement | null {
  const { ts } = useI18n();
  const { serviceDAGs } = useServiceDAGs();
  const [open, setOpen] = useState<string | null>(null);
  if (serviceDAGs.length === 0) {
    return null;
  }
  const openDAG = serviceDAGs.find((dag) => dag.fileName === open);

  return (
    <section
      aria-label={ts('Service DAGs')}
      className={cn('px-2 pb-2', className)}
    >
      <h2 className="pb-1.5 text-sm font-semibold text-foreground">
        {ts('Service DAGs')}
        <span className="ml-1.5 font-normal text-muted-foreground">
          {serviceDAGs.length}
        </span>
      </h2>
      {/* A phone lists every card at full width: a scroll box inside the page
          is hard to scroll there. */}
      <div className="grid gap-2 sm:max-h-40 sm:grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] sm:overflow-y-auto">
        {serviceDAGs.map((dag) => (
          <ServiceDAGCard
            key={dag.fileName}
            dag={dag}
            expanded={expandable ? dag.fileName === open : undefined}
            onToggle={
              expandable
                ? () =>
                    setOpen((current) =>
                      current === dag.fileName ? null : dag.fileName
                    )
                : undefined
            }
          />
        ))}
      </div>
      {expandable && openDAG && (
        <div className="mt-2 rounded-md border border-border bg-card">
          <ServiceDAGRuns name={openDAG.name} />
        </div>
      )}
    </section>
  );
}
