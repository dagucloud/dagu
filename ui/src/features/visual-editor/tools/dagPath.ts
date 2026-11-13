// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiStep } from '../contracts';

/**
 * The DAGs that the enlarged view has opened, from the DAG that the tool
 * names to the one that shows now. The DAG that holds the step is the root
 * of the path: it is not in the list, but a DAG that calls it is a loop too.
 */
export interface DagPath {
  /** The DAG that holds the calling step. */
  host: string;
  /** The opened DAGs, first the tool DAG. */
  opened: readonly string[];
}

/** The path of a DAG tool: its DAG is the first and only one opened. */
export function startPath(host: string, tool: string): DagPath {
  return { host, opened: [tool.trim()] };
}

/** The DAG that shows now. */
export function currentDag(path: DagPath): string {
  return path.opened[path.opened.length - 1] ?? path.host;
}

/**
 * True when opening `name` from the path would walk a DAG again: it is
 * the host, or it is open already. The view then shows the call once, as a
 * recursive call, and never opens it.
 */
export function isRecursive(path: DagPath, name: string): boolean {
  const target = name.trim();
  return target === path.host || path.opened.includes(target);
}

/** The path with `name` opened, or the same path when that would loop. */
export function openDag(path: DagPath, name: string): DagPath {
  return isRecursive(path, name)
    ? path
    : { host: path.host, opened: [...path.opened, name.trim()] };
}

/** The path back to its `depth`-th opened DAG (1 is the tool DAG). */
export function backTo(path: DagPath, depth: number): DagPath {
  return {
    host: path.host,
    opened: path.opened.slice(0, Math.max(1, depth)),
  };
}

/** What the enlarged view tells about one step of the DAG that it shows. */
export interface StepFacts {
  name: string;
  description: string;
  /** The executor or kind of the step: `call`, `router`, an action type, or `command`. */
  action: string;
  /** The first lines of its command or script. */
  command: string[];
  depends: string[];
  /** One line for each precondition: the condition and what it expects. */
  conditions: string[];
  /** The DAG that the step calls, when it does. */
  call: string | undefined;
  parallel: boolean;
}

/** The lines of the command preview of a step. */
const COMMAND_LINES = 4;

function commandLines(step: ApiStep): string[] {
  const first = step.commands?.[0] as
    | { command?: unknown; args?: unknown }
    | undefined;
  const text =
    first && typeof first.command === 'string'
      ? [first.command, ...(Array.isArray(first.args) ? first.args : [])].join(
          ' '
        )
      : (step.script ?? '');
  return text
    .split('\n')
    .filter((line) => line.trim() !== '')
    .slice(0, COMMAND_LINES);
}

/** The facts of a step of a DAG that the API describes, for the step area of the enlarged view. */
export function stepFacts(step: ApiStep): StepFacts {
  const type = step.executorConfig?.type;
  const action = step.call
    ? 'call'
    : step.router
      ? 'router'
      : type && type !== 'command' && type !== 'shell'
        ? type
        : 'command';
  return {
    name: step.name,
    description: step.description?.trim() ?? '',
    action,
    command: commandLines(step),
    depends: [...(step.depends ?? []), ...(step.inferredDepends ?? [])],
    conditions: (step.preconditions ?? []).map((item) => {
      const subject = item.condition ?? item.eval ?? '';
      return item.expected !== undefined && item.expected !== ''
        ? `${subject} = ${item.expected}`
        : subject;
    }),
    call: step.call?.trim() || undefined,
    parallel: !!step.parallel,
  };
}
