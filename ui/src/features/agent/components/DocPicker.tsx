// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  forwardRef,
  useContext,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';

import { FileText, Search, X } from 'lucide-react';

import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { cn } from '@/lib/utils';
import { workspaceSelectionQuery } from '@/lib/workspace';

export interface DocRef {
  id: string;
  title: string;
}

interface DocEntry {
  id: string;
  title: string;
  description?: string;
}

export interface DocPickerHandle {
  handleKeyDown: (e: KeyboardEvent<HTMLTextAreaElement>) => boolean;
}

interface DocPickerProps {
  selectedDocs: DocRef[];
  onSelect: (doc: DocRef) => void;
  onRemove: (id: string) => void;
  isOpen: boolean;
  onClose: () => void;
  filterQuery: string;
  currentPageDoc?: DocRef | null;
  disabled?: boolean;
}

export const DocPicker = forwardRef<DocPickerHandle, DocPickerProps>(
  function DocPicker(
    { selectedDocs, onSelect, onRemove, isOpen, onClose, filterQuery, currentPageDoc, disabled },
    ref
  ) {
    const client = useClient();
    const appBarContext = useContext(AppBarContext);
    const workspaceQuery = useMemo(
      () => workspaceSelectionQuery(appBarContext?.workspaceSelection),
      [appBarContext?.workspaceSelection]
    );
    const dropdownRef = useRef<HTMLDivElement>(null);
    const [docs, setDocs] = useState<DocEntry[]>([]);
    const [highlightIndex, setHighlightIndex] = useState(0);

    // Fetch docs on mount
    useEffect(() => {
      const controller = new AbortController();
      const remoteNode = appBarContext?.selectedRemoteNode || 'local';

      async function fetchDocs() {
        try {
          const { data } = await client.GET('/docs', {
            params: {
              query: {
                remoteNode,
                flat: true,
                perPage: 200,
                ...workspaceQuery,
              },
            },
            signal: controller.signal,
          });
          if (!data?.items) return;
          setDocs(
            data.items.map((item) => ({
              id: item.id,
              title: item.title,
              description: item.description || undefined,
            }))
          );
        } catch {
          // Best-effort
        }
      }
      fetchDocs();

      return () => controller.abort();
    }, [client, appBarContext?.selectedRemoteNode, workspaceQuery]);

    // Click-outside handler
    useEffect(() => {
      if (!isOpen) return;
      function handleClickOutside(event: MouseEvent) {
        if (
          dropdownRef.current &&
          !dropdownRef.current.contains(event.target as Node)
        ) {
          onClose();
        }
      }
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }, [isOpen, onClose]);

    const selectedIds = useMemo(
      () => new Set(selectedDocs.map((d) => d.id)),
      [selectedDocs]
    );

    const filtered = useMemo(() => {
      const available = docs.filter(
        (d) => !selectedIds.has(d.id) && d.id !== currentPageDoc?.id
      );
      const q = filterQuery.trim().toLowerCase();
      if (!q) return available;
      return available.filter(
        (d) =>
          d.id.toLowerCase().includes(q) ||
          d.title.toLowerCase().includes(q) ||
          (d.description?.toLowerCase().includes(q) ?? false)
      );
    }, [docs, filterQuery, selectedIds, currentPageDoc]);

    // Reset highlight when filter changes
    useEffect(() => {
      setHighlightIndex(0);
    }, [filterQuery]);

    useImperativeHandle(ref, () => ({
      handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>): boolean {
        if (!isOpen) return false;
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          setHighlightIndex((prev) => Math.min(prev + 1, filtered.length - 1));
          return true;
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault();
          setHighlightIndex((prev) => Math.max(prev - 1, 0));
          return true;
        }
        if (e.key === 'Enter') {
          e.preventDefault();
          const d = filtered[highlightIndex];
          if (d) {
            onSelect({ id: d.id, title: d.title });
          }
          return true;
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          onClose();
          return true;
        }
        return false;
      },
    }));

    return (
      <>
        {/* Doc chips */}
        {(currentPageDoc || selectedDocs.length > 0) && (
          <div className="flex flex-wrap gap-1 mb-1">
            {currentPageDoc && (
              <span
                className={cn(
                  'inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs',
                  'bg-primary/15 text-primary'
                )}
              >
                <FileText className="h-3 w-3" />
                <span className="max-w-[120px] truncate">{currentPageDoc.title || currentPageDoc.id}</span>
                <span className="opacity-60 text-[10px]">(current)</span>
              </span>
            )}
            {selectedDocs.map((doc) => (
              <span
                key={doc.id}
                className={cn(
                  'inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs',
                  'bg-blue-500/15 text-blue-600 dark:text-blue-400'
                )}
              >
                <FileText className="h-3 w-3" />
                <span className="max-w-[120px] truncate">{doc.title || doc.id}</span>
                <button
                  type="button"
                  onClick={() => onRemove(doc.id)}
                  className="hover:text-destructive"
                  disabled={disabled}
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        )}

        {/* Dropdown */}
        {isOpen && (
          <div
            ref={dropdownRef}
            className={cn(
              'absolute bottom-full left-0 mb-1 z-50',
              'w-72 max-h-64 overflow-hidden',
              'bg-popover border border-border rounded-md shadow-lg',
              'flex flex-col'
            )}
          >
            <div className="p-2 border-b">
              <div className="relative">
                <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
                <div
                  className={cn(
                    'w-full pl-7 pr-2 py-1.5 text-sm',
                    'text-muted-foreground'
                  )}
                >
                  {filterQuery ? `@${filterQuery}` : 'Type to filter docs...'}
                </div>
              </div>
            </div>

            <div className="overflow-y-auto flex-1 max-h-48">
              {filtered.length === 0 && (
                <div className="px-3 py-2 text-sm text-muted-foreground">
                  {filterQuery ? 'No docs found' : 'No docs available'}
                </div>
              )}
              {filtered.map((doc, idx) => (
                <button
                  key={doc.id}
                  type="button"
                  onClick={() => onSelect({ id: doc.id, title: doc.title })}
                  className={cn(
                    'w-full px-3 py-1.5 text-left text-sm',
                    'hover:bg-accent',
                    idx === highlightIndex && 'bg-accent'
                  )}
                >
                  <div className="flex items-center gap-2">
                    <FileText className="h-3.5 w-3.5 text-blue-500 shrink-0" />
                    <div className="min-w-0">
                      <div className="font-medium truncate">{doc.title || doc.id}</div>
                      {doc.description && (
                        <div className="text-xs text-muted-foreground truncate">
                          {doc.description}
                        </div>
                      )}
                      {doc.title && doc.title !== doc.id && (
                        <div className="text-xs text-muted-foreground truncate font-mono">
                          {doc.id}
                        </div>
                      )}
                    </div>
                  </div>
                </button>
              ))}
            </div>
          </div>
        )}
      </>
    );
  }
);
