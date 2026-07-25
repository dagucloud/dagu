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
      <div className="text-muted-foreground text-xs">
        {doneCount} of {tasks.length} tasks complete
      </div>

      <div className="divide-border bg-card divide-y rounded border">
        {tasks.map((task) => (
          <div key={task.name} className="flex gap-2 px-3 py-2">
            {task.done ? (
              <CircleCheck
                className="text-success mt-0.5 h-4 w-4 shrink-0"
                aria-label="complete"
              />
            ) : (
              <CircleDashed
                className="text-muted-foreground mt-0.5 h-4 w-4 shrink-0"
                aria-label="open"
              />
            )}
            <div className="min-w-0 flex-1">
              <div className="text-foreground text-sm font-medium break-words whitespace-normal">
                {task.name}
              </div>
              {task.description ? (
                <div className="text-muted-foreground text-xs break-words whitespace-normal">
                  {task.description}
                </div>
              ) : null}
              {task.done && task.reason ? (
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
