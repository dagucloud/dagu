// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AnsiLine } from '@/lib/ansi';
import { components, Stream } from '../../../../api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import {
  type ForeachLogTarget,
  useStepLogQuery,
} from '../../hooks/useStepLogQuery';

/**
 * Simple inline log viewer - no controls, just logs
 */
export function InlineLogViewer({
  dagName,
  dagRunId,
  stepName,
  stream,
  dagRun,
  foreach,
  live = true,
}: {
  dagName: string;
  dagRunId: string;
  stepName: string;
  stream: components['schemas']['Stream'];
  dagRun?: components['schemas']['DAGRunDetails'];
  /** Reads a body step log of a foreach item instead of a run step log. */
  foreach?: ForeachLogTarget;
  /** Whether to keep polling for new output. */
  live?: boolean;
}) {
  const { data, isLoading } = useStepLogQuery(
    { dagName, dagRunId, stepName, dagRun, foreach },
    { stream, tail: 100 },
    {
      refreshInterval: live ? 2000 : 0,
      // Shorter than the interval, or SWR skips every other refresh.
      dedupingInterval: 1000,
      revalidateOnFocus: false,
    }
  );

  // Process log content
  const content = data?.content || '';
  const lines = content ? content.split('\n') : [];
  const totalLines = data?.totalLines || 0;
  const lineCount = data?.lineCount || 0;

  return (
    <div className="bg-muted rounded overflow-hidden border border-border">
      {isLoading && !data ? (
        <div className="text-muted-foreground text-xs py-4 px-3">
          <I18nText text={'Loading logs...'} />
        </div>
      ) : lines.length === 0 ? (
        <div className="text-muted-foreground text-xs py-4 px-3">
          <I18nText text={'<No log output>'} />
        </div>
      ) : (
        <div className="overflow-x-auto max-h-[400px] overflow-y-auto">
          <pre className="font-mono text-xs text-foreground p-2">
            {lines.map((line, index) => {
              const lineNumber = totalLines - lineCount + index + 1;
              return (
                <div key={index} className="flex px-1 py-0.5">
                  <span className="text-muted-foreground mr-3 select-none w-12 text-right flex-shrink-0">
                    {lineNumber}
                  </span>
                  <span className="whitespace-pre-wrap break-all flex-grow">
                    {line ? <AnsiLine text={line} /> : ' '}
                  </span>
                </div>
              );
            })}
          </pre>
        </div>
      )}
    </div>
  );
}

export { Stream };
