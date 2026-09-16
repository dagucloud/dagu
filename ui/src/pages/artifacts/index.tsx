// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import dayjs from '@/lib/dayjs';
import {
  AlertCircle,
  File,
  FileCode,
  FileImage,
  FileText,
  Folder,
  FolderOpen,
  Link as LinkIcon,
  RefreshCw,
} from 'lucide-react';
import React from 'react';
import { Link } from 'react-router-dom';
import type {
  ArtifactListItem,
  ArtifactListQuery,
} from '@/features/artifacts/hooks/artifactListPagination';
import { usePaginatedArtifacts } from '@/features/artifacts/hooks/artifactListPagination';
import { ArtifactFilePreview } from '@/features/dags/components/artifacts/ArtifactFilePreview';
import { Button } from '@/components/ui/button';
import { DateRangePicker } from '@/components/ui/date-range-picker';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import Title from '@/components/ui/title';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useConfig } from '../../contexts/ConfigContext';
import { workspaceSelectionQuery } from '../../lib/workspace';
import { cn } from '@/lib/utils';

const DEFAULT_PRESET = 'all';
const ARTIFACT_LIST_LIMIT = 100;

function computePresetDates(
  preset: string,
  tzOffsetInSec: number | undefined
): { from: string; to?: string } {
  const now = dayjs();
  const startOfDay =
    tzOffsetInSec !== undefined
      ? now.utcOffset(tzOffsetInSec / 60).startOf('day')
      : now.startOf('day');

  switch (preset) {
    case 'today':
      return { from: startOfDay.format('YYYY-MM-DDTHH:mm') };
    case 'yesterday':
      return {
        from: startOfDay.subtract(1, 'day').format('YYYY-MM-DDTHH:mm'),
        to: startOfDay.format('YYYY-MM-DDTHH:mm'),
      };
    case 'last7days':
      return {
        from: startOfDay.subtract(7, 'day').format('YYYY-MM-DDTHH:mm'),
      };
    case 'last30days':
      return {
        from: startOfDay.subtract(30, 'day').format('YYYY-MM-DDTHH:mm'),
      };
    case 'thisWeek':
      return {
        from: startOfDay.startOf('week').format('YYYY-MM-DDTHH:mm'),
      };
    case 'thisMonth':
      return {
        from: startOfDay.startOf('month').format('YYYY-MM-DDTHH:mm'),
      };
    default:
      return { from: startOfDay.format('YYYY-MM-DDTHH:mm') };
  }
}

function runKey(item: Pick<ArtifactListItem, 'name' | 'dagRunId'>): string {
  return `${item.name}\u0000${item.dagRunId}`;
}

// Synthetic paths keep tree node identities unique across runs while the
// real relative path stays available for preview and download requests.
function runTreeRoot(item: Pick<ArtifactListItem, 'name' | 'dagRunId'>): string {
  return `@run:${runKey(item)}`;
}

type FileTreeNode = {
  name: string;
  path: string;
  type: 'directory' | 'file';
  size?: number;
  children?: FileTreeNode[];
};

function filesToTreeNodes(
  files: ArtifactListItem['files'],
  prefix: string
): FileTreeNode[] {
  const root: FileTreeNode[] = [];
  for (const file of files) {
    const parts = file.path.split('/');
    let level: FileTreeNode[] = root;
    let acc = prefix;
    for (let i = 0; i < parts.length - 1; i++) {
      const part = parts[i]!;
      acc = `${acc}/${part}`;
      const existing = level.find(
        (node) => node.type === 'directory' && node.path === acc
      );
      if (existing) {
        level = existing.children ?? [];
        continue;
      }
      const dir: FileTreeNode = {
        name: part,
        path: acc,
        type: 'directory',
        children: [],
      };
      level.push(dir);
      level = dir.children ?? [];
    }
    const leafName = parts[parts.length - 1] ?? file.path;
    level.push({
      name: leafName,
      path: `${acc}/${leafName}`,
      type: 'file',
      size: file.size,
    });
  }
  return root;
}

function collectDirectoryPaths(nodes: FileTreeNode[]): string[] {
  const paths: string[] = [];
  for (const node of nodes) {
    if (node.type === 'directory') {
      paths.push(node.path);
      if (node.children) {
        paths.push(...collectDirectoryPaths(node.children));
      }
    }
  }
  return paths;
}

function Artifacts() {
  const { ts } = useI18n();
  const appBarContext = React.useContext(AppBarContext);
  const config = useConfig();
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(appBarContext.workspaceSelection),
    [appBarContext.workspaceSelection]
  );
    const [searchText, setSearchText] = React.useState('');
  const [apiSearchText, setApiSearchText] = React.useState('');
  const [fileNameText, setFileNameText] = React.useState('');
  const [apiFileNameText, setApiFileNameText] = React.useState('');
  const [dateRangeMode, setDateRangeMode] = React.useState<
    'preset' | 'custom'
  >('preset');
  const [datePreset, setDatePreset] = React.useState(DEFAULT_PRESET);
  const [fromDate, setFromDate] = React.useState<string | undefined>();
  const [toDate, setToDate] = React.useState<string | undefined>();
  const [apiFromDate, setApiFromDate] = React.useState<string | undefined>();
  const [apiToDate, setApiToDate] = React.useState<string | undefined>();
  const [selected, setSelected] = React.useState<{
    name: string;
    dagRunId: string;
    path: string;
  } | null>(null);
  const [expandedPaths, setExpandedPaths] = React.useState<Set<string>>(
    new Set()
  );
  const loadMoreSentinelRef = React.useRef<HTMLDivElement>(null);
  const autoLoadPendingRef = React.useRef(false);

  // Convert datetime to unix timestamp (seconds) for API calls
  const formatDateForApi = (
    dateString: string | undefined
  ): number | undefined => {
    if (!dateString) return undefined;

    // Add seconds if they're missing (datetime-local inputs only have HH:mm)
    const dateWithSeconds =
      dateString.split(':').length < 3 ? `${dateString}:00` : dateString;

    // Interpret the wall clock in the configured timezone, never in the
    // browser's, then convert to the Unix timestamp.
    if (config.tzOffsetInSec !== undefined) {
      return dayjs(dateWithSeconds)
        .utcOffset(config.tzOffsetInSec / 60, true)
        .unix();
    } else {
      return dayjs(dateWithSeconds).unix();
    }
  };

  React.useEffect(() => {
    appBarContext.setTitle('Artifacts');
  }, [appBarContext]);

  const handleSearch = () => {
    setApiSearchText(searchText.trim());
    setApiFileNameText(fileNameText.trim());
    setApiFromDate(fromDate);
    setApiToDate(toDate);
  };

  const handleDatePresetChange = (preset: string) => {
    setDatePreset(preset);
    if (preset === 'all') {
      setFromDate(undefined);
      setToDate(undefined);
      setApiFromDate(undefined);
      setApiToDate(undefined);
      return;
    }
    const dates = computePresetDates(preset, config.tzOffsetInSec);
    setFromDate(dates.from);
    setToDate(dates.to);
    setApiFromDate(dates.from);
    setApiToDate(dates.to);
  };

  const handleInputKeyPress = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      handleSearch();
    }
  };

  const artifactQuery = React.useMemo<ArtifactListQuery>(
    () => ({
      remoteNode: appBarContext.selectedRemoteNode || 'local',
      name: apiSearchText || undefined,
      fileName: apiFileNameText || undefined,
      fromDate: formatDateForApi(apiFromDate),
      toDate: formatDateForApi(apiToDate),
      limit: ARTIFACT_LIST_LIMIT,
      ...workspaceQuery,
    }),
    [
      apiFileNameText,
      apiFromDate,
      apiSearchText,
      apiToDate,
      appBarContext.selectedRemoteNode,
      formatDateForApi,
      workspaceQuery,
    ]
  );

  const {
    items,
    error,
    isInitialLoading,
    isLoadingMore,
    loadMoreError,
    hasMore,
    refresh: refreshArtifacts,
    loadMore: handleLoadMore,
  } = usePaginatedArtifacts({
    query: artifactQuery,
  });

  // Keep the selection and the visible tree in sync with the loaded list:
  // auto-select the first file of the newest run with files, and expand the
  // run (and its subdirectories) the selection lives in.
  React.useEffect(() => {
    const selectedIsLoaded =
      selected !== null &&
      items.some(
        (item) =>
          item.name === selected.name &&
          item.dagRunId === selected.dagRunId &&
          item.files.some((file) => file.path === selected.path)
      );

    if (!selectedIsLoaded) {
      const firstRun = items.find((item) => item.files.length > 0);
      setSelected(
        firstRun && firstRun.files.length > 0
          ? {
              name: firstRun.name,
              dagRunId: firstRun.dagRunId,
              path: firstRun.files[0]!.path,
            }
          : null
      );
      return;
    }

    const run = items.find(
      (item) =>
        item.name === selected!.name && item.dagRunId === selected!.dagRunId
    );
    if (!run) {
      return;
    }
    const dirs = [
      runTreeRoot(run),
      ...collectDirectoryPaths(filesToTreeNodes(run.files, runTreeRoot(run))),
    ];
    setExpandedPaths((previous) => {
      const next = new Set(previous);
      let changed = false;
      for (const dir of dirs) {
        if (!next.has(dir)) {
          next.add(dir);
          changed = true;
        }
      }
      return changed ? next : previous;
    });
  }, [items, selected]);

  const canAutoLoadMore = typeof IntersectionObserver !== 'undefined';
  useAutoLoadMore(
    loadMoreSentinelRef,
    canAutoLoadMore && hasMore && !isLoadingMore && !loadMoreError,
    () => {
      if (autoLoadPendingRef.current) {
        return;
      }
      autoLoadPendingRef.current = true;
      void handleLoadMore();
    }
  );
  React.useEffect(() => {
    if (!isLoadingMore) {
      autoLoadPendingRef.current = false;
    }
  }, [isLoadingMore]);

  const fileCount = items.reduce(
    (total, item) => total + item.files.length,
    0
  );

  const formatTimezoneOffset = (): string => {
    if (config.tzOffsetInSec === undefined) return '';

    // Convert seconds to hours and minutes
    const offsetInMinutes = config.tzOffsetInSec / 60;
    const hours = Math.floor(Math.abs(offsetInMinutes) / 60);
    const minutes = Math.abs(offsetInMinutes) % 60;

    // Format with sign and padding
    const sign = offsetInMinutes >= 0 ? '+' : '-';
    const formattedHours = hours.toString().padStart(2, '0');
    const formattedMinutes = minutes.toString().padStart(2, '0');

    return `(${sign}${formattedHours}:${formattedMinutes})`;
  };

  const formatTimestamp = (timestamp: string | undefined): string => {
    if (!timestamp) {
      return '-';
    }
    const value = dayjs(timestamp);
    const configuredTime =
      config.tzOffsetInSec === undefined
        ? value
        : value.utcOffset(config.tzOffsetInSec / 60);
    return configuredTime.format('YYYY-MM-DD HH:mm:ss');
  };

  const tzLabel = formatTimezoneOffset();
  const selectedNodeSyntheticPath =
    selected !== null
      ? `${runTreeRoot(selected)}/${selected.path}`
      : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="mb-2 flex min-w-0 items-center gap-3">
        <Title>
          <I18nText text={'Artifacts'} />
        </Title>
        <I18nProps>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => void refreshArtifacts()}
            title="Reload artifacts"
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
        </I18nProps>
      </div>
      <div className="mb-3 space-y-3 rounded-lg border border-border bg-card/50 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <I18nProps>
            <Input
              placeholder="Filter by DAG name..."
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              onKeyDown={handleInputKeyPress}
              className="w-[200px]"
            />
          </I18nProps>
          <I18nProps>
            <Input
              placeholder="Filter by file name..."
              value={fileNameText}
              onChange={(e) => setFileNameText(e.target.value)}
              onKeyDown={handleInputKeyPress}
              className="w-[200px]"
            />
          </I18nProps>
          <I18nProps>
            <ToggleGroup aria-label="Date range mode" className="h-9 p-0.5">
              <I18nProps>
                <ToggleButton
                  value="preset"
                  groupValue={dateRangeMode}
                  onClick={() => {
                    setDateRangeMode('preset');
                    handleDatePresetChange(datePreset);
                  }}
                  position="first"
                  className="h-8 px-3"
                >
                  <I18nText text={'Quick'} />
                </ToggleButton>
              </I18nProps>
              <I18nProps>
                <ToggleButton
                  value="custom"
                  groupValue={dateRangeMode}
                  onClick={() => {
                    setDateRangeMode('custom');
                    setFromDate(apiFromDate);
                    setToDate(apiToDate);
                  }}
                  position="last"
                  className="h-8 px-3"
                >
                  <I18nText text={'Custom'} />
                </ToggleButton>
              </I18nProps>
            </ToggleGroup>
          </I18nProps>
          {dateRangeMode === 'preset' ? (
            <Select value={datePreset} onValueChange={handleDatePresetChange}>
              <I18nProps>
                <SelectTrigger aria-label="Date preset" className="w-[180px]">
                  <I18nProps>
                    <SelectValue placeholder="Select period" />
                  </I18nProps>
                </SelectTrigger>
              </I18nProps>
              <SelectContent>
                <SelectItem value="all">
                  <I18nText text={'All time'} />
                </SelectItem>
                <SelectItem value="today">
                  <I18nText text={'Today'} />
                </SelectItem>
                <SelectItem value="yesterday">
                  <I18nText text={'Yesterday'} />
                </SelectItem>
                <SelectItem value="last7days">
                  <I18nText text={'Last 7 days'} />
                </SelectItem>
                <SelectItem value="last30days">
                  <I18nText text={'Last 30 days'} />
                </SelectItem>
                <SelectItem value="thisWeek">
                  <I18nText text={'This week'} />
                </SelectItem>
                <SelectItem value="thisMonth">
                  <I18nText text={'This month'} />
                </SelectItem>
              </SelectContent>
            </Select>
          ) : (
            <DateRangePicker
              fromDate={fromDate}
              toDate={toDate}
              onFromDateChange={setFromDate}
              onToDateChange={setToDate}
              onEnterPress={() => handleSearch()}
              fromLabel={`From ${tzLabel}`}
              toLabel={`To ${tzLabel}`}
              className="w-full md:w-auto"
            />
          )}
        </div>
      </div>

      {items.length === 0 ? (
        isInitialLoading ? (
          <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
            <I18nText text={'Loading artifacts...'} />
          </div>
        ) : error ? (
          <div className="flex items-start gap-2 rounded-md bg-destructive/5 px-3 py-3 text-sm text-destructive">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>
              {error instanceof Error
                ? error.message
                : 'Failed to load artifacts'}
            </span>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center rounded-md border border-dashed border-border bg-muted/20 px-4 py-10 text-sm text-muted-foreground">
            <p className="font-medium text-foreground">
              <I18nText text={'No artifacts found'} />
            </p>
            <p className="mt-1 max-w-md text-center">
              <I18nText
                text={
                  'No DAG-runs in the selected time range produced artifact files. Adjust the date range or filters.'
                }
              />
            </p>
          </div>
        )
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 xl:grid-cols-[320px_minmax(0,1fr)] xl:grid-rows-1">
          <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border border-border bg-surface">
            <div className="flex items-center justify-between border-b border-border px-3 py-2">
              <div>
                <p className="text-sm font-medium">
                  <I18nText text={'Artifacts'} />
                </p>
                <p className="text-xs text-muted-foreground">
                  {ts('{count} files', { count: fileCount })}
                  {items.some((item) => item.filesTruncated) ? (
                    <span className="ml-1">
                      <I18nText text={'· selective list, use the run to view all files'} />
                    </span>
                  ) : null}
                </p>
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-auto p-2">
              <div className="space-y-0.5">
                {items.map((item) => {
                  const root = runTreeRoot(item);
                  const nodes = filesToTreeNodes(item.files, root);
                  const isOpen = expandedPaths.has(root);
                  const Icon = isOpen ? FolderOpen : Folder;
                  return (
                    <div key={runKey(item)}>
                      <div className="group/run flex items-center gap-1 rounded-md transition-colors hover:bg-muted">
                        <button
                          type="button"
                          onClick={() => {
                            setExpandedPaths((previous) => {
                              const next = new Set(previous);
                              if (next.has(root)) {
                                next.delete(root);
                                return next;
                              }
                              next.add(root);
                              for (const dir of collectDirectoryPaths(nodes)) {
                                next.add(dir);
                              }
                              return next;
                            });
                          }}
                          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm text-foreground transition-colors"
                        >
                          <Icon className="h-4 w-4 shrink-0" />
                          <span className="min-w-0 flex-1 truncate font-medium">
                            {item.name}
                          </span>
                          <span className="shrink-0 text-[11px] text-muted-foreground">
                            {item.files.length > 0
                              ? ts('{count} files', {
                                  count: item.files.length,
                                })
                              : '—'}
                          </span>
                        </button>
                        <Link
                          to={`/dag-runs/${item.name}/${item.dagRunId}`}
                          className="mr-1 shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                          title="Open DAG run"
                          aria-label={`Open DAG run ${item.name}`}
                          onClick={(event) => event.stopPropagation()}
                        >
                          <LinkIcon className="h-4 w-4" />
                        </Link>
                      </div>
                      <span className="block truncate pl-8 text-[11px] text-muted-foreground">
                        {formatTimestamp(item.createdAt)} · {item.dagRunId}
                      </span>
                      {isOpen && nodes.length > 0 && (
                        <div className="space-y-0.5">
                          {nodes.map((node) => (
                            <TreeNode
                              key={node.path}
                              node={node}
                              depth={1}
                              expandedPaths={expandedPaths}
                              selectedPath={selectedNodeSyntheticPath}
                              onToggleDir={(path) => {
                                setExpandedPaths((previous) => {
                                  const next = new Set(previous);
                                  if (next.has(path)) {
                                    next.delete(path);
                                  } else {
                                    next.add(path);
                                  }
                                  return next;
                                });
                              }}
                              onSelectFile={(path) => {
                                const realPath = path.slice(
                                  root.length + 1
                                );
                                setSelected({
                                  name: item.name,
                                  dagRunId: item.dagRunId,
                                  path: realPath,
                                });
                              }}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
              <div ref={loadMoreSentinelRef} className="h-4 w-full" />
            </div>
            <div className="p-2">
              {loadMoreError && (
                <div className="text-sm text-error">{loadMoreError}</div>
              )}
              {isLoadingMore ? (
                <div className="text-sm text-muted-foreground">
                  <I18nText text={'Loading more artifacts...'} />
                </div>
              ) : hasMore ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-full"
                  onClick={() => void handleLoadMore()}
                >
                  {loadMoreError ? (
                    <I18nText text={'Retry loading more'} />
                  ) : (
                    <I18nText text={'Load more'} />
                  )}
                </Button>
              ) : (
                <div className="text-sm text-muted-foreground">
                  <I18nText text={'All artifact files are displayed.'} />
                </div>
              )}
            </div>
          </div>

          <ArtifactFilePreview
            dagRunName={selected?.name ?? ''}
            dagRunId={selected?.dagRunId ?? ''}
            path={selected?.path ?? null}
            remoteNode={appBarContext.selectedRemoteNode || 'local'}
            fillHeight
          />
        </div>
      )}
    </div>
  );
}

function TreeNode({
  node,
  depth,
  expandedPaths,
  selectedPath,
  onToggleDir,
  onSelectFile,
}: {
  node: FileTreeNode;
  depth: number;
  expandedPaths: Set<string>;
  selectedPath: string | null;
  onToggleDir: (path: string) => void;
  onSelectFile: (path: string) => void;
}) {
  const isDir = node.type === 'directory';
  const isOpen = isDir && expandedPaths.has(node.path);
  const isSelected = !isDir && selectedPath === node.path;

  const Icon = isDir
    ? isOpen
      ? FolderOpen
      : Folder
    : node.path.match(/\.(md|markdown|mdown|mkd)$/i)
      ? FileText
      : node.path.match(/\.(html?|xhtml)$/i)
        ? FileCode
        : node.path.match(/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/i)
          ? FileImage
          : File;

  return (
    <div>
      <button
        type="button"
        onClick={() => {
          if (isDir) {
            onToggleDir(node.path);
            return;
          }
          onSelectFile(node.path);
        }}
        className={cn(
          'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors',
          isSelected
            ? 'bg-primary/10 text-primary'
            : 'text-foreground hover:bg-muted'
        )}
        style={{ paddingLeft: `${depth * 14 + 8}px` }}
      >
        <Icon className="h-4 w-4 shrink-0" />
        <span className="min-w-0 flex-1 truncate">{node.name}</span>
        {!isDir && node.size != null && (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {Intl.NumberFormat().format(node.size)}
          </span>
        )}
      </button>
      {isDir && isOpen && node.children && node.children.length > 0 && (
        <div className="space-y-0.5">
          {node.children.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              depth={depth + 1}
              expandedPaths={expandedPaths}
              selectedPath={selectedPath}
              onToggleDir={onToggleDir}
              onSelectFile={onSelectFile}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function useAutoLoadMore(
  sentinelRef: React.RefObject<HTMLDivElement | null>,
  enabled: boolean,
  onLoadMore: () => void
) {
  React.useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !enabled || typeof IntersectionObserver === 'undefined') {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          onLoadMore();
        }
      },
      { threshold: 0.1 }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [enabled, onLoadMore, sentinelRef]);
}

export default Artifacts;