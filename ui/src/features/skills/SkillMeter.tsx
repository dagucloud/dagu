// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, type ReactElement } from 'react';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { bodyMeter, bodyStats, type MeterBar } from './skillIssues';

const NUMBER = new Intl.NumberFormat('en-US');

function Bar({
  bar,
  label,
  hint,
}: {
  bar: MeterBar;
  label: string;
  hint: string;
}): ReactElement {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-testid="skill-meter-item"
          data-over={bar.over ? 'true' : undefined}
          className={cn(
            'inline-flex items-center gap-2',
            bar.over && 'text-warning'
          )}
        >
          <span>{label}</span>
          <span
            aria-hidden="true"
            className="relative inline-block h-1.5 w-24 overflow-hidden rounded-full bg-muted"
          >
            <span
              className={cn(
                'absolute inset-y-0 left-0 rounded-full',
                bar.over ? 'bg-warning' : 'bg-primary/60'
              )}
              style={{ width: `${bar.fill * 100}%` }}
            />
            <span
              data-testid="skill-meter-marker"
              className="absolute inset-y-0 w-px bg-foreground/60"
              style={{ left: `${bar.marker * 100}%` }}
            />
          </span>
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom">{hint}</TooltipContent>
    </Tooltip>
  );
}

/**
 * The size of the body of SKILL.md: an estimate of the tokens and the number
 * of lines. Each has a bar and a marker at the limit that the format
 * recommends. The bar turns to the warning colour past the marker.
 */
export function SkillMeter({
  content,
  className,
}: {
  /** The text of SKILL.md, with the front matter. */
  content: string;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const meter = useMemo(() => bodyMeter(bodyStats(content)), [content]);
  return (
    <div
      className={cn(
        'flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground',
        className
      )}
    >
      <Bar
        bar={meter.tokens}
        label={ts('about {count} tokens', {
          count: NUMBER.format(meter.tokens.value),
        })}
        hint={ts(
          'An estimate: the bytes of the body divided by 3.7. The format recommends {limit} tokens or fewer.',
          { limit: NUMBER.format(meter.tokens.limit) }
        )}
      />
      <Bar
        bar={meter.lines}
        label={ts('{count} lines', { count: NUMBER.format(meter.lines.value) })}
        hint={ts('The format recommends {limit} lines or fewer.', {
          limit: NUMBER.format(meter.lines.limit),
        })}
      />
    </div>
  );
}
