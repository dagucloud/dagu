// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ChevronRight,
  File,
  FileImage,
  FileText,
  Folder,
  Plus,
  Upload,
} from 'lucide-react';
import { useMemo, useState, type ReactElement } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import {
  buildFileTree,
  formatBytes,
  imageType,
  type FileNode,
} from '@/features/skills/skillFiles';
import {
  fileIssueState,
  type SkillFile,
  type SkillIssue,
} from '@/features/skills/skillIssues';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

function FileIcon({ file }: { file: SkillFile }): ReactElement {
  const Icon = imageType(file.path) ? FileImage : file.text ? FileText : File;
  return (
    <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
  );
}

/**
 * The files of a skill as a tree: SKILL.md, then references/, assets/ and
 * scripts/. A click selects a file. A dot marks a file with an error or a
 * warning, and a file with unsaved changes.
 */
export function SkillFileTree({
  files,
  active,
  issues,
  dirtyPaths,
  canWrite,
  busy,
  onSelect,
  onAdd,
  onUpload,
  className,
}: {
  files: readonly SkillFile[];
  active: string;
  issues: readonly SkillIssue[];
  dirtyPaths: ReadonlySet<string>;
  canWrite: boolean;
  busy: boolean;
  onSelect(path: string): void;
  onAdd(): void;
  onUpload(): void;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const tree = useMemo(() => buildFileTree(files), [files]);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const toggle = (path: string) =>
    setCollapsed((previous) => {
      const next = new Set(previous);
      if (!next.delete(path)) next.add(path);
      return next;
    });

  const render = (node: FileNode, depth: number): ReactElement => {
    const pad = { paddingLeft: `${0.5 + depth * 0.875}rem` };
    if (node.kind === 'dir') {
      const closed = collapsed.has(node.path);
      return (
        <li key={node.path}>
          <button
            type="button"
            aria-expanded={!closed}
            onClick={() => toggle(node.path)}
            style={pad}
            className="flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-xs text-muted-foreground hover:bg-muted"
          >
            <ChevronRight
              className={cn(
                'size-3 shrink-0 transition-transform',
                !closed && 'rotate-90'
              )}
              aria-hidden
            />
            <Folder className="size-3.5 shrink-0" aria-hidden />
            <span className="min-w-0 flex-1 truncate font-mono">
              {node.name}/
            </span>
            <span className="shrink-0 tabular-nums">{node.count}</span>
          </button>
          {!closed && node.children.length > 0 && (
            <ul>{node.children.map((child) => render(child, depth + 1))}</ul>
          )}
        </li>
      );
    }
    const state = fileIssueState(issues, node.path);
    return (
      <li key={node.path}>
        <button
          type="button"
          data-path={node.path}
          aria-current={active === node.path ? 'true' : undefined}
          onClick={() => onSelect(node.path)}
          style={{ paddingLeft: `${0.5 + depth * 0.875 + 0.875}rem` }}
          className={cn(
            'flex w-full items-center gap-1.5 rounded py-1 pr-2 text-left text-xs transition-colors',
            active === node.path
              ? 'bg-accent text-accent-foreground'
              : 'hover:bg-muted'
          )}
        >
          <FileIcon file={node.file} />
          <span className="min-w-0 flex-1 truncate font-mono">{node.name}</span>
          {dirtyPaths.has(node.path) && (
            <span
              role="img"
              aria-label={ts('Unsaved changes')}
              className="size-1.5 shrink-0 rounded-full bg-primary"
            />
          )}
          {state && (
            <span
              role="img"
              aria-label={state === 'error' ? ts('Error') : ts('Warning')}
              className={cn(
                'size-1.5 shrink-0 rounded-full',
                state === 'error' ? 'bg-destructive' : 'bg-warning'
              )}
            />
          )}
          <span className="shrink-0 tabular-nums text-[10px] text-muted-foreground">
            {formatBytes(node.file.size)}
          </span>
        </button>
      </li>
    );
  };

  return (
    <nav
      aria-label={ts('Files of the skill')}
      data-testid="skill-files"
      className={cn('flex min-h-0 flex-col gap-1', className)}
    >
      <div className="flex items-center gap-1 px-1">
        <span className="text-xs font-medium text-muted-foreground">
          {ts('Files ({count})', { count: files.length })}
        </span>
        {canWrite && (
          <span className="ml-auto flex items-center gap-0.5">
            <IconButton
              size="sm"
              label={ts('Add a file')}
              icon={Plus}
              disabled={busy}
              onClick={onAdd}
            />
            <IconButton
              size="sm"
              label={ts('Upload a file')}
              icon={Upload}
              disabled={busy}
              onClick={onUpload}
            />
          </span>
        )}
      </div>
      <ul className="min-h-0 flex-1 overflow-y-auto">
        {tree.map((node) => render(node, 0))}
      </ul>
    </nav>
  );
}
