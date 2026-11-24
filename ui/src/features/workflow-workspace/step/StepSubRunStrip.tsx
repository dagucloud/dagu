// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import {
  allSubRunsOf,
  subDagNameOf,
  useStepSubRunsOpener,
} from './useStepRunActions';

/**
 * The line over the log of a step that ran sub-runs. The log holds the
 * summary of the runs, not their steps: the line names the workflow and
 * opens the runs, one at once, several through a picker. A step with nodes
 * opens its node runs instead, when the host gives `onOpenNodes`. Nothing
 * for a step with no sub-run.
 */
export function StepSubRunStrip({
  node,
  holder,
  className,
  onOpenNodes,
}: {
  node: ApiNode;
  /** The run that holds the step. */
  holder: ApiDAGRunDetails;
  className?: string;
  /** Shows the node runs of the run. */
  onOpenNodes?(): void;
}) {
  const { ts } = useI18n();
  const subRuns = useStepSubRunsOpener();
  const count = allSubRunsOf(node).length;
  if (node.nodeRuns && onOpenNodes) {
    return (
      <div
        data-testid="ww-step-sub-runs"
        className={cn(
          'flex shrink-0 items-center gap-2 border-b px-3 py-1 text-xs text-muted-foreground',
          className
        )}
      >
        <span className="min-w-0 truncate">
          {ts('This step matched {count} nodes.', {
            count: node.nodeRuns.total,
          })}
        </span>
        <Button type="button" variant="ghost" size="xs" onClick={onOpenNodes}>
          {ts('Open Nodes')}
        </Button>
      </div>
    );
  }
  if (count === 0) return null;
  const name = subDagNameOf(node);
  return (
    <div
      data-testid="ww-step-sub-runs"
      className={cn(
        'flex shrink-0 items-center gap-2 border-b px-3 py-1 text-xs text-muted-foreground',
        className
      )}
    >
      <span className="min-w-0 truncate">
        {count === 1
          ? ts('This step ran {name} as a sub-run.', { name })
          : ts('This step ran {name} as {count} sub-runs.', { name, count })}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="xs"
        onClick={(event) =>
          subRuns.open(node, holder, {
            newTab: event.metaKey || event.ctrlKey,
          })
        }
      >
        {count === 1 ? ts('Open sub-run') : ts('Choose sub-run')}
      </Button>
      {subRuns.picker}
    </div>
  );
}
