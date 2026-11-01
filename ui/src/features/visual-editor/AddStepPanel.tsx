// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Search, Star } from 'lucide-react';
import {
  Fragment,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
} from 'react';
import { PanelHeader } from '@/features/dags/components/step-details/PanelShell';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { actionIcon } from './canvas/icons';
import { ADD_STEP_ALIASES } from './addStepAliases';
import {
  groupCatalog,
  groupRanked,
  rankCatalog,
  requiredKeys,
  templateYaml,
  type AliasMap,
} from './canvas/paletteModel';
import type { ActionCatalogEntry } from './contracts';
import { YamlSnippet } from './YamlSnippet';
import {
  readFavouriteActions,
  toggleFavouriteAction,
  writeFavouriteActions,
} from './recentActions';

export interface AddStepPanelProps {
  catalog: ActionCatalogEntry[];
  /** Where the step will go, in words: "After fetch". */
  targetLabel: string;
  /** Names of the actions that the user added lately, newest first. */
  recent: string[];
  /** `stay` is set for Shift+Enter: the panel stays open for one more step. */
  onPick(entry: ActionCatalogEntry, options?: { stay: true }): void;
  onClose(): void;
  /** Search words for other names of actions; the default is ADD_STEP_ALIASES. */
  aliases?: AliasMap;
  /**
   * The action that the pointer or the keyboard is on, for the ghost on the
   * canvas. Null before the user moves in the list and when the
   * panel closes. It is called at once, with no delay.
   */
  onHoverEntry?(entry: ActionCatalogEntry | null): void;
}

/** The kind of node that an action makes, to give it the icon of the canvas. */
function iconOf(entry: ActionCatalogEntry) {
  if (entry.name === 'run') return actionIcon({ kind: 'run' });
  if (entry.name === 'router.route') return actionIcon({ kind: 'router' });
  if (entry.name === 'dag.run') return actionIcon({ kind: 'call' });
  return actionIcon({ kind: 'action', action: entry.name });
}

/** The actions that most workflows need, shown first when nothing is typed. */
const POPULAR_ACTIONS = [
  'run',
  'http.request',
  'dag.run',
  'router.route',
  'jq.filter',
  'log.write',
];

/**
 * A short line for the popular actions whose catalog text is missing or only
 * repeats the action name.
 */
const POPULAR_BLURBS: Record<string, string> = {
  run: 'Run a command in a shell.',
  'http.request': 'Call a URL and keep the response.',
  'dag.run': 'Run another workflow as a step.',
  'router.route': 'Send the run down one branch by a value.',
  'jq.filter': 'Reshape JSON with a jq expression.',
  'log.write': 'Write a message to the log.',
};

interface Section {
  id: string;
  title: string;
  entries: ActionCatalogEntry[];
}

function entriesNamed(
  catalog: ActionCatalogEntry[],
  names: string[]
): ActionCatalogEntry[] {
  return names
    .map((name) => catalog.find((entry) => entry.name === name))
    .filter((entry): entry is ActionCatalogEntry => !!entry);
}

/**
 * The sections of the list. Without a query: the favourites, the actions used
 * lately, the popular ones, then one for each namespace. With a query: the
 * matches by namespace, the group of the best match first.
 */
export function buildSections(
  catalog: ActionCatalogEntry[],
  query: string,
  recent: string[],
  titles: {
    recent: string;
    popular: string;
    favourites?: string;
  },
  options: {
    favourites?: string[];
    aliases?: AliasMap;
  } = {}
): Section[] {
  if (query.trim() !== '') {
    const ranked = rankCatalog(catalog, query, options.aliases);
    return [
      ...groupRanked(ranked).map((group) => ({
        id: group.namespace,
        title: group.namespace,
        entries: group.entries,
      })),
    ];
  }
  const sections: Section[] = [];
  // An action shows once: in the first section that lists it.
  const listed = new Set<string>();
  const add = (id: string, title: string, entries: ActionCatalogEntry[]) => {
    const fresh = entries.filter((entry) => !listed.has(entry.name));
    if (fresh.length === 0) return;
    fresh.forEach((entry) => listed.add(entry.name));
    sections.push({ id, title, entries: fresh });
  };
  add(
    'favourites',
    titles.favourites ?? 'Favourites',
    entriesNamed(catalog, options.favourites ?? [])
  );
  add('recent', titles.recent, entriesNamed(catalog, recent));
  add('popular', titles.popular, entriesNamed(catalog, POPULAR_ACTIONS));
  for (const group of groupCatalog(catalog)) {
    add(group.namespace, group.namespace, group.entries);
  }
  return sections;
}

/**
 * The list of steps that opens beside the canvas when the user adds a step:
 * a search box, the favourite actions, the actions used lately, then every
 * action by namespace, each with its icon and its description. A strip under
 * the list shows the YAML that the active action writes. Arrow keys, Home and
 * End move; Enter adds; Shift+Enter adds and keeps the panel open; Escape
 * clears the search, then closes. It builds no edit: the editor does that
 * from `onPick`.
 */
export function AddStepPanel({
  catalog,
  targetLabel,
  recent,
  onPick,
  onClose,
  aliases = ADD_STEP_ALIASES,
  onHoverEntry,
}: AddStepPanelProps): ReactElement {
  const { ts } = useI18n();
  const baseId = useId();
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndexState] = useState(0);
  // The first row is lit from the start but is no choice until the user
  // moves in the list or types.
  const [moved, setMoved] = useState(false);
  const setActiveIndex = (index: number) => {
    setMoved(true);
    setActiveIndexState(index);
  };
  const [favourites, setFavourites] = useState(readFavouriteActions);

  const titles = useMemo(
    () => ({
      recent: ts('Recently used'),
      popular: ts('Popular'),
      favourites: ts('Favourites'),
    }),
    [ts]
  );
  const sections = useMemo(
    () =>
      buildSections(catalog, query, recent, titles, { favourites, aliases }),
    [catalog, query, recent, titles, favourites, aliases]
  );
  // The list is by position: a row is its place, not its action.
  const rows = useMemo(
    () =>
      sections.flatMap((section) =>
        section.entries.map((entry) => ({ section: section.id, entry }))
      ),
    [sections]
  );
  // The list can become shorter under the active row, after a pick.
  const current = Math.min(activeIndex, rows.length - 1);
  const active = current >= 0 ? rows[current] : undefined;
  const rowId = (index: number) => `${baseId}-row-${index}`;
  const shell = catalog.find((entry) => entry.name === 'run');
  // One line for every row, so that the heights of the rows agree.
  const describe = (entry: ActionCatalogEntry): string => {
    const text = entry.description?.trim();
    if (text && text !== entry.name) return text;
    const blurb = POPULAR_BLURBS[entry.name];
    return blurb ? ts(blurb) : entry.name;
  };

  useEffect(() => {
    document.getElementById(rowId(current))?.scrollIntoView?.({
      block: 'nearest',
    });
  }, [current, rows]);

  const changeQuery = (value: string) => {
    setQuery(value);
    setActiveIndexState(0);
  };

  const shown = moved || query.trim() !== '' ? (active?.entry ?? null) : null;
  const hoverRef = useRef(onHoverEntry);
  hoverRef.current = onHoverEntry;
  useEffect(() => {
    hoverRef.current?.(shown);
  }, [shown]);
  useEffect(() => () => hoverRef.current?.(null), []);

  const pick = (entry: ActionCatalogEntry, stay: boolean) => {
    if (stay) {
      onPick(entry, { stay: true });
    } else {
      onPick(entry);
    }
  };

  const toggleFavourite = (name: string) => {
    const next = toggleFavouriteAction(favourites, name);
    setFavourites(next);
    writeFavouriteActions(next);
    // The active row stays on the action, which moves in or out of the
    // favourites: a second toggle undoes the first.
    const place = buildSections(catalog, query, recent, titles, {
      favourites: next,
      aliases,
    })
      .flatMap((section) => section.entries)
      .findIndex((entry) => entry.name === name);
    if (place >= 0) setActiveIndexState(place);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    // Enter and Escape of an input method confirm or cancel its text.
    if (event.nativeEvent.isComposing) return;
    const last = rows.length - 1;
    if (event.altKey && event.shiftKey && event.code === 'KeyF') {
      if (active) toggleFavourite(active.entry.name);
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    switch (event.key) {
      case 'ArrowDown':
        setActiveIndex(Math.max(Math.min(current + 1, last), 0));
        break;
      case 'ArrowUp':
        setActiveIndex(Math.max(current - 1, 0));
        break;
      // Shift+Home and Shift+End keep selecting the text of the search.
      case 'Home':
        if (event.shiftKey) return;
        setActiveIndex(0);
        break;
      case 'End':
        if (event.shiftKey) return;
        setActiveIndex(Math.max(last, 0));
        break;
      case 'Enter':
        if (active) pick(active.entry, event.shiftKey);
        break;
      case 'Escape':
        if (query !== '') changeQuery('');
        else onClose();
        break;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
  };

  const required = active ? requiredKeys(active.entry) : [];
  let index = -1;
  return (
    <div
      data-testid="ve-add-step"
      className="flex min-h-0 flex-1 flex-col bg-card"
    >
      <PanelHeader
        title={ts('Add a step')}
        subtitle={targetLabel}
        closeLabel={ts('Close')}
        onClose={onClose}
      />
      <div className="border-b border-border p-3">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground"
            aria-hidden="true"
          />
          <input
            autoFocus
            type="text"
            role="combobox"
            aria-expanded
            aria-controls={`${baseId}-list`}
            aria-activedescendant={active ? rowId(current) : undefined}
            aria-keyshortcuts="Alt+Shift+F"
            aria-label={ts('Search actions')}
            placeholder={ts('Search for an action')}
            value={query}
            onChange={(event) => changeQuery(event.target.value)}
            onKeyDown={onKeyDown}
            className="h-9 w-full rounded-md border bg-background pl-8 pr-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </div>
      </div>
      <div
        id={`${baseId}-list`}
        role="listbox"
        aria-label={ts('Step palette')}
        onMouseDown={(event) => event.preventDefault()}
        className="min-h-0 flex-1 overflow-y-auto py-1"
      >
        {rows.length === 0 ? (
          <div className="space-y-2 px-4 py-6 text-center text-sm text-muted-foreground">
            <p>{ts('No matching actions')}</p>
            <div className="flex flex-wrap justify-center gap-2">
              {shell && (
                <button
                  type="button"
                  onClick={() => onPick(shell)}
                  className="rounded-md border px-2 py-1 text-xs font-medium text-foreground hover:bg-accent"
                >
                  {ts('Add a shell command')}
                </button>
              )}
              <button
                type="button"
                onClick={() => changeQuery('')}
                className="rounded-md border px-2 py-1 text-xs font-medium text-foreground hover:bg-accent"
              >
                {ts('Show all actions')}
              </button>
            </div>
          </div>
        ) : (
          sections.map((section) => (
            <Fragment key={section.id}>
              <div
                aria-hidden="true"
                className="sticky top-0 z-10 flex items-center justify-between bg-card px-4 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground"
              >
                <span>
                  {section.id === 'core' ? ts('Core') : section.title}
                </span>
                <span className="font-normal">{section.entries.length}</span>
              </div>
              {section.entries.map((entry) => {
                index += 1;
                const own = index;
                const Icon = iconOf(entry);
                const starred = favourites.includes(entry.name);
                return (
                  <div
                    key={`${section.id}:${entry.name}`}
                    id={rowId(own)}
                    role="option"
                    aria-selected={own === current}
                    onMouseEnter={() => setActiveIndex(own)}
                    onClick={(event) => pick(entry, event.shiftKey)}
                    className={cn(
                      'group mx-2 flex cursor-default items-start gap-3 rounded-md px-2 py-2',
                      own === current && 'bg-accent text-accent-foreground'
                    )}
                  >
                    <span className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                      <Icon className="h-4 w-4" aria-hidden="true" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">
                        {entry.title}
                      </span>
                      <span
                        className="block truncate text-xs text-muted-foreground"
                        title={describe(entry)}
                      >
                        {describe(entry)}
                      </span>
                    </span>
                    <span className="mt-1 max-w-[7rem] shrink-0 truncate font-mono text-[10px] text-muted-foreground">
                      {entry.name}
                    </span>
                    {/* Not a button: an option holds no control. The keyboard
                        toggles the favourite with Alt+Shift+F in the search. */}
                    <span
                      aria-hidden="true"
                      title={
                        starred
                          ? ts('Remove from favourites')
                          : ts('Add to favourites')
                      }
                      onClick={(event) => {
                        event.stopPropagation();
                        toggleFavourite(entry.name);
                      }}
                      className={cn(
                        'mt-0.5 flex h-6 w-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground',
                        !starred &&
                          own !== current &&
                          'opacity-0 group-hover:opacity-100'
                      )}
                    >
                      <Star
                        className={cn(
                          'h-3.5 w-3.5',
                          starred && 'fill-current text-warning'
                        )}
                        aria-hidden="true"
                      />
                    </span>
                  </div>
                );
              })}
            </Fragment>
          ))
        )}
      </div>
      {active && (
        <section
          aria-label={ts('Details of the action')}
          data-testid="ve-add-step-details"
          className="space-y-1 border-t border-border px-4 py-2 text-xs"
        >
          <p className="flex flex-wrap items-center gap-1 text-muted-foreground">
            {required.length === 0 ? (
              ts('No required keys')
            ) : (
              <>
                <span>{ts('Required keys')}</span>
                {required.map((key) => (
                  <code
                    key={key}
                    className="rounded bg-muted px-1 font-mono text-[11px] text-foreground"
                  >
                    {key}
                  </code>
                ))}
              </>
            )}
          </p>
          <YamlSnippet
            data-testid="ve-add-step-yaml"
            source={templateYaml(active.entry)}
            required={required}
            requiredLabel={ts('Required key')}
            className="max-h-28 rounded bg-muted px-2 py-1"
          />
        </section>
      )}
      <footer className="border-t border-border px-4 py-2 text-[11px] text-muted-foreground">
        {rows.length === 0
          ? ts('Nothing matches: Esc clears the search')
          : ts(
              'Up and Down to move, Enter to add, Shift+Enter to add another, Esc to clear or close'
            )}
      </footer>
    </div>
  );
}
