// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  FilePen,
  FilePlus,
  FileText,
  Search,
  SquareTerminal,
  type LucideIcon,
} from 'lucide-react';
import type { BuiltinToolName } from './model';

const ICONS: Record<BuiltinToolName, LucideIcon> = {
  read: FileText,
  write: FilePlus,
  edit: FilePen,
  search: Search,
  shell: SquareTerminal,
};

/** Icon of a built-in tool of a CLI agent, on the canvas. */
export function builtinIcon(name: BuiltinToolName): LucideIcon {
  return ICONS[name];
}

type Translate = (text: string) => string;

/** One line that tells what a built-in tool lets the agent do. */
export function builtinHint(ts: Translate, name: BuiltinToolName): string {
  switch (name) {
    case 'read':
      return ts('Read the content of files in the working directory.');
    case 'write':
      return ts('Create files and replace their content.');
    case 'edit':
      return ts('Change a part of a file.');
    case 'search':
      return ts('Search file names and file content.');
    case 'shell':
      return ts('Run shell commands. Only the allowed commands run.');
  }
}

/** The name of a built-in tool, as the canvas and the inspector show it. */
export function builtinLabel(ts: Translate, name: BuiltinToolName): string {
  switch (name) {
    case 'read':
      return ts('Read files');
    case 'write':
      return ts('Write files');
    case 'edit':
      return ts('Edit files');
    case 'search':
      return ts('Search files');
    case 'shell':
      return ts('Shell commands');
  }
}
