// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import StatusChip from '@/components/ui/status-chip';
import { CircleHelp } from 'lucide-react';
import { useEffect } from 'react';
import { Link } from 'react-router-dom';
import { useDocLive } from './context';

type Props = {
  dagRef: string;
  label?: string;
};

/**
 * Live status chip for a DAG referenced from document content. Requires a
 * DocLiveProvider; callers render a plain link when no provider is mounted.
 */
export function DagStatusChip({ dagRef, label }: Props) {
  const live = useDocLive();

  useEffect(() => {
    if (!live) return;
    return live.registerRef(dagRef);
  }, [live, dagRef]);

  if (!live) return null;
  const result = live.lookup(dagRef);
  // An explicitly empty label renders the status chip alone.
  const text = label === undefined ? dagRef : label.trim();

  if (result.state === 'not-found') {
    return (
      <span
        className="inline-flex items-center gap-1 align-middle px-1.5 py-0.5 text-xs rounded-full bg-muted text-muted-foreground border border-border"
        title={`DAG not found: ${dagRef}`}
      >
        <CircleHelp className="h-3 w-3" />
        {text}
      </span>
    );
  }
  if (result.state === 'loading') {
    return (
      <span className="inline-flex items-center align-middle px-1.5 py-0.5 text-xs rounded-full bg-muted text-muted-foreground border border-border">
        {text}
      </span>
    );
  }
  return (
    <Link
      to={`/dags/${encodeURIComponent(result.fileName)}`}
      className="inline-flex items-center gap-1 align-middle no-underline"
      title={`${result.dagName}: ${result.latestDAGRun.statusLabel}`}
    >
      {text !== '' && <span className="text-xs">{text}</span>}
      <StatusChip status={result.latestDAGRun.status} size="xs">
        {result.latestDAGRun.statusLabel}
      </StatusChip>
    </Link>
  );
}
