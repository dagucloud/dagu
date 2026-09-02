// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Archive } from 'lucide-react';
import { components } from '@/api/v1/schema';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

type Props = {
  dagRun: DAGRunSummary;
  onClick: () => void;
};

export function DAGRunArtifactsButton({
  dagRun,
  onClick,
}: Props): React.ReactElement | null {
  const { ts } = useI18n();

  if (!dagRun.artifactsAvailable) {
    return null;
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={ts('View artifacts for {name} {runId}', {
            name: dagRun.name,
            runId: dagRun.dagRunId,
          })}
          className="inline-flex size-7 shrink-0 items-center justify-center rounded-md border border-border bg-background/80 text-muted-foreground shadow-sm transition-colors hover:border-primary/40 hover:bg-primary/10 hover:text-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
          onClick={(event) => {
            event.stopPropagation();
            onClick();
          }}
        >
          <Archive className="h-4 w-4" />
        </button>
      </TooltipTrigger>
      <TooltipContent>
        <I18nText text={'View artifacts'} />
      </TooltipContent>
    </Tooltip>
  );
}
