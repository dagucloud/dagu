// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Ban, CircleCheck, CircleDashed, CircleSlash } from 'lucide-react';

type AgentTask = components['schemas']['AgentTask'];

interface TaskChecklistTabProps {
  tasks: AgentTask[];
}

/** One glyph per task status, so the list reads at a glance. */
function TaskStatusIcon({ status }: { status: AgentTask['status'] }) {
  const base = 'mt-0.5 h-4 w-4 shrink-0';
  switch (status) {
    case 'completed':
      return (
        <CircleCheck
          className={`${base} text-success`}
          aria-label="completed"
        />
      );
    case 'skipped':
      return (
        <CircleSlash
          className={`${base} text-muted-foreground`}
          aria-label="skipped"
        />
      );
    case 'failed':
      return <Ban className={`${base} text-error`} aria-label="failed" />;
    default:
      return (
        <CircleDashed
          className={`${base} text-muted-foreground/60`}
          aria-label="open"
        />
      );
  }
}

/**
 * Goal checklist of an agent DAG-run. The run ends once no task is open,
 * and succeeds unless one was settled as failed.
 */
export function TaskChecklistTab({ tasks }: TaskChecklistTabProps) {
  const settled = tasks.filter((task) => task.status !== 'open').length;

  return (
    <div className="flex flex-col gap-2 p-2">
      <div className="text-muted-foreground text-xs">
        {settled} of {tasks.length} tasks settled
      </div>

      <div className="divide-border bg-card divide-y rounded border">
        {tasks.map((task) => (
          <div key={task.name} className="flex gap-2 px-3 py-2">
            <TaskStatusIcon status={task.status} />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="text-foreground text-sm font-medium break-words whitespace-normal">
                  {task.name}
                </span>
                {task.status !== 'open' ? (
                  <span className="text-muted-foreground text-xs">
                    {task.status}
                  </span>
                ) : null}
              </div>
              {task.description ? (
                <div className="text-muted-foreground text-xs break-words whitespace-normal">
                  {task.description}
                </div>
              ) : null}
              {task.status !== 'open' && task.reason ? (
                <div className="text-muted-foreground/80 mt-0.5 text-xs break-words whitespace-normal italic">
                  {task.reason}
                </div>
              ) : null}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
