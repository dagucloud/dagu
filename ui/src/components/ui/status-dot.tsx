// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { NodeStatus, Status } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { HATCH_PATTERN } from '@/lib/hatchPattern';
import { nodeStatusText, runStatusText } from '@/lib/status-utils';
import { statusClasses, toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';

interface StatusDotCommon {
  /**
   * With kind 'run': the run runs for ever and has no failed step
   * (runDisplayStatus). The dot is in the success token and pulses calmly,
   * and its name is "Healthy". `status` stays the API status.
   */
  healthy?: boolean;
  /** Accessible name. The default is the translated text of the status. */
  label?: string;
  /**
   * With kind 'step': the run did not execute the step but reused its outputs
   * from a source run, where the step succeeded ('success', green) or did not
   * ('failed', red). The dot has the same size and a hatch in place of the
   * solid fill, and its name says "Reused, succeeded" or "Reused, failed".
   * `status` stays the status of the node.
   */
  reused?: 'success' | 'failed';
  /** With `reused`: the short id of the source run, for the tooltip. */
  reusedFrom?: string;
  /** Hides the dot from assistive technology, when text next to it names the status. */
  decorative?: boolean;
  className?: string;
}

export type StatusDotProps = StatusDotCommon &
  ({ kind: 'run'; status: Status } | { kind: 'step'; status: NodeStatus });

/** A running, retrying or waiting item pulses when motion is allowed. */
function pulses(props: Pick<StatusDotProps, 'kind' | 'status'>): boolean {
  if (props.kind === 'run') {
    return props.status === Status.Running || props.status === Status.Waiting;
  }
  return (
    props.status === NodeStatus.Running ||
    props.status === NodeStatus.Retrying ||
    props.status === NodeStatus.Waiting
  );
}

/**
 * The status of a run or of a step as a dot in its status token. The dot has
 * an accessible name from the translated status text, unless it is decorative.
 */
export function StatusDot(props: StatusDotProps): ReactElement {
  const {
    status,
    kind,
    label,
    healthy = false,
    decorative = false,
    reused,
    reusedFrom,
    className,
  } = props;
  const { ts } = useI18n();
  const isHealthy = healthy && kind === 'run';
  const reuse = kind === 'step' ? reused : undefined;
  const text =
    label ??
    ts(
      reuse
        ? reuse === 'success'
          ? 'Reused, succeeded'
          : 'Reused, failed'
        : isHealthy
          ? 'Healthy'
          : kind === 'run'
            ? runStatusText(status as Status)
            : nodeStatusText(status as NodeStatus)
    );
  return (
    <span
      {...(decorative
        ? { 'aria-hidden': true }
        : {
            role: 'img',
            'aria-label': text,
            title:
              reuse && reusedFrom && label === undefined
                ? ts(
                    reuse === 'success'
                      ? 'Reused from run {id}: succeeded'
                      : 'Reused from run {id}: failed',
                    { id: reusedFrom }
                  )
                : text,
          })}
      data-testid="status-dot"
      data-status={status}
      data-reused={reuse}
      // The hatch replaces the solid fill: same size, same token colour.
      style={reuse ? { backgroundImage: HATCH_PATTERN } : undefined}
      className={cn(
        'inline-block h-2 w-2 shrink-0 rounded-full',
        reuse
          ? toneClasses(reuse === 'success' ? 'success' : 'error').dot
          : isHealthy
            ? toneClasses('success').dot
            : statusClasses(status).dot,
        (isHealthy || pulses(props)) && 'motion-safe:animate-pulse',
        className
      )}
    />
  );
}
