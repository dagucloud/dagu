// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { MousePointerClick } from 'lucide-react';
import React from 'react';

type Props = {
  /** A click opens the step details. */
  inspect?: boolean;
  /** A double-click opens the sub DAG run. */
  openSubRun?: boolean;
  /** A right-click updates the node status. */
  updateStatus?: boolean;
  className?: string;
};

/** Icon whose tooltip lists the mouse and gesture actions a graph supports. */
export function GraphInteractionsHint({
  inspect = false,
  openSubRun = false,
  updateStatus = false,
  className,
}: Props): React.JSX.Element {
  const { ts } = useI18n();
  return (
    <div className={cn('flex justify-end', className)}>
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex h-7 w-7 items-center justify-center rounded bg-muted text-muted-foreground cursor-help"
            aria-label={ts('Graph interactions')}
          >
            <MousePointerClick className="h-3.5 w-3.5" />
          </div>
        </TooltipTrigger>
        <TooltipContent>
          <div className="space-y-1">
            {inspect && (
              <p>
                <I18nText text={'Click: Inspect step details'} />
              </p>
            )}
            {openSubRun && (
              <p>
                <I18nText text={'Double-click: Navigate to sub dagRun'} />
              </p>
            )}
            {updateStatus && (
              <p>
                <I18nText text={'Right-click: Update node status'} />
              </p>
            )}
            <p>
              <I18nText text={'Drag: Pan the graph'} />
            </p>
            <p>
              <I18nText text={'Ctrl/Cmd + scroll or pinch: Zoom'} />
            </p>
          </div>
        </TooltipContent>
      </Tooltip>
    </div>
  );
}
