// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, {
  useState,
  useEffect,
  useRef,
  useCallback,
  useContext,
  useMemo,
} from 'react';
import { useQuery } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import { Badge } from '@/components/ui/badge';
import { whenEnabled } from '@/hooks/queryUtils';
import {
  Search,
  ChevronDown,
  X,
  AlertTriangle,
  Tags as LabelsIcon,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  withoutWorkspaceLabels,
  workspaceSelectionKey,
  workspaceSelectionQuery,
} from '@/lib/workspace';
import type { components } from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type DAGFile = components['schemas']['DAGFile'];

interface Props {
  selectedTemplate: string;
  selectedWorkspace: string;
  onSelect: (fileName: string) => void;
  onOpenChange?: (isOpen: boolean) => void;
}

export function TemplateSelector({
  selectedTemplate,
  onSelect,
  onOpenChange,
}: Props): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceQuery = useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
  const workspaceKey = workspaceSelectionKey(appBarContext.workspaceSelection);

  const [isOpen, setIsOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState('');
  const [debouncedTerm, setDebouncedTerm] = useState('');
  const [selectedLabels, setSelectedLabels] = useState<string[]>([]);
  const [isLabelFilterOpen, setIsLabelFilterOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const [selectedDag, setSelectedDag] = useState<DAGFile | null>(null);

  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Debounce search term
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedTerm(searchTerm), 300);
    return () => clearTimeout(timer);
  }, [searchTerm]);

  useEffect(() => {
    onOpenChange?.(isOpen);
  }, [isOpen, onOpenChange]);

  // Keep labels fully lazy. We only request them when the user explicitly opens
  // the label filter UI inside the selector.
  const { data: labelsData } = useQuery(
    '/dags/labels',
    whenEnabled(isOpen && isLabelFilterOpen, {
      params: { query: { remoteNode, ...workspaceQuery } },
    })
  );
  const availableLabels = useMemo(
    () => withoutWorkspaceLabels(labelsData?.labels ?? []),
    [labelsData?.labels]
  );
  // The DAG list only stays live while the selector dropdown is open. The
  // closed trigger uses locally cached selection metadata instead.
  const { data, isLoading } = useQuery(
    '/dags',
    whenEnabled(isOpen, {
      params: {
        query: {
          remoteNode,
          perPage: 50,
          ...(debouncedTerm ? { name: debouncedTerm } : {}),
          ...(selectedLabels.length > 0
            ? { labels: selectedLabels.join(',') }
            : {}),
          ...workspaceQuery,
        },
      },
    })
  );
  const dags = useMemo(() => data?.dags ?? [], [data?.dags]);

  const resetFilters = useCallback(() => {
    setSearchTerm('');
    setDebouncedTerm('');
    setSelectedLabels([]);
    setHighlightedIndex(-1);
    setIsLabelFilterOpen(false);
  }, []);

  // Cache selected DAG for trigger display
  useEffect(() => {
    if (!selectedTemplate) {
      setSelectedDag(null);
      return;
    }
    const found = dags.find((d) => d.fileName === selectedTemplate);
    if (found) {
      setSelectedDag(found);
    }
  }, [dags, selectedTemplate]);

  // Group DAGs by group field
  const groupedDags = useMemo(() => {
    const groups = new Map<string, DAGFile[]>();
    for (const dag of dags) {
      const group = dag.dag.group || '';
      const list = groups.get(group) || [];
      list.push(dag);
      groups.set(group, list);
    }
    // Sort groups alphabetically, ungrouped last
    const sorted = [...groups.entries()].sort((a, b) => {
      if (a[0] === '' && b[0] === '') return 0;
      if (a[0] === '') return 1;
      if (b[0] === '') return -1;
      return a[0].localeCompare(b[0]);
    });
    return sorted;
  }, [dags]);

  // Flattened list for keyboard navigation
  const flatList = useMemo(() => {
    const items: DAGFile[] = [];
    for (const [, dagList] of groupedDags) {
      items.push(...dagList);
    }
    return items;
  }, [groupedDags]);

  // Reset highlight on filter change
  useEffect(() => {
    setHighlightedIndex(-1);
  }, [debouncedTerm, selectedLabels]);

  // Click outside to close
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        resetFilters();
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [resetFilters]);

  useEffect(() => {
    setIsOpen(false);
    setIsLabelFilterOpen(false);
    setSearchTerm('');
    setDebouncedTerm('');
    setSelectedLabels([]);
    setHighlightedIndex(-1);
    setSelectedDag(null);
  }, [remoteNode, workspaceKey]);

  // Scroll highlighted item into view
  useEffect(() => {
    if (highlightedIndex < 0 || !listRef.current) return;
    const items = listRef.current.querySelectorAll('[data-dag-item]');
    items[highlightedIndex]?.scrollIntoView({ block: 'nearest' });
  }, [highlightedIndex]);

  const handleSelect = useCallback(
    (fileName: string) => {
      const dag = dags.find((d) => d.fileName === fileName);
      if (dag) setSelectedDag(dag);
      setIsOpen(false);
      resetFilters();
      onSelect(fileName);
    },
    [dags, onSelect, resetFilters]
  );

  const toggleLabel = useCallback((label: string) => {
    setSelectedLabels((prev) =>
      prev.includes(label) ? prev.filter((t) => t !== label) : [...prev, label]
    );
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setHighlightedIndex((prev) =>
          prev < flatList.length - 1 ? prev + 1 : 0
        );
        break;
      case 'ArrowUp':
        e.preventDefault();
        setHighlightedIndex((prev) =>
          prev > 0 ? prev - 1 : flatList.length - 1
        );
        break;
      case 'Enter':
        e.preventDefault();
        if (highlightedIndex >= 0 && highlightedIndex < flatList.length) {
          const dag = flatList[highlightedIndex];
          if (dag) handleSelect(dag.fileName);
        }
        break;
      case 'Escape':
        setIsOpen(false);
        resetFilters();
        break;
    }
  };

  const handleOpen = () => {
    resetFilters();
    setIsOpen(true);
    setTimeout(() => inputRef.current?.focus(), 0);
  };

  const selectedDagName = selectedDag?.dag.name;
  const hasGroups = groupedDags.some(([group]) => group !== '');

  return (
    <div ref={containerRef} className="relative">
      {/* Trigger button */}
      <button
        type="button"
        onClick={handleOpen}
        className={cn(
          'flex items-center justify-between gap-2 h-7 px-3 text-xs rounded-md border border-border bg-background whitespace-nowrap transition-colors outline-none',
          'hover:border-border-strong cursor-pointer w-48',
          isOpen && 'border-ring'
        )}
      >
        {selectedTemplate && selectedDagName ? (
          <>
            <span className="truncate flex-1 text-left">{selectedDagName}</span>
            <I18nProps>
              <span
                role="button"
                tabIndex={0}
                onClick={(e) => {
                  e.stopPropagation();
                  onSelect('');
                  setSelectedDag(null);
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.stopPropagation();
                    e.preventDefault();
                    onSelect('');
                    setSelectedDag(null);
                  }
                }}
                aria-label="Clear selection"
                className="p-0.5 rounded hover:bg-muted-foreground/20 hover:text-destructive cursor-pointer"
              >
                <X className="h-3 w-3" />
              </span>
            </I18nProps>
          </>
        ) : (
          <>
            <Search className="h-3 w-3 text-muted-foreground shrink-0" />
            <span className="text-muted-foreground truncate flex-1 text-left">
              <I18nText text={'Select template...'} />
            </span>
          </>
        )}
        <ChevronDown
          className={cn(
            'h-3 w-3 text-muted-foreground shrink-0 transition-transform',
            isOpen && 'rotate-180'
          )}
        />
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div className="absolute z-50 mt-1 w-80 max-h-[min(70vh,600px)] rounded-md border border-border bg-popover shadow-md flex flex-col">
          {/* Search input */}
          <div className="flex items-center gap-2 px-3 py-1.5 border-b border-border">
            <Search className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            <I18nProps>
              <input
                ref={inputRef}
                type="text"
                value={searchTerm}
                onChange={(e) => setSearchTerm(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="Search DAGs..."
                className="flex-1 bg-transparent border-none outline-none text-xs placeholder:text-muted-foreground"
              />
            </I18nProps>
            {isLoading && debouncedTerm && (
              <span className="text-[10px] text-muted-foreground">
                <I18nText text={'Searching...'} />
              </span>
            )}
            <button
              type="button"
              onClick={() => setIsLabelFilterOpen((prev) => !prev)}
              className={cn(
                'inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-[10px] text-muted-foreground hover:text-foreground',
                isLabelFilterOpen && 'border-ring text-foreground'
              )}
            >
              <LabelsIcon className="h-3 w-3" />
              <I18nText text={'Labels'} />
            </button>
          </div>

          {/* Label filter row */}
          {isLabelFilterOpen && (
            <div className="flex flex-wrap gap-1 px-3 pt-2 pb-2.5 border-b border-border max-h-[200px] overflow-y-auto shrink-0">
              {availableLabels.length === 0 ? (
                <span className="text-[10px] text-muted-foreground">
                  {labelsData ? (
                    <I18nText text={'No labels found'} />
                  ) : (
                    <I18nText text={'Loading labels...'} />
                  )}
                </span>
              ) : (
                availableLabels.map((label) => {
                  const isActive = selectedLabels.includes(label);
                  return (
                    <button
                      key={label}
                      type="button"
                      onClick={() => toggleLabel(label)}
                      className="cursor-pointer"
                    >
                      <Badge
                        variant={isActive ? 'primary' : 'default'}
                        className="text-[10px] px-1.5 cursor-pointer"
                      >
                        {isActive && <X className="h-2 w-2 mr-0.5" />}
                        {label}
                      </Badge>
                    </button>
                  );
                })
              )}
            </div>
          )}

          {/* DAG list */}
          <div
            ref={listRef}
            className="overflow-y-auto flex-1 min-h-0"
            onKeyDown={handleKeyDown}
          >
            {flatList.length === 0 ? (
              <div className="px-3 py-4 text-xs text-muted-foreground text-center">
                <I18nText text={'No DAGs found'} />
              </div>
            ) : (
              groupedDags.map(([group, dagList]) => (
                <div key={group}>
                  {/* Group header */}
                  {hasGroups && (
                    <div className="px-3 py-1 text-[10px] uppercase tracking-wider text-muted-foreground font-medium bg-popover border-b border-border sticky top-0 z-10">
                      {group || <I18nText text={'(ungrouped)'} />}
                    </div>
                  )}
                  {/* DAG items */}
                  {dagList.map((dag) => {
                    const idx = flatList.indexOf(dag);
                    const params = dag.dag.params;
                    const labels = dag.dag.labels ?? dag.dag.tags;
                    const description = dag.dag.description;
                    return (
                      <div
                        key={dag.fileName}
                        data-dag-item
                        className={cn(
                          'px-3 py-1.5 cursor-pointer transition-colors',
                          idx === highlightedIndex
                            ? 'bg-muted'
                            : 'hover:bg-muted'
                        )}
                        onClick={() => handleSelect(dag.fileName)}
                        onMouseEnter={() => setHighlightedIndex(idx)}
                      >
                        {/* Name + error/param indicators */}
                        <div className="flex items-center justify-between gap-2">
                          <span
                            className={cn(
                              'font-medium text-xs truncate',
                              dag.errors?.length > 0 && 'text-destructive'
                            )}
                          >
                            {dag.dag.name}
                          </span>
                          <div className="flex items-center gap-1 shrink-0">
                            {dag.errors?.length > 0 && (
                              <AlertTriangle className="h-3 w-3 text-destructive" />
                            )}
                            {params && params.length > 0 && (
                              <span className="text-[10px] text-muted-foreground">
                                {params.length}p
                              </span>
                            )}
                          </div>
                        </div>
                        {/* Description */}
                        {description && (
                          <div className="text-[11px] text-muted-foreground truncate">
                            {description}
                          </div>
                        )}
                        {/* Labels */}
                        {labels && labels.length > 0 && (
                          <div className="flex items-center gap-1 mt-0.5">
                            {labels.slice(0, 3).map((label) => (
                              <button
                                key={label}
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  toggleLabel(label);
                                }}
                              >
                                <Badge
                                  variant={
                                    selectedLabels.includes(label)
                                      ? 'primary'
                                      : 'default'
                                  }
                                  className="text-[10px] px-1 py-0 h-3 cursor-pointer"
                                >
                                  {label}
                                </Badge>
                              </button>
                            ))}
                            {labels.length > 3 && (
                              <span className="text-[10px] text-muted-foreground">
                                +{labels.length - 3}
                              </span>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}
