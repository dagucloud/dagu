// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ServiceDAGState, components } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import { StatusDot } from '@/components/ui/status-dot';
import { SERVICE_STATE_CHIP } from '@/features/service-dags/components/ServiceDAGState';

type DAGFile = components['schemas']['DAGFile'];

/** The chip of the Runs page for the state of a service DAG. */
export function ServiceStateChip({ state }: { state: ServiceDAGState }) {
  const [status, label] = SERVICE_STATE_CHIP[state];
  return (
    <StatusChip status={status} size="xs">
      {label}
    </StatusChip>
  );
}

/**
 * The last run of a workflow in the list: the dot of its status and the time
 * since it started, in the compact form of the workflow header ("3d ago").
 * A service DAG, when its state is known, shows this state in the chip of the
 * Runs page: the time since its last start resets at each restart.
 */
export function LastRunCell({
  dag,
  serviceState,
}: {
  dag: DAGFile;
  serviceState?: ServiceDAGState;
}) {
  const run = dag.latestDAGRun;
  if (serviceState) {
    return (
      <span
        data-testid="dag-last-run"
        data-service-state={serviceState}
        className="flex min-w-0 items-center text-xs"
      >
        <ServiceStateChip state={serviceState} />
      </span>
    );
  }
  if (!run?.startedAt || run.startedAt === '-') {
    return <span className="font-normal text-muted-foreground">-</span>;
  }
  return (
    <span
      data-testid="dag-last-run"
      className="flex min-w-0 items-center gap-1.5 text-xs"
    >
      <StatusDot kind="run" status={run.status} />
      <span className="truncate text-foreground/70">
        <RelativeTime
          timestamp={run.startedAt}
          absolute={run.startedAt}
          compact
          ago
        />
      </span>
    </span>
  );
}
