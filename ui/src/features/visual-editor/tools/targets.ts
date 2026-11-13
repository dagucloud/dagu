// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import type { ToolEntryModel } from './model';

/** A page of the app that holds what an entry names. */
export interface PageLink {
  /** The name of the DAG or skill that the page shows. */
  name: string;
  /** The route, without the base path of the app. */
  path: string;
}

/** A name that a `${...}` reference gives at run time has no page. */
function plain(name: string | undefined): name is string {
  return name !== undefined && name.trim() !== '' && !name.includes('${');
}

function link(prefix: string, name: string | undefined): PageLink | null {
  return plain(name)
    ? { name, path: `${prefix}${encodeURIComponent(name)}` }
    : null;
}

/** The page of a DAG that is used as a tool. */
export function dagLink(name: string | undefined): PageLink | null {
  return link('/dags/', name);
}

/** The page where a skill is edited. */
export function skillLink(name: string | undefined): PageLink | null {
  return link('/ai/skills/', name);
}

/**
 * The page of a sub-run of the shown run: the run page of the root run, on
 * its sub-run stack. `remoteNode` is the node that holds the run.
 */
export function subRunLink(
  root: { name: string; id: string; fileName?: string | undefined },
  subRunId: string,
  remoteNode: string
): PageLink {
  return {
    name: subRunId,
    path: buildDAGRunPageURL({
      rootDAGRunName: root.name,
      rootDAGRunId: root.id,
      ...(root.fileName ? { fileName: root.fileName } : {}),
      remoteNode,
      subDAGRunId: subRunId,
    }),
  };
}

/**
 * Where a tool entry lives. A DAG has one page. An MCP server has no page.
 */
export function entryLink(entry: ToolEntryModel): PageLink | null {
  return entry.kind === 'dag' ? dagLink(entry.label) : null;
}

/** An input that a tool declares: its name and, when known, what it is for. */
export interface ToolInputHint {
  name: string;
  description?: string;
}
