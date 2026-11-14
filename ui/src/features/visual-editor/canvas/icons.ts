// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Boxes,
  Box,
  Braces,
  Cloud,
  Container,
  Database,
  File,
  GitBranch,
  Globe,
  Hourglass,
  Mail,
  MessageSquare,
  Server,
  Split,
  Terminal,
  Workflow,
  type LucideIcon,
} from 'lucide-react';
import type { CanvasNode } from '../contracts';

const SQL_NAMESPACES = new Set([
  'sql',
  'postgres',
  'postgresql',
  'mysql',
  'sqlite',
  'duckdb',
  'clickhouse',
  'redis',
]);

/** Text before the first dot of an action name, or null when there is none. */
export function actionNamespace(action?: string): string | null {
  if (!action) {
    return null;
  }
  const dot = action.indexOf('.');
  const namespace = dot < 0 ? action : action.slice(0, dot);
  return namespace === '' ? null : namespace;
}

const NAMESPACE_ICONS: Record<string, LucideIcon> = {
  http: Globe,
  s3: Cloud,
  file: File,
  git: GitBranch,
  docker: Container,
  container: Container,
  k8s: Boxes,
  kubernetes: Boxes,
  mail: Mail,
  ssh: Server,
  wait: Hourglass,
  router: Split,
  dag: Workflow,
  chat: MessageSquare,
  jq: Braces,
};

/** Icon of a canvas node: the kind decides first, then the action namespace. */
export function actionIcon(
  node: Pick<CanvasNode, 'kind' | 'action'>
): LucideIcon {
  if (node.kind === 'router') {
    return Split;
  }
  if (node.kind === 'call') {
    return Workflow;
  }
  if (node.kind === 'run') {
    return Terminal;
  }
  const namespace = actionNamespace(node.action)?.toLowerCase();
  if (!namespace) {
    return Box;
  }
  if (SQL_NAMESPACES.has(namespace)) {
    return Database;
  }
  // The namespace is user text: only an own key of the table is an icon.
  return Object.prototype.hasOwnProperty.call(NAMESPACE_ICONS, namespace)
    ? NAMESPACE_ICONS[namespace]!
    : Box;
}
