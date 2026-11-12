// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useI18n } from '@/i18n/I18nProvider';
import type { BarSlotProps } from '../contracts';
import { useWorkflowLabels } from './useWorkflowLabels';

/** Chips shown before the rest collapses into '+n'. */
export const VISIBLE_LABELS = 3;

const chip =
  'inline-flex h-5 max-w-[10rem] shrink-0 items-center truncate rounded-md border border-border bg-muted px-1.5 text-[11px] text-muted-foreground transition-colors hover:bg-accent hover:text-foreground';

/** Labels of the workflow; each one opens the workflow list filtered by it. */
export function LabelChips({ dag }: BarSlotProps) {
  const { ts } = useI18n();
  const { labels, listUrl } = useWorkflowLabels(dag);
  if (labels.length === 0) return null;

  // A single hidden label takes the room of its '+1' chip.
  const visible =
    labels.length > VISIBLE_LABELS + 1
      ? labels.slice(0, VISIBLE_LABELS)
      : labels;
  const hidden = labels.slice(visible.length);

  return (
    <ul
      data-testid="ww-label-chips"
      aria-label={ts('Labels')}
      className="flex min-w-0 items-center gap-1"
    >
      {visible.map((label) => (
        <li key={label} className="flex min-w-0">
          <Link
            to={listUrl(label)}
            className={chip}
            title={ts('Show workflows with label {label}', { label })}
          >
            {label}
          </Link>
        </li>
      ))}
      {hidden.length > 0 && (
        <li className="flex">
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                className={chip}
                aria-label={ts('{count} more labels', {
                  count: hidden.length,
                })}
              >
                +{hidden.length}
              </button>
            </TooltipTrigger>
            <TooltipContent className="max-w-xs">
              {hidden.join(', ')}
            </TooltipContent>
          </Tooltip>
        </li>
      )}
    </ul>
  );
}
