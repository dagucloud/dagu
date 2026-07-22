// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from '@/lib/dayjs';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { components } from '../../../../api/v1/schema';

type PushBackHistoryEntry = components['schemas']['PushBackHistoryEntry'];

type Props = {
  history?: PushBackHistoryEntry[];
  title?: string;
  className?: string;
};

const formatTimestamp = (timestamp?: string) => {
  if (!timestamp) return '';
  const formatted = dayjs(timestamp);
  return formatted.isValid()
    ? formatted.format('YYYY-MM-DD HH:mm:ss Z')
    : timestamp;
};

const formatInputs = (inputs?: Record<string, string>) => {
  if (!inputs || Object.keys(inputs).length === 0) {
    return '';
  }
  return Object.entries(inputs)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}=${JSON.stringify(value)}`)
    .join(' ');
};

const PushBackSubject = ({ name, id }: { name: string; id?: string }) => {
  const label = <span className="text-foreground/80">{name}</span>;
  if (!id) {
    return label;
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>{label}</TooltipTrigger>
      <TooltipContent>Subject ID: {id}</TooltipContent>
    </Tooltip>
  );
};

export default function PushBackHistory({
  history,
  title = 'Push-back History',
  className,
}: Props) {
  if (!history || history.length === 0) {
    return null;
  }

  const entries = [...history].reverse();

  return (
    <div className={className}>
      <div className="text-xs font-medium text-muted-foreground mb-1.5">
        {title}
      </div>
      <div className="space-y-2">
        {entries.map((entry) => {
          const formattedInputs = formatInputs(entry.inputs);
          return (
            <div
              key={`${entry.iteration}-${entry.at || 'pending'}`}
              className="rounded-md border border-border bg-muted/30 px-3 py-2"
            >
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                <span className="font-medium text-foreground/90">
                  Iteration {entry.iteration}
                </span>
                {entry.by || entry.byId ? (
                  <span className="text-muted-foreground">
                    by{' '}
                    <PushBackSubject
                      name={entry.by || entry.byId!}
                      id={entry.byId}
                    />
                  </span>
                ) : null}
                {entry.at && (
                  <span className="text-muted-foreground">
                    at {formatTimestamp(entry.at)}
                  </span>
                )}
              </div>
              {formattedInputs && (
                <div className="mt-1 text-xs text-muted-foreground">
                  <span className="font-medium">Inputs:</span>{' '}
                  <span className="font-mono text-foreground/80 break-all">
                    {formattedInputs}
                  </span>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
