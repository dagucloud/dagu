// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  Ban,
  CheckCircle2,
  Circle,
  Loader2,
  MinusCircle,
  PauseCircle,
  RotateCw,
  XCircle,
  type LucideIcon,
} from 'lucide-react';
import { useMemo } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { ELAPSED_SLOT, useStepElapsed } from '@/hooks/useStepElapsed';
import { useI18n } from '@/i18n/I18nProvider';
import { RepeatBadge } from '@/components/ui/repeat-badge';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { RetryCount } from '@/components/ui/retry-count';
import { Spinner } from '@/components/ui/spinner';
import { nodeStatusText } from '@/lib/status-utils';
import { statusClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import type { ApiNode } from '../contracts';
import { RepeatRule } from './RepeatRule';

const ICONS: Record<NodeStatus, LucideIcon> = {
  [NodeStatus.NotStarted]: Circle,
  [NodeStatus.Running]: Loader2,
  [NodeStatus.Failed]: XCircle,
  [NodeStatus.Aborted]: Ban,
  [NodeStatus.Success]: CheckCircle2,
  [NodeStatus.Skipped]: MinusCircle,
  [NodeStatus.PartialSuccess]: AlertTriangle,
  [NodeStatus.Waiting]: PauseCircle,
  [NodeStatus.Rejected]: XCircle,
  [NodeStatus.Retrying]: RotateCw,
};

/**
 * The exit code of a failed step. The API has no field for it: the error
 * text of the node ends with "exit status N" when a command exited.
 */
export function exitCodeOf(node: ApiNode): number | null {
  const found = /exit status (\d+)/.exec(node.error ?? '');
  return found ? Number(found[1]) : null;
}

/**
 * One line above the tabs of the OUTPUT column: the status of the step in
 * the run, its duration, its retries and its exit code. Nothing when the
 * step has no node in the run.
 */
export function StepFactsLine({
  node,
  testRun = false,
}: {
  node: ApiNode | null | undefined;
  /** The run is a test run of an unsaved draft, which the server drops. */
  testRun?: boolean;
}) {
  if (!node) return null;
  return <FactsLine node={node} testRun={testRun} />;
}

function FactsLine({ node, testRun }: { node: ApiNode; testRun: boolean }) {
  const { ts } = useI18n();
  const duration = useStepElapsed(node);
  const Icon = ICONS[node.status] ?? Circle;
  const exitCode = exitCodeOf(node);
  const repeat = useMemo(
    () => readRepeatPolicy(node.step.repeatPolicy),
    [node.step.repeatPolicy]
  );
  return (
    <div
      data-testid="ww-step-facts-line"
      className="flex h-8 shrink-0 items-center gap-3 overflow-hidden whitespace-nowrap border-b border-border px-2 text-xs text-muted-foreground"
    >
      <span
        data-testid="ww-step-facts-status"
        className={cn(
          'inline-flex items-center gap-1.5 font-medium',
          statusClasses(node.status).text
        )}
      >
        {node.status === NodeStatus.Running ? (
          <Spinner className="shrink-0" />
        ) : (
          <Icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
        )}
        {ts(nodeStatusText(node.status))}
      </span>
      {duration && (
        <span data-testid="ww-step-duration" className={ELAPSED_SLOT}>
          {duration}
        </span>
      )}
      {node.retryCount > 0 && (
        <span data-testid="ww-step-retries">
          <RetryCount count={node.retryCount} />
        </span>
      )}
      {exitCode !== null && (
        <span data-testid="ww-step-exit" className="font-mono">
          {ts('Exit code {code}', { code: exitCode })}
        </span>
      )}
      {repeat && (
        <>
          <span data-testid="ww-step-repeat" className="inline-flex shrink-0">
            <RepeatBadge
              policy={repeat}
              count={node.doneCount}
              running={node.status === NodeStatus.Running}
            />
          </span>
          <RepeatRule policy={repeat} />
        </>
      )}
      {testRun && (
        <span data-testid="ww-step-test-run" className="ml-auto shrink-0">
          {ts('Test run, not kept')}
        </span>
      )}
    </div>
  );
}
