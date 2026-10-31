// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle, PanelRightOpen, ScrollText } from 'lucide-react';
import { NodeStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import { useWorkspaceRun } from '../data';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { useDockCommands } from './DockContext';

/**
 * The reason that a step failed, as the run recorded it. Null for a step that
 * did not fail or has no recorded reason.
 */
export function stepFailureReason(
  node:
    | { status: NodeStatus; error?: string; rejectionReason?: string }
    | undefined
): string | null {
  if (!node) return null;
  if (node.status === NodeStatus.Rejected) {
    return node.rejectionReason || node.error || null;
  }
  if (node.status === NodeStatus.Failed || node.status === NodeStatus.Aborted) {
    return node.error || null;
  }
  return null;
}

/**
 * A red banner with the error of a step and a way to its stderr and to the
 * node view of the step. It shows
 * nothing when the step has no recorded failure reason.
 */
export function StepFailureBanner({
  step,
  className,
}: {
  step: string;
  className?: string;
}) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const dock = useDockCommands();
  const [, setUrl] = useWorkspaceUrl();
  const reason = stepFailureReason(run.nodeByStep.get(step));
  if (reason === null) return null;
  const tone = toneClasses('error');
  return (
    <div
      role="alert"
      data-testid="ww-step-failure"
      className={cn(
        'flex flex-wrap items-start gap-x-2 gap-y-1 border-b px-3 py-1.5 text-xs',
        tone.bg,
        tone.text,
        className
      )}
    >
      <AlertTriangle
        aria-hidden="true"
        className="mt-0.5 h-3.5 w-3.5 shrink-0"
      />
      <p
        title={reason}
        className="max-h-16 min-w-[min(100%,12rem)] flex-1 basis-48 overflow-y-auto whitespace-pre-wrap break-words font-medium"
      >
        {reason}
      </p>
      <Button
        type="button"
        variant="outline"
        size="xs"
        className="shrink-0"
        onClick={() => dock.openStep({ step, stream: 'stderr' })}
      >
        <ScrollText aria-hidden="true" />
        {ts('View stderr')}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="xs"
        className="shrink-0"
        onClick={() => setUrl({ step, ndv: 'expanded' }, { replace: true })}
      >
        <PanelRightOpen aria-hidden="true" />
        {ts('Open step')}
      </Button>
    </div>
  );
}
