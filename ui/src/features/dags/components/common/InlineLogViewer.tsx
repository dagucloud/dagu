// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { components, Stream } from '../../../../api/v1/schema';

/**
 * ANSI color codes regex for stripping
 */
const ANSI_CODES_REGEX = [
  '[\\u001B\\u009B][[\\]()#;?]*(?:(?:(?:(?:;[-a-zA-Z\\d\\/#&.:=?%@~_]+)*|[a-zA-Z\\d]+(?:;[-a-zA-Z\\d\\/#&.:=?%@~_]*)*)?\\u0007)',
  '(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PR-TZcf-nq-uy=><~]))',
].join('|');

/**
 * Simple inline log viewer - no controls, just logs
 */
export function InlineLogViewer({
  dagName,
  dagRunId,
  stepName,
  stream,
  dagRun,
}: {
  dagName: string;
  dagRunId: string;
  stepName: string;
  stream: components['schemas']['Stream'];
  dagRun?: components['schemas']['DAGRunDetails'];
}) {
  const remoteNode = useRemoteNode();

  // Determine if this is a sub DAG run - check both rootDAGRunId AND rootDAGRunName
  const isSubDAGRun =
    dagRun &&
    dagRun.rootDAGRunId &&
    dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId;

  // Fetch sub-DAG-run step log (only when isSubDAGRun is true)
  const subDAGQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/log',
    whenEnabled(!!isSubDAGRun, {
      params: {
        query: {
          remoteNode,
          stream,
          tail: 100,
        },
        path: {
          name: dagRun?.rootDAGRunName as string,
          dagRunId: dagRun?.rootDAGRunId as string,
          subDAGRunId: dagRun?.dagRunId as string,
          stepName,
        },
      },
    }),
    {
      refreshInterval: 2000,
      revalidateOnFocus: false,
    }
  );

  // Fetch regular DAG-run step log (only when isSubDAGRun is false)
  const dagRunQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log',
    whenEnabled(!isSubDAGRun, {
      params: {
        query: {
          remoteNode,
          stream,
          tail: 100,
        },
        path: {
          name: dagName,
          dagRunId,
          stepName,
        },
      },
    }),
    {
      refreshInterval: 2000,
      revalidateOnFocus: false,
    }
  );

  // Use the appropriate query based on whether this is a sub-DAG-run
  const { data, isLoading } = isSubDAGRun ? subDAGQuery : dagRunQuery;

  // Process log content
  const content =
    data?.content?.replace(new RegExp(ANSI_CODES_REGEX, 'g'), '') || '';
  const lines = content ? content.split('\n') : [];
  const totalLines = data?.totalLines || 0;
  const lineCount = data?.lineCount || 0;

  return (
    <div className="bg-muted rounded overflow-hidden border border-border">
      {isLoading && !data ? (
        <div className="text-muted-foreground text-xs py-4 px-3">
          Loading logs...
        </div>
      ) : lines.length === 0 ? (
        <div className="text-muted-foreground text-xs py-4 px-3">
          &lt;No log output&gt;
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
                    {line || ' '}
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

export { ANSI_CODES_REGEX, Stream };
