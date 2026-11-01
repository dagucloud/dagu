// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { components } from '../../../../api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import DAGStepTable from '../dag-details/DAGStepTable';
import { AgentSpecOverview } from './AgentSpecOverview';
import DAGAttributes from './DAGAttributes';

type DAGDetails = components['schemas']['DAGDetails'];
type Step = components['schemas']['Step'];

/**
 * In a panel narrower than 30rem the 960px table would clip its right
 * columns. The rows then stack: one cell under the other, with no header.
 */
export const STEP_TABLE_STACK_CLASS =
  '@max-[30rem]:[&_table]:min-w-0 @max-[30rem]:[&_table]:block ' +
  '@max-[30rem]:[&_thead]:hidden @max-[30rem]:[&_tbody]:block ' +
  '@max-[30rem]:[&_tr]:block @max-[30rem]:[&_td]:block ' +
  '@max-[30rem]:[&_td]:w-full @max-[30rem]:[&_td]:py-1 ' +
  '@max-[30rem]:[&_td:first-child]:hidden @max-[30rem]:[&_td:empty]:hidden ' +
  '@max-[30rem]:[&_td:nth-child(2)]:pt-3 @max-[30rem]:[&_td:last-child]:pb-3';

/** The lifecycle handlers that the DAG defines, in a fixed order. */
export function getHandlers(dag?: DAGDetails): Step[] {
  const handlers = dag?.handlerOn;
  return [
    handlers?.success,
    handlers?.failure,
    handlers?.abort,
    handlers?.exit,
  ].filter((step): step is Step => !!step);
}

/**
 * What the workflow is, apart from one step: its description, schedule,
 * parameters and limits, then the table of its handlers. The steps are on the
 * canvas, not here. It is the content of the Workflow panel of the visual
 * editor.
 */
export function WorkflowPanel({ dag }: { dag: DAGDetails }): ReactElement {
  const { ts } = useI18n();
  const handlers = getHandlers(dag);
  return (
    <>
      {dag.description && (
        <p className="whitespace-pre-line break-words text-sm text-muted-foreground">
          {dag.description}
        </p>
      )}
      <DAGAttributes dag={dag} />
      {/* The steps of an agent DAG are the actions that it can choose. */}
      {dag.type === 'agent' && <AgentSpecOverview dag={dag} />}
      {handlers.length > 0 && (
        <section className="space-y-2" aria-label={ts('Handlers')}>
          <h3 className="text-sm font-medium text-foreground">
            {ts('Handlers')}
          </h3>
          <div
            data-testid="ve-workflow-handlers"
            className={`@container min-w-0 ${STEP_TABLE_STACK_CLASS}`}
          >
            <DAGStepTable steps={handlers} />
          </div>
        </section>
      )}
    </>
  );
}

export default WorkflowPanel;
