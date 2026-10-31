// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ChevronRight,
  FileSearch,
  Monitor,
  Moon,
  Network,
  Search,
  Sun,
} from 'lucide-react';
import * as React from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  type ThemePreference,
  useUserPreferences,
} from '@/contexts/UserPreference';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { useNavigationSections } from '@/hooks/useNavigationSections';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { workspaceSelectionQuery } from '@/lib/workspace';
import type { RegisteredPaletteGroup } from './CommandPaletteProvider';

const WORKFLOW_RESULT_LIMIT = 8;
const SEARCH_DEBOUNCE_MS = 150;

type CommandGroup = 'workflow' | 'workflows' | 'pages' | 'actions';

type CommandItem = {
  id: string;
  group: CommandGroup;
  /** Label of a registered group ('workflow'). */
  groupLabel?: string;
  label: string;
  hint?: string;
  shortcut?: string;
  icon: React.ReactNode;
  disabled?: boolean;
  run: () => void;
};

const NO_GROUPS: RegisteredPaletteGroup[] = [];

function matches(
  item: { label: string; hint?: string; keywords?: string[] },
  query: string
) {
  if (!query) {
    return true;
  }
  const needle = query.toLowerCase();
  return (
    item.label.toLowerCase().includes(needle) ||
    (item.hint?.toLowerCase().includes(needle) ?? false) ||
    (item.keywords?.some((word) => word.toLowerCase().includes(needle)) ??
      false)
  );
}

/**
 * Searchable list of workflows, pages, and actions. The commands of the
 * registered groups come first.
 */
export default function CommandPaletteDialog({
  open,
  onOpenChange,
  groups = NO_GROUPS,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  groups?: RegisteredPaletteGroup[];
}): React.ReactElement {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const appBar = React.useContext(AppBarContext);
  const { preferences, updatePreference } = useUserPreferences();
  const navigation = useNavigationSections();
  const [query, setQuery] = React.useState('');
  const [activeIndex, setActiveIndex] = React.useState(0);
  const listRef = React.useRef<HTMLDivElement>(null);
  const trimmedQuery = query.trim();
  const debouncedQuery = useDebouncedValue(trimmedQuery, SEARCH_DEBOUNCE_MS);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const workspaceQuery = React.useMemo(
    () => workspaceSelectionQuery(appBar.workspaceSelection),
    [appBar.workspaceSelection]
  );

  const { data: workflowData, isLoading: workflowsLoading } = useQuery(
    '/dags',
    whenEnabled(open, {
      params: {
        query: {
          remoteNode,
          perPage: WORKFLOW_RESULT_LIMIT,
          ...(debouncedQuery ? { name: debouncedQuery } : {}),
          ...workspaceQuery,
        },
      },
    })
  );

  const close = React.useCallback(() => onOpenChange(false), [onOpenChange]);

  const items = React.useMemo(() => {
    const go = (to: string) => () => {
      close();
      navigate(to);
    };

    // available() reads the page each time the palette opens. The ids carry
    // the registration, apart from the built-in ids.
    const registered: CommandItem[] = open
      ? groups.flatMap(({ group, commands }, registration) =>
          commands
            .filter(
              (command) =>
                (command.available?.() ?? true) &&
                matches(command, trimmedQuery)
            )
            .map((command) => ({
              id: `registered-${registration}:${command.id}`,
              group: group.id,
              groupLabel: group.label,
              label: command.label,
              shortcut: command.shortcut,
              icon: command.icon ?? <ChevronRight className="size-4" />,
              disabled: command.disabled,
              run: () => {
                close();
                command.run();
              },
            }))
        )
      : [];

    const workflows: CommandItem[] = (workflowData?.dags ?? []).map((dag) => ({
      id: `workflow:${dag.fileName}`,
      group: 'workflows',
      label: dag.dag.name,
      hint: dag.dag.description || undefined,
      icon: <Network className="size-4" />,
      run: go(buildDAGPageURL({ fileName: dag.fileName, remoteNode })),
    }));

    const pages: CommandItem[] = [
      ...navigation.mainItems.map((link) => ({ link, section: '' })),
      ...navigation.sections.flatMap((section) =>
        section.items.map((link) => ({ link, section: section.label }))
      ),
    ]
      .map(({ link, section }) => ({
        id: `page:${link.to}`,
        group: 'pages' as const,
        label: link.text,
        hint: section || undefined,
        icon: link.icon,
        run: go(link.to),
      }))
      .filter((item) => matches(item, trimmedQuery));

    const themeActions: {
      value: ThemePreference;
      label: string;
      icon: React.ReactNode;
    }[] = [
      {
        value: 'light',
        label: ts('Use light theme'),
        icon: <Sun className="size-4" />,
      },
      {
        value: 'dark',
        label: ts('Use dark theme'),
        icon: <Moon className="size-4" />,
      },
      {
        value: 'system',
        label: ts('Use system theme'),
        icon: <Monitor className="size-4" />,
      },
    ];
    const actions: CommandItem[] = [
      ...(trimmedQuery
        ? [
            {
              id: 'action:search-contents',
              group: 'actions' as const,
              label: ts('Search workflow contents for "{query}"', {
                query: trimmedQuery,
              }),
              icon: <FileSearch className="size-4" />,
              run: go(`/search?q=${encodeURIComponent(trimmedQuery)}`),
            },
          ]
        : []),
      ...themeActions
        .filter(({ value }) => value !== preferences.theme)
        .map(({ value, label, icon }) => ({
          id: `action:theme-${value}`,
          group: 'actions' as const,
          label,
          icon,
          run: () => {
            updatePreference('theme', value);
            close();
          },
        }))
        .filter((item) => matches(item, trimmedQuery)),
    ];

    return [...registered, ...workflows, ...pages, ...actions];
  }, [
    close,
    groups,
    navigate,
    navigation,
    open,
    preferences.theme,
    remoteNode,
    trimmedQuery,
    ts,
    updatePreference,
    workflowData?.dags,
  ]);

  React.useEffect(() => {
    setActiveIndex(0);
  }, [trimmedQuery, items.length]);

  React.useEffect(() => {
    if (!open) {
      setQuery('');
    }
  }, [open]);

  React.useEffect(() => {
    listRef.current
      ?.querySelector<HTMLElement>(`[data-index="${activeIndex}"]`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex]);

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (items.length === 0) {
      return;
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActiveIndex((index) => (index + 1) % items.length);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActiveIndex((index) => (index - 1 + items.length) % items.length);
    } else if (event.key === 'Enter') {
      event.preventDefault();
      const item = items[activeIndex];
      if (item && !item.disabled) {
        item.run();
      }
    }
  };

  const groupLabels: Record<Exclude<CommandGroup, 'workflow'>, string> = {
    workflows: ts('Workflows'),
    pages: ts('Pages'),
    actions: ts('Actions'),
  };
  const activeItem = items[activeIndex];
  const listboxId = 'command-palette-results';

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        hideCloseButton
        className="top-[12vh] max-w-xl translate-y-0 gap-0 overflow-hidden p-0 sm:rounded-xl"
      >
        <DialogTitle className="sr-only">{ts('Command palette')}</DialogTitle>
        <DialogDescription className="sr-only">
          {ts('Search workflows, pages, and actions')}
        </DialogDescription>
        <div className="flex items-center gap-2.5 border-b border-border px-4">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={onKeyDown}
            placeholder={ts('Search workflows, pages, and actions...')}
            role="combobox"
            aria-expanded="true"
            aria-controls={listboxId}
            aria-activedescendant={
              activeItem ? `command-${activeItem.id}` : undefined
            }
            aria-label={ts('Command palette')}
            className="h-12 min-w-0 flex-1 bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
          />
          <kbd className="rounded border border-border px-1.5 py-0.5 font-sans text-[11px] text-muted-foreground">
            Esc
          </kbd>
        </div>
        <div
          ref={listRef}
          id={listboxId}
          role="listbox"
          aria-label={ts('Results')}
          className="max-h-[min(60vh,380px)] overflow-y-auto p-1.5"
        >
          {items.length === 0 ? (
            <p className="px-3 py-8 text-center text-sm text-muted-foreground">
              {workflowsLoading
                ? ts('Searching workflows...')
                : ts('No results for "{query}"', { query: trimmedQuery })}
            </p>
          ) : (
            items.map((item, index) => {
              const startsGroup =
                index === 0 || items[index - 1]?.group !== item.group;
              const isActive = index === activeIndex;
              return (
                <React.Fragment key={item.id}>
                  {startsGroup && (
                    <div
                      role="presentation"
                      className="px-2 pb-1 pt-2 text-xs font-medium text-muted-foreground"
                    >
                      {item.group === 'workflow'
                        ? item.groupLabel
                        : groupLabels[item.group]}
                    </div>
                  )}
                  <div
                    id={`command-${item.id}`}
                    role="option"
                    aria-selected={isActive}
                    aria-disabled={item.disabled || undefined}
                    data-index={index}
                    onMouseMove={() => setActiveIndex(index)}
                    onClick={item.disabled ? undefined : item.run}
                    className={cn(
                      'flex items-center gap-2.5 rounded-md px-2 py-2 text-sm',
                      item.disabled
                        ? 'cursor-not-allowed opacity-50'
                        : 'cursor-pointer',
                      isActive
                        ? 'bg-accent text-foreground'
                        : 'text-foreground/90'
                    )}
                  >
                    <span className="flex shrink-0 text-muted-foreground [&_svg]:size-4">
                      {item.icon}
                    </span>
                    <span className="min-w-0 truncate">{item.label}</span>
                    {item.hint && (
                      <span className="ml-auto min-w-0 max-w-[45%] truncate pl-3 text-xs text-muted-foreground">
                        {item.hint}
                      </span>
                    )}
                    {item.shortcut && (
                      <kbd className="ml-auto shrink-0 rounded border border-border px-1.5 py-0.5 font-sans text-[11px] text-muted-foreground">
                        {item.shortcut}
                      </kbd>
                    )}
                  </div>
                </React.Fragment>
              );
            })
          )}
        </div>
        <div className="flex items-center gap-4 border-t border-border px-4 py-2 text-xs text-muted-foreground">
          <span className="flex items-center gap-1.5">
            <kbd className="rounded border border-border px-1 font-sans">
              ↑↓
            </kbd>
            {ts('Navigate')}
          </span>
          <span className="flex items-center gap-1.5">
            <kbd className="rounded border border-border px-1 font-sans">↵</kbd>
            {ts('Open')}
          </span>
        </div>
      </DialogContent>
    </Dialog>
  );
}
