// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { CircleCheck, CircleDashed } from 'lucide-react';

type ControllerTask = components['schemas']['ControllerTask'];

interface TaskChecklistTabProps {
  tasks: ControllerTask[];
}

/**
 * Goal checklist of a controller DAG-run. The run concludes successfully only
 * once every task is marked complete by the controller.
 */
export function TaskChecklistTab({ tasks }: TaskChecklistTabProps) {
  const doneCount = tasks.filter((task) => task.done).length;

  return (
    <div className="flex flex-col gap-2 p-2">
      <div className="text-xs text-slate-600 dark:text-slate-400">
        {doneCount} of {tasks.length} tasks complete
      </div>

      <div className="divide-y divide-slate-200 rounded border border-slate-200 dark:divide-slate-700 dark:border-slate-700">
        {tasks.map((task) => (
          <div key={task.name} className="flex gap-2 px-2 py-1.5">
            {task.done ? (
              <CircleCheck
                className="mt-0.5 h-4 w-4 shrink-0 text-green-600 dark:text-green-500"
                aria-label="complete"
              />
            ) : (
              <CircleDashed
                className="mt-0.5 h-4 w-4 shrink-0 text-slate-400 dark:text-slate-500"
                aria-label="open"
              />
            )}
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium whitespace-normal break-words text-slate-800 dark:text-slate-200">
                {task.name}
              </div>
              {task.description ? (
                <div className="text-xs whitespace-normal break-words text-slate-600 dark:text-slate-400">
                  {task.description}
                </div>
              ) : null}
              {task.done && task.reason ? (
                <div className="mt-0.5 text-xs whitespace-normal break-words text-slate-500 dark:text-slate-500">
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
