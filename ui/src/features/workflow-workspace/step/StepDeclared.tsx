// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { buildWorkspaceUrl, useWorkspaceUrl } from '../state/workspaceUrl';

/**
 * What the definition of a step declares for the steps after it: the output
 * variable and the named outputs. It needs no run. The link selects the last
 * run in the same tab (a push of `?run=<id>`), else the run of the host when
 * the last run is not known. It shows only when the workspace has a run id.
 */
export function StepDeclared({
  step,
  node,
}: {
  step: string;
  node: ApiNode | undefined;
}) {
  const { ts } = useI18n();
  const { fileName, dagData, run } = useWorkspaceData('StepDeclared');
  const [url] = useWorkspaceUrl();
  const dag = dagData.dag;

  const definition =
    node?.step ??
    dag?.steps?.find((s) => s.name === step) ??
    Object.values(dag?.handlerOn ?? {}).find((s) => s?.name === step);

  const rows: { token: string; kind?: string }[] = [];
  if (definition?.output) rows.push({ token: `\${${definition.output}}` });
  for (const out of definition?.outputs ?? []) {
    rows.push({
      token: `\${steps.${definition?.id ?? definition?.name}.outputs.${out.name}}`,
      kind: out.type,
    });
  }
  const runId = run.latestRef?.runId ?? run.ref?.runId ?? null;
  if (rows.length === 0 && runId === null) return null;

  const runUrl =
    runId === null
      ? null
      : buildWorkspaceUrl(fileName, {
          ...url,
          mode: 'workspace',
          popout: null,
          run: runId,
          step,
          ndv: null,
          dock: null,
          stream: null,
          item: null,
          body: null,
          sub: null,
        });

  return (
    <section
      data-testid="ww-step-declared"
      aria-label={ts('Declared outputs')}
      className="space-y-1"
    >
      {rows.length > 0 && (
        <>
          <div className="text-xs font-medium text-muted-foreground">
            {ts('Declared outputs')}
          </div>
          <ul className="space-y-0.5">
            {rows.map((row) => (
              <li key={row.token} className="flex items-baseline gap-2 text-xs">
                <code className="min-w-0 break-all font-mono">{row.token}</code>
                {row.kind && (
                  <span className="text-muted-foreground">{row.kind}</span>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
      {runUrl && (
        <Link
          to={runUrl}
          className="inline-block text-xs underline underline-offset-2"
        >
          {ts('Open last run')}
        </Link>
      )}
    </section>
  );
}
