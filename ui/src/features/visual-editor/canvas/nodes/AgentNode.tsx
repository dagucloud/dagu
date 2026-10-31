// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { Ban, Bot, CircleCheck, CircleDashed, CircleSlash } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useAgentRun } from '../agentRunContext';
import { Handle, type NodeProps } from '../engine';
import { HUB_MAX_TASKS } from '../layout';
import {
  SOURCE_HANDLE,
  type AgentTaskState,
  type AgentTaskView,
  type StepFlowNode,
} from '../types';
import {
  HANDLE_CLASS,
  RunDot,
  cardStateClass,
  handlePosition,
} from './NodeParts';
import { useCanvasActions } from '../context';

const TASK_ICON_CLASS = 'h-3 w-3 shrink-0';

function TaskIcon({ state }: { state?: AgentTaskState }) {
  const { ts } = useI18n();
  switch (state) {
    case 'completed':
      return (
        <CircleCheck
          className={cn(TASK_ICON_CLASS, 'text-success')}
          aria-label={ts('completed')}
        />
      );
    case 'failed':
      return (
        <Ban
          className={cn(TASK_ICON_CLASS, 'text-destructive')}
          aria-label={ts('failed')}
        />
      );
    case 'skipped':
      return (
        <CircleSlash
          className={cn(TASK_ICON_CLASS, 'text-muted-foreground')}
          aria-label={ts('skipped')}
        />
      );
    default:
      return (
        <CircleDashed
          className={cn(TASK_ICON_CLASS, 'text-muted-foreground/60')}
          aria-label={state === 'open' ? ts('Task pending') : undefined}
          aria-hidden={state === undefined ? true : undefined}
        />
      );
  }
}

function TaskRow({
  task,
  state,
}: {
  task: AgentTaskView;
  state?: AgentTaskState;
}) {
  return (
    <li
      data-testid="ve-canvas-hub-task"
      data-state={state}
      title={task.description ? `${task.name}\n${task.description}` : task.name}
      className="flex min-w-0 items-center gap-1.5 text-xs"
    >
      <TaskIcon state={state} />
      <span
        className={cn(
          'min-w-0 truncate',
          state === 'completed' && 'text-muted-foreground'
        )}
      >
        {task.name}
      </span>
    </li>
  );
}

/**
 * The agent of an agent DAG: one hub over the grid of its tools. It shows
 * the model, the first line of the system prompt, the limit of tool calls and
 * the checklist of the tasks. With a run it shows the run state, the turns of
 * the agent and which tasks are done. It has no form: its settings are in
 * the YAML.
 */
function AgentNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { node } = data;
  const { ts } = useI18n();
  const { direction } = useCanvasActions();
  const tasks = node.taskCount;
  const hub = node.hub;
  const listed = hub?.tasks.slice(0, HUB_MAX_TASKS) ?? [];
  const hidden = (hub?.tasks.length ?? 0) - listed.length;
  const model = [hub?.provider, hub?.model].filter(Boolean).join(' / ');
  const agentRun = useAgentRun();
  // A task that the run does not list is still open.
  const stateOf = (task: AgentTaskView): AgentTaskState | undefined =>
    agentRun ? (agentRun.tasks[task.name] ?? 'open') : undefined;
  const settled =
    hub?.tasks.filter((task) => {
      const state = stateOf(task);
      return state !== undefined && state !== 'open';
    }).length ?? 0;

  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="agent"
      className={cn(
        'group relative flex h-full w-full flex-col gap-1 rounded-xl border-2 border-primary/40 bg-primary/5 px-3 py-2 text-card-foreground',
        cardStateClass(node, selected, data.highlighted)
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
          <Bot className="h-4 w-4" aria-hidden="true" />
        </span>
        <span className="text-sm font-semibold">{ts('Agent')}</span>
        {model && (
          <span
            data-testid="ve-canvas-hub-model"
            title={model}
            className="min-w-0 truncate rounded-full border bg-card px-2 text-xs leading-5 text-muted-foreground"
          >
            {model}
          </span>
        )}
        <span className="flex-1" />
        {hub?.maxIterations !== undefined && (
          <span
            data-testid="ve-canvas-hub-limit"
            className="shrink-0 text-xs text-muted-foreground"
          >
            {ts('Up to {count} tool calls', { count: hub.maxIterations })}
          </span>
        )}
        {agentRun && (
          <span
            data-testid="ve-canvas-hub-iterations"
            className="shrink-0 rounded bg-primary/15 px-1.5 text-xs font-medium leading-5 text-primary"
          >
            {ts('Iterations: {count}', { count: agentRun.iterations })}
          </span>
        )}
        <RunDot run={node.run} />
      </div>
      {hub?.systemFirstLine && (
        <p
          data-testid="ve-canvas-hub-system"
          title={hub.systemFirstLine}
          className="truncate text-xs italic text-muted-foreground"
        >
          {hub.systemFirstLine}
        </p>
      )}
      {tasks !== undefined && (
        <div className="flex min-w-0 items-baseline gap-2 text-xs text-muted-foreground">
          <span className="truncate">
            {ts(tasks === 1 ? '{count} task' : '{count} tasks', {
              count: tasks,
            })}
          </span>
          {agentRun && (
            <span data-testid="ve-canvas-hub-settled">
              {ts('{settled} of {total} tasks settled', {
                settled,
                total: tasks,
              })}
            </span>
          )}
        </div>
      )}
      {listed.length > 0 && (
        <ul
          aria-label={ts('Tasks')}
          className="grid min-w-0 grid-cols-2 gap-x-3 gap-y-0.5"
        >
          {listed.map((task) => (
            <TaskRow key={task.name} task={task} state={stateOf(task)} />
          ))}
          {hidden > 0 && (
            <li
              className="text-xs text-muted-foreground"
              title={hub?.tasks
                .slice(HUB_MAX_TASKS)
                .map((task) => task.name)
                .join('\n')}
            >
              {ts('+{count} more', { count: hidden })}
            </li>
          )}
        </ul>
      )}
      <Handle
        id={SOURCE_HANDLE}
        type="source"
        position={handlePosition(direction, 'source')}
        isConnectable={false}
        className={HANDLE_CLASS}
      />
    </div>
  );
}

export const AgentNode = memo(AgentNodeComponent);
