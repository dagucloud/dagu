import { LicenseBadge } from '@/components/LicenseBadge';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { UserMenu } from '@/components/UserMenu';
import { useAuth } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { cn } from '@/lib/utils';
import { getResponsiveTitleClass } from '@/lib/text-utils';
import { roleAtLeast } from '@/lib/workspaceAccess';
import { defaultWorkspaceSelection } from '@/lib/workspace';
import { UserRole, ViewSpecType } from '@/api/v1/schema';
import {
  viewMatchesScope,
  viewScopeForSelection,
} from '@/features/views/viewScope';
import {
  Globe,
  GripVertical,
  LayoutGrid,
  PanelLeft,
  Search,
  Star,
} from 'lucide-react';
import * as React from 'react';
import { Link, useLocation } from 'react-router-dom';
import { AppBarContext } from './contexts/AppBarContext';
import { useViews } from '@/hooks/useViews';
import { useUserPreferences } from './contexts/UserPreference';
import { ThemeSelector } from './components/ThemeSelector';
import { WorkspaceSelector } from './components/workspace/WorkspaceSelector';
import { LanguageSelector } from './components/LanguageSelector';
import { WorkspaceDeprecationNotice } from './components/WorkspaceDeprecationNotice';
import { useI18n } from './i18n/I18nProvider';
import { useNavigationSections } from './hooks/useNavigationSections';
import { useScrollFade } from './hooks/useScrollFade';
import {
  commandPaletteShortcut,
  useCommandPalette,
} from './components/command-palette/CommandPaletteProvider';

type NavItemProps = {
  to: string;
  icon?: React.ReactNode;
  text: string;
  isOpen: boolean;
  onClick?: () => void;
  customColor?: boolean;
  activePaths?: string | string[];
  reorderable?: boolean;
  suppressActive?: boolean;
};

type MainListItemsProps = {
  isOpen?: boolean;
  onNavItemClick?: () => void;
  onToggle?: () => void;
  customColor?: boolean;
  /** Leaves the title and collapse toggle to a container that has its own. */
  hideHeader?: boolean;
};

const DEFAULT_TITLE = 'Dagu';

function getTitleInitial(title: string): string {
  return title.charAt(0).toUpperCase();
}

function getActiveLinkStyle(): string {
  return 'bg-sidebar-active';
}

function getActiveIconStyle(customColor: boolean): string {
  return customColor ? 'text-sidebar-foreground' : 'text-sidebar-primary';
}

const sidebarItemBaseClassName =
  'transition-[background-color,box-shadow] duration-150 ease-out focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring';

const sidebarItemClassName = cn(
  sidebarItemBaseClassName,
  'hover:bg-sidebar-hover active:bg-sidebar-active'
);

const sidebarItemActiveClassName = cn(
  sidebarItemBaseClassName,
  'bg-sidebar-active'
);

function uniqueRemoteNodes(nodes: string[]): string[] {
  return [...new Set(nodes.map((node) => node.trim()).filter(Boolean))];
}

type RemoteNodeSelectContentProps = {
  nodes: string[];
};

function RemoteNodeSelectContent({
  nodes,
}: RemoteNodeSelectContentProps): React.ReactElement {
  const uniqueNodes = uniqueRemoteNodes(nodes);
  return (
    <SelectContent>
      {uniqueNodes.map((node) => (
        <SelectItem key={node} value={node}>
          {node}
        </SelectItem>
      ))}
    </SelectContent>
  );
}

// Developer-tool Navigation Item - Clean Active States with Left Border
function NavItem({
  to,
  icon,
  text,
  isOpen,
  onClick,
  customColor = false,
  activePaths,
  reorderable = false,
  suppressActive = false,
}: NavItemProps): React.ReactElement {
  const location = useLocation();
  const { t } = useI18n();
  const isActive =
    !suppressActive &&
    (activePaths
      ? isBasePathActive(location, activePaths)
      : isNavTargetActive(location, to));

  const linkClassName = cn(
    'flex items-center rounded-md px-2 group relative',
    'h-8 gap-2.5',
    'text-sidebar-foreground',
    reorderable && 'cursor-grab active:cursor-grabbing',
    isActive
      ? cn(getActiveLinkStyle(), sidebarItemActiveClassName)
      : sidebarItemClassName
  );

  const iconClassName = cn(
    'flex items-center justify-center flex-shrink-0',
    isActive ? getActiveIconStyle(customColor) : 'text-sidebar-foreground'
  );

  return (
    <div className="px-1">
      <Link
        to={to}
        onClick={onClick}
        className={linkClassName}
        aria-current={isActive ? 'page' : undefined}
        draggable={reorderable ? false : undefined}
        aria-description={
          reorderable ? t('navigation.reorderBookmark') : undefined
        }
        aria-keyshortcuts={
          reorderable ? 'Alt+ArrowUp Alt+ArrowDown' : undefined
        }
        title={
          isOpen ? (reorderable ? t('navigation.reorderBookmark') : '') : text
        }
      >
        {icon && <div className={iconClassName}>{icon}</div>}
        <span
          className={cn(
            'text-sm font-medium whitespace-nowrap overflow-hidden',
            isActive
              ? 'text-sidebar-accent-foreground'
              : 'text-sidebar-foreground group-hover:text-sidebar-accent-foreground'
          )}
          style={{
            transition:
              'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1), max-width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
            opacity: isOpen ? 1 : 0,
            maxWidth: isOpen ? '180px' : '0px',
            transform: isOpen ? 'translateX(0)' : 'translateX(-8px)',
          }}
        >
          {text}
        </span>
        {reorderable && isOpen && (
          <GripVertical
            size={14}
            aria-hidden="true"
            className="ml-auto shrink-0 opacity-0 group-hover:opacity-60 group-focus-visible:opacity-60"
          />
        )}
      </Link>
    </div>
  );
}

function CommandPaletteTrigger({
  isOpen,
  customColor,
}: {
  isOpen: boolean;
  customColor: boolean;
}): React.ReactElement {
  const { open } = useCommandPalette();
  const { ts } = useI18n();
  const shortcut = commandPaletteShortcut();

  return (
    <div className="px-1">
      <button
        type="button"
        onClick={open}
        aria-label={ts('Search...')}
        aria-keyshortcuts="Meta+K Control+K"
        title={isOpen ? undefined : `${ts('Search...')} (${shortcut})`}
        className={cn(
          'flex h-8 w-full items-center gap-2.5 rounded-md px-2 text-sm text-sidebar-foreground transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring',
          isOpen && !customColor
            ? 'border border-sidebar-border bg-card shadow-xs hover:border-border-strong'
            : 'hover:bg-sidebar-hover'
        )}
      >
        <Search size={16} className="shrink-0" />
        {isOpen && (
          <>
            <span className="truncate">{ts('Search...')}</span>
            <kbd className="ml-auto rounded border border-sidebar-border px-1.5 font-sans text-[11px] leading-4">
              {shortcut}
            </kbd>
          </>
        )}
      </button>
    </div>
  );
}

function PinnedViewItems({
  items,
  remoteNode,
  isOpen,
  onNavItemClick,
  customColor,
}: MainListItemsProps & {
  items: (Pick<NavItemProps, 'to' | 'text' | 'icon'> & { id: string })[];
  remoteNode: string;
}): React.ReactElement {
  const { preferences, updatePreference } = useUserPreferences();
  const [draggedId, setDraggedId] = React.useState<string | null>(null);
  const [dropId, setDropId] = React.useState<string | null>(null);
  const storedOrder = preferences.pinnedViewOrder?.[remoteNode];
  const order = [
    ...new Set([
      ...(Array.isArray(storedOrder) ? storedOrder : []),
      ...items.map((item) => item.id),
    ]),
  ];
  const sortedItems = [...items].sort(
    (a, b) => order.indexOf(a.id) - order.indexOf(b.id)
  );

  function moveItem(fromId: string, toId: string): void {
    const ids = sortedItems.map((item) => item.id);
    const from = ids.indexOf(fromId);
    const to = ids.indexOf(toId);
    if (from < 0 || to < 0 || from === to) {
      return;
    }
    ids.splice(from, 1);
    ids.splice(to, 0, fromId);
    const visibleIds = new Set(ids);
    let index = 0;
    updatePreference('pinnedViewOrder', {
      ...preferences.pinnedViewOrder,
      // Preserve the positions of bookmarks hidden by the workspace filter.
      [remoteNode]: order.map((id) =>
        visibleIds.has(id) ? ids[index++]! : id
      ),
    });
  }

  function clearDrag(): void {
    setDraggedId(null);
    setDropId(null);
  }

  return (
    <>
      {sortedItems.map((item, index) => (
        <div
          key={item.id}
          draggable
          className={cn(
            'relative cursor-grab active:cursor-grabbing',
            draggedId === item.id && 'opacity-50',
            dropId === item.id &&
              draggedId !== item.id &&
              'after:absolute after:inset-x-1 after:h-0.5 after:bg-sidebar-primary',
            dropId === item.id &&
              (sortedItems.findIndex((item) => item.id === draggedId) < index
                ? 'after:bottom-0'
                : 'after:top-0')
          )}
          onDragStart={(event) => {
            event.dataTransfer.effectAllowed = 'move';
            event.dataTransfer.setData('text/plain', item.id);
            setDraggedId(item.id);
          }}
          onDragOver={(event) => {
            if (draggedId === null) {
              return;
            }
            event.preventDefault();
            event.dataTransfer.dropEffect = 'move';
            setDropId(item.id);
          }}
          onDragLeave={(event) => {
            if (
              !event.currentTarget.contains(event.relatedTarget as Node | null)
            ) {
              setDropId(null);
            }
          }}
          onDrop={(event) => {
            if (draggedId !== null) {
              event.preventDefault();
              moveItem(draggedId, item.id);
            }
            clearDrag();
          }}
          onDragEnd={clearDrag}
          onKeyDown={(event) => {
            if (
              !event.altKey ||
              !['ArrowUp', 'ArrowDown'].includes(event.key)
            ) {
              return;
            }
            event.preventDefault();
            const target =
              sortedItems[index + (event.key === 'ArrowUp' ? -1 : 1)];
            if (target) {
              moveItem(item.id, target.id);
            }
          }}
        >
          <NavItem
            {...item}
            isOpen={isOpen ?? false}
            onClick={onNavItemClick}
            customColor={customColor}
            reorderable
          />
        </div>
      ))}
    </>
  );
}

function isNavTargetActive(
  location: ReturnType<typeof useLocation>,
  target: string
): boolean {
  const [targetWithoutHash = '', targetHash] = target.split('#');
  const [targetPath, targetSearch] = targetWithoutHash.split('?');
  if (targetHash) {
    return (
      location.pathname === targetPath && location.hash === `#${targetHash}`
    );
  }
  const pathMatches =
    location.pathname === targetPath ||
    (targetPath !== '/' && location.pathname.startsWith(targetPath + '/'));
  if (!pathMatches) {
    return false;
  }
  if (targetSearch) {
    if (location.pathname !== targetPath) {
      return false;
    }
    const expected = new URLSearchParams(targetSearch);
    const actual = new URLSearchParams(location.search);
    return [...expected].every(([key, value]) => actual.get(key) === value);
  }
  return true;
}

function isBasePathActive(
  location: ReturnType<typeof useLocation>,
  basePath: string | string[]
): boolean {
  const paths = Array.isArray(basePath) ? basePath : [basePath];
  return paths.some((path) => {
    if (path.includes('#')) {
      return isNavTargetActive(location, path);
    }
    if (path === '/') {
      return location.pathname === '/';
    }
    return (
      location.pathname === path ||
      location.pathname.startsWith(path + '/') ||
      (path.endsWith('-') && location.pathname.startsWith(path))
    );
  });
}

export const mainListItems = React.forwardRef<
  HTMLDivElement,
  MainListItemsProps
>(function MainListItems(
  {
    isOpen = false,
    onNavItemClick,
    onToggle,
    customColor = false,
    hideHeader = false,
  },
  ref
) {
  const config = useConfig();
  const { user } = useAuth();
  const appBar = React.useContext(AppBarContext);
  const location = useLocation();
  const { views: kanbanViews } = useViews();
  const { views: workflowViews } = useViews(ViewSpecType.workflow);
  const { views: runViews } = useViews(ViewSpecType.run);
  const { views: artifactViews } = useViews(ViewSpecType.artifact);
  const workflowViewScope = viewScopeForSelection(appBar.workspaceSelection);
  const pinnedKanbanViews = kanbanViews.filter((view) => view.pinned);
  const pinnedRunViews = runViews.filter(
    (view) => view.pinned && viewMatchesScope(view, workflowViewScope)
  );
  const pinnedWorkflowViews = workflowViews.filter(
    (view) => view.pinned && viewMatchesScope(view, workflowViewScope)
  );
  const pinnedArtifactViews = artifactViews.filter(
    (view) => view.pinned && viewMatchesScope(view, workflowViewScope)
  );
  const activeViewId = new URLSearchParams(location.search).get('view');
  const isPinnedWorkflowViewActive =
    location.pathname === '/dags' &&
    pinnedWorkflowViews.some((view) => view.id === activeViewId);
  const isPinnedRunViewActive =
    location.pathname === '/dag-runs' &&
    pinnedRunViews.some((view) => view.id === activeViewId);
  const isPinnedArtifactViewActive =
    location.pathname === '/artifacts' &&
    pinnedArtifactViews.some((view) => view.id === activeViewId);
  const canWrite =
    config.authMode !== 'builtin'
      ? config.permissions.writeDags
      : roleAtLeast(user?.role ?? null, UserRole.developer);
  const { t } = useI18n();
  const navigation = useNavigationSections();
  const pinnedViewActive: Record<string, boolean> = {
    '/dags': isPinnedWorkflowViewActive,
    '/dag-runs': isPinnedRunViewActive,
    '/artifacts': isPinnedArtifactViewActive,
  };
  const mainItems = navigation.mainItems.map((item) => ({
    ...item,
    suppressActive: pinnedViewActive[item.to] ?? false,
  }));
  const navSections = navigation.sections;

  const title = config.title || DEFAULT_TITLE;
  const titleInitial = getTitleInitial(title);
  // The nav scrolls on short screens; the fade shows sections below.
  const [navRef, navFade] = useScrollFade<HTMLElement>('y');

  return (
    <div ref={ref} className="flex flex-col h-full">
      {/* Developer-tool Header - Clean & Minimal */}
      {!hideHeader && (
        <div className="h-14 relative mb-4 flex items-center border-b border-sidebar-border px-1">
          <button
            onClick={onToggle}
            className={cn(
              'h-9 px-2 rounded-md flex-shrink-0 flex items-center justify-center',
              'text-sidebar-foreground',
              sidebarItemClassName
            )}
            aria-label={
              isOpen
                ? t('navigation.collapseSidebar')
                : t('navigation.expandSidebar')
            }
          >
            {/* Expand icon (character) - visible when collapsed */}
            <div
              className="w-7 h-7 rounded-md flex items-center justify-center border border-sidebar-foreground absolute"
              style={{
                transition:
                  'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1)',
                opacity: isOpen ? 0 : 1,
                transform: isOpen ? 'scale(0.8)' : 'scale(1)',
                pointerEvents: isOpen ? 'none' : 'auto',
              }}
            >
              <span className="font-medium text-xs text-sidebar-foreground">
                {titleInitial}
              </span>
            </div>
            {/* Collapse icon (PanelLeft) - visible when expanded */}
            <div
              style={{
                transition:
                  'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1)',
                opacity: isOpen ? 1 : 0,
                transform: isOpen ? 'scale(1)' : 'scale(0.8)',
              }}
            >
              <PanelLeft size={18} />
            </div>
          </button>
          <span
            className={cn(
              'font-semibold tracking-tight text-sidebar-accent-foreground select-none whitespace-nowrap leading-tight ml-1 overflow-hidden',
              getResponsiveTitleClass(title, 'sidebar-expanded')
            )}
            style={{
              transition:
                'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1), max-width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
              opacity: isOpen ? 1 : 0,
              maxWidth: isOpen ? '180px' : '0px',
              transform: isOpen ? 'translateX(0)' : 'translateX(-8px)',
            }}
          >
            {title}
          </span>
        </div>
      )}
      {/* Developer-tool Navigation - Compact Spacing */}
      <nav
        ref={navRef}
        style={navFade}
        aria-label={t('navigation.primary')}
        className="flex-1 min-h-0 overflow-y-auto pr-1 flex flex-col gap-4"
      >
        <AppBarContext.Consumer>
          {(context) => {
            const {
              remoteNodes,
              selectedRemoteNode,
              selectRemoteNode,
              workspaces,
              workspaceSelection,
              selectWorkspace,
              createWorkspace,
              deleteWorkspace,
            } = context;
            const selectableRemoteNodes = uniqueRemoteNodes(remoteNodes ?? []);
            if (selectableRemoteNodes.length === 0) return null;

            return (
              <div className="space-y-2">
                <WorkspaceSelector
                  workspaces={workspaces ?? []}
                  workspaceSelection={
                    workspaceSelection ?? defaultWorkspaceSelection()
                  }
                  onSelectWorkspace={(selection) =>
                    void selectWorkspace?.(selection)
                  }
                  onCreate={(name) => void createWorkspace?.(name)}
                  onDelete={(id) => void deleteWorkspace?.(id)}
                  canWrite={canWrite && isOpen}
                  variant="sidebar"
                  collapsed={!isOpen}
                />
                {selectableRemoteNodes.length > 1 && (
                  <div className="px-1">
                    <Select
                      value={selectedRemoteNode}
                      onValueChange={selectRemoteNode}
                    >
                      <SelectTrigger
                        aria-label={t('navigation.remoteNode')}
                        className={cn(
                          'h-9 text-xs text-sidebar-foreground rounded-md',
                          isOpen
                            ? 'bg-sidebar-hover border-sidebar-border hover:bg-sidebar-active'
                            : 'bg-transparent border-transparent hover:bg-sidebar-hover [&>svg:last-child]:hidden'
                        )}
                        style={{
                          transition:
                            'width 280ms cubic-bezier(0.4, 0, 0.2, 1), background-color 150ms ease, border-color 150ms ease, padding 280ms cubic-bezier(0.4, 0, 0.2, 1)',
                          width: isOpen ? '100%' : '36px',
                          paddingLeft: isOpen ? '12px' : '9px',
                          paddingRight: isOpen ? '12px' : '9px',
                        }}
                      >
                        <div className="flex items-center gap-2">
                          <Globe
                            size={18}
                            className="text-sidebar-foreground flex-shrink-0"
                          />
                          <span
                            className="overflow-hidden whitespace-nowrap"
                            style={{
                              transition:
                                'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), max-width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
                              opacity: isOpen ? 1 : 0,
                              maxWidth: isOpen ? '150px' : '0px',
                            }}
                          >
                            <SelectValue />
                          </span>
                        </div>
                      </SelectTrigger>
                      <RemoteNodeSelectContent nodes={selectableRemoteNodes} />
                    </Select>
                  </div>
                )}
              </div>
            );
          }}
        </AppBarContext.Consumer>

        <CommandPaletteTrigger isOpen={isOpen} customColor={customColor} />

        <div className="space-y-0.5">
          <PinnedViewItems
            key={appBar.selectedRemoteNode}
            remoteNode={appBar.selectedRemoteNode || 'local'}
            items={[
              ...pinnedKanbanViews.map((view) => ({
                id: view.id,
                to: `/views/${view.id}`,
                text: view.name,
                icon: <LayoutGrid size={18} />,
              })),
              ...pinnedRunViews.map((view) => ({
                id: view.id,
                to: `/dag-runs?view=${encodeURIComponent(view.id)}`,
                text: view.name,
                icon: <Star size={18} />,
              })),
              ...pinnedWorkflowViews.map((view) => ({
                id: view.id,
                to: `/dags?view=${encodeURIComponent(view.id)}`,
                text: view.name,
                icon: <Star size={18} />,
              })),
              ...pinnedArtifactViews.map((view) => ({
                id: view.id,
                to: `/artifacts?view=${encodeURIComponent(view.id)}`,
                text: view.name,
                icon: <Star size={18} />,
              })),
            ]}
            isOpen={isOpen}
            onNavItemClick={onNavItemClick}
            customColor={customColor}
          />

          {mainItems.map((item) => (
            <NavItem
              key={item.to}
              {...item}
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
          ))}
        </div>

        {navSections.map((section) => {
          const { items } = section;
          return (
            <div
              key={section.key}
              role="group"
              aria-label={section.label}
              className="space-y-0.5"
            >
              {isOpen ? (
                <div
                  aria-hidden="true"
                  className={cn(
                    'overflow-hidden whitespace-nowrap px-3 pb-1 text-xs font-medium text-muted-foreground',
                    customColor && 'opacity-70'
                  )}
                >
                  {section.label}
                </div>
              ) : (
                <div
                  aria-hidden="true"
                  className="mx-2 mb-1.5 h-px bg-sidebar-border"
                />
              )}
              {items.map((item) => (
                <NavItem
                  key={item.to}
                  {...item}
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
              ))}
            </div>
          );
        })}
      </nav>

      {/* Developer-tool Footer - Clean Controls */}
      <div className="mt-auto flex flex-col gap-1 border-t border-sidebar-border pb-2 pt-2">
        <div
          className={cn(
            'px-2',
            isOpen
              ? 'flex items-center gap-0.5'
              : 'flex flex-col items-center gap-1.5'
          )}
        >
          <div className={cn(isOpen && 'min-w-0 flex-1')}>
            <UserMenu isCollapsed={!isOpen} />
          </div>
          <ThemeSelector compact />
          <LanguageSelector variant="sidebar" compact />
          <WorkspaceDeprecationNotice />
        </div>
        <div
          className={cn(
            'flex min-w-0 items-center gap-2',
            isOpen ? 'px-3' : 'flex-col px-0 text-center'
          )}
        >
          {config.version && (
            <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
              {isOpen ? `v${config.version}` : config.version.split('.')[0]}
            </span>
          )}
          <div className="min-w-0">
            <LicenseBadge onNavigate={onNavItemClick} compact={!isOpen} />
          </div>
        </div>
      </div>
    </div>
  );
});
