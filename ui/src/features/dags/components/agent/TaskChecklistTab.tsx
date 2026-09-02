// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Ban, CircleCheck, CircleDashed, CircleSlash } from 'lucide-react';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

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
        <I18nProps>
          <CircleCheck
            className={`${base} text-success`}
            aria-label="completed"
          />
        </I18nProps>
      );
    case 'skipped':
      return (
        <I18nProps>
          <CircleSlash
            className={`${base} text-muted-foreground`}
            aria-label="skipped"
          />
        </I18nProps>
      );
    case 'failed':
      return (
        <I18nProps>
          <Ban className={`${base} text-error`} aria-label="failed" />
        </I18nProps>
      );
    default:
      return (
        <I18nProps>
          <CircleDashed
            className={`${base} text-muted-foreground/60`}
            aria-label="open"
          />
        </I18nProps>
      );
  }
}

/**
 * Goal checklist of an agent DAG-run. The run ends once no task is open,
 * and succeeds unless one was settled as failed.
 */
export function TaskChecklistTab({ tasks }: TaskChecklistTabProps) {
  const { ts } = useI18n();
  const settled = tasks.filter((task) => task.status !== 'open').length;

  return (
    <div className="flex flex-col gap-2 p-2">
      <div className="text-muted-foreground text-xs">
        {ts('{settled} of {total} tasks settled', {
          settled,
          total: tasks.length,
        })}
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
                    <I18nText text={task.status} />
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
