// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, DocTreeNodeResponseType } from '@/api/v1/schema';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import {
  docMutationTargetForTreeNode,
  isWorkspaceRootTreeNode,
  type DocMutationTarget,
} from '../lib/doc-mutation';
import {
  ChevronDown,
  ChevronRight,
  FileText,
  Folder,
  FolderOpen,
  MoreHorizontal,
  Pencil,
  Plus,
  Trash2,
} from 'lucide-react';
import React, { useCallback, useRef, useEffect } from 'react';
import { NodeRendererProps } from 'react-arborist';

type DocTreeNodeResponse = components['schemas']['DocTreeNodeResponse'];

export type ContextAction =
  | { type: 'create'; parentDir: string; workspace?: string | null }
  | {
      type: 'rename';
      docPath: string;
      title: string;
      workspace?: string | null;
    }
  | {
      type: 'delete';
      docPath: string;
      title: string;
      isDir: boolean;
      hasChildren: boolean;
      workspace?: string | null;
    }
  | { type: 'deleteBatch'; targets: DocMutationTarget[] };

type Props = NodeRendererProps<DocTreeNodeResponse> & {
  onContextAction: (action: ContextAction) => void;
  canWrite: boolean;
  activeDocPath?: string | null;
  selectedIds?: string[];
  selectedTargets?: DocMutationTarget[];
};

function DocArboristNode({
  node,
  style,
  dragHandle,
  onContextAction,
  canWrite,
  activeDocPath,
  selectedIds = [],
  selectedTargets = [],
}: Props) {
  const isDir = node.data.type === DocTreeNodeResponseType.directory;
  const displayTitle = node.data.title || node.data.name;
  const hasChildren = !!(node.data.children && node.data.children.length > 0);
  const isActiveDoc = !isDir && node.id === activeDocPath;
  const mutationTarget = docMutationTargetForTreeNode(
    node.id,
    node.data.workspace ?? null
  );
  const isWorkspaceRoot = isWorkspaceRootTreeNode(
    node.id,
    node.data.workspace ?? null
  );
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input when editing starts
  useEffect(() => {
    if (node.isEditing && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [node.isEditing]);

  const handleClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    if (node.isEditing) return;
    if (e.ctrlKey || e.metaKey) {
      node.selectMulti();
      return;
    }
    if (e.shiftKey) {
      node.selectContiguous();
      return;
    }
    node.select();
    if (isDir) {
      node.toggle();
    } else {
      node.activate();
    }
  }, [isDir, node]);

  const submitOrReset = useCallback(() => {
    const value = inputRef.current?.value?.trim();
    if (value) {
      node.submit(value);
    } else {
      node.reset();
    }
  }, [node]);

  const handleSubmit = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      submitOrReset();
    },
    [submitOrReset]
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        node.reset();
      }
    },
    [node]
  );

  return (
    <div
      ref={dragHandle}
      style={style}
      className={cn(
        'flex items-center gap-1 py-1 pr-1 cursor-pointer group rounded-sm',
        'hover:bg-accent/50',
        node.isSelected && !isActiveDoc && !node.isEditing && 'bg-primary/10',
        isActiveDoc && !node.isEditing && 'bg-accent text-accent-foreground',
        node.willReceiveDrop && 'bg-primary/10 ring-1 ring-primary/30',
        node.isDragging && 'opacity-50'
      )}
      onClick={handleClick}
    >
      {isDir ? (
        <>
          {node.isOpen ? (
            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
          {node.isOpen ? (
            <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
        </>
      ) : (
        <>
          <span className="w-3 shrink-0" />
          <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        </>
      )}

      {node.isEditing ? (
        <form onSubmit={handleSubmit} className="flex-1 min-w-0">
          <input
            ref={inputRef}
            type="text"
            defaultValue={node.data.name}
            onKeyDown={handleKeyDown}
            onBlur={submitOrReset}
            className="w-full text-sm bg-background border border-border rounded px-1 py-0 outline-none focus:ring-1 focus:ring-primary"
          />
        </form>
      ) : (
        <span className="flex-1 text-sm truncate select-none">
          {displayTitle}
        </span>
      )}

      {canWrite && !node.isEditing && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              className="shrink-0 p-0.5 rounded-sm opacity-0 group-hover:opacity-100 hover:bg-muted-foreground/20 focus-visible:opacity-100"
              onClick={(e) => e.stopPropagation()}
            >
              <MoreHorizontal className="h-3.5 w-3.5" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40">
            {isDir && (
              <DropdownMenuItem
                onClick={(e) => {
                  e.stopPropagation();
                  onContextAction({
                    type: 'create',
                    parentDir: mutationTarget.path,
                    workspace: mutationTarget.workspace,
                  });
                }}
              >
                <Plus className="h-3.5 w-3.5 mr-2" />
                New Document
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              disabled={isWorkspaceRoot}
              onClick={(e) => {
                e.stopPropagation();
                if (isWorkspaceRoot) return;
                onContextAction({
                  type: 'rename',
                  docPath: mutationTarget.path,
                  title: displayTitle,
                  workspace: mutationTarget.workspace,
                });
              }}
            >
              <Pencil className="h-3.5 w-3.5 mr-2" />
              Rename
            </DropdownMenuItem>
            <DropdownMenuItem
              onClick={(e) => {
                e.stopPropagation();
                if (selectedIds.length > 1 && node.isSelected) {
                  onContextAction({
                    type: 'deleteBatch',
                    targets: selectedTargets,
                  });
                } else {
                  onContextAction({
                    type: 'delete',
                    docPath: mutationTarget.path,
                    title: displayTitle,
                    isDir,
                    hasChildren,
                    workspace: mutationTarget.workspace,
                  });
                }
              }}
              disabled={
                isWorkspaceRoot ||
                (selectedIds.length > 1 &&
                  node.isSelected &&
                  selectedTargets.length === 0)
              }
            >
              <Trash2 className="h-3.5 w-3.5 mr-2" />
              {selectedIds.length > 1 && node.isSelected
                ? `Delete ${selectedTargets.length} items`
                : 'Delete'}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}

export default DocArboristNode;
