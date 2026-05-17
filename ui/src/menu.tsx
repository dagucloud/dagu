import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { UserMenu } from '@/components/UserMenu';
import {
  useCanAccessSystemStatus,
  useCanViewEventLogs,
  useCanManageWebhooks,
  useCanManageSecrets,
  useCanViewAuditLogs,
  useAuth,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useHasFeature, useLicense } from '@/hooks/useLicense';
import { cn } from '@/lib/utils';
import { getResponsiveTitleClass } from '@/lib/text-utils';
import { roleAtLeast } from '@/lib/workspaceAccess';
import { defaultWorkspaceSelection } from '@/lib/workspace';
import { UserRole } from '@/api/v1/schema';
import {
  Activity,
  Bell,
  ChevronDown,
  Gauge,
  Shield,
  Globe,
  History,
  KeyRound,
  Moon,
  Network,
  PanelLeft,
  Sun,
  Terminal,
  Webhook,
} from 'lucide-react';
import * as React from 'react';
import { Link, useLocation } from 'react-router-dom';
import { AppBarContext } from './contexts/AppBarContext';
import { useUserPreferences } from './contexts/UserPreference';
import { useAgentChatContext } from './features/agent';
import { WorkspaceSelector } from './components/workspace/WorkspaceSelector';

type NavItemProps = {
  to: string;
  icon?: React.ReactNode;
  text: string;
  isOpen: boolean;
  onClick?: () => void;
  customColor?: boolean;
  activePaths?: string | string[];
};

type MainListItemsProps = {
  isOpen?: boolean;
  onAgentModeToggle?: () => void;
  onNavItemClick?: () => void;
  onToggle?: () => void;
  customColor?: boolean;
};

const DEFAULT_TITLE = 'Dagu';

function getTitleInitial(title: string): string {
  return title.charAt(0).toUpperCase();
}

function getActiveIndicatorStyle(): string {
  return 'bg-sidebar-primary';
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
  'hover:bg-sidebar-hover hover:shadow-[inset_3px_0_0_0_var(--sidebar-primary),inset_0_0_0_1px_var(--sidebar-border)] active:bg-sidebar-active'
);

const sidebarItemActiveClassName = cn(
  sidebarItemBaseClassName,
  'bg-sidebar-active shadow-[inset_3px_0_0_0_var(--sidebar-primary),inset_0_0_0_1px_var(--sidebar-border)]'
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

type SidebarButtonProps = {
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
  isOpen: boolean;
};

// Developer-tool Sidebar Button - Clean & Minimal
function SidebarButton({
  onClick,
  icon,
  label,
  isOpen,
}: SidebarButtonProps): React.ReactElement {
  return (
    <button
      onClick={onClick}
      className={cn(
        'flex items-center gap-3 w-full p-2 rounded-md group',
        sidebarItemClassName
      )}
      title={isOpen ? '' : label}
    >
      <div className="flex items-center justify-center flex-shrink-0 text-sidebar-foreground">
        {icon}
      </div>
      <span
        className="text-sm font-medium text-sidebar-foreground whitespace-nowrap overflow-hidden"
        style={{
          transition:
            'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1), max-width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
          opacity: isOpen ? 1 : 0,
          maxWidth: isOpen ? '180px' : '0px',
          transform: isOpen ? 'translateX(0)' : 'translateX(-8px)',
        }}
      >
        {label}
      </span>
    </button>
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
}: NavItemProps): React.ReactElement {
  const location = useLocation();
  const isActive = activePaths
    ? isBasePathActive(location, activePaths)
    : isNavTargetActive(location, to);

  const linkClassName = cn(
    'flex items-center rounded-md px-2 group relative',
    'h-9 gap-3',
    'text-sidebar-foreground',
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
        title={isOpen ? '' : text}
      >
        {isActive && (
          <div
            className={cn(
              'absolute left-0 w-[3px] h-6 rounded-r-sm',
              getActiveIndicatorStyle()
            )}
            style={{ transition: 'opacity 200ms ease' }}
          />
        )}
        {icon && <div className={iconClassName}>{icon}</div>}
        <span
          className={cn(
            'text-sm font-medium whitespace-nowrap overflow-hidden',
            isActive ? 'text-sidebar-foreground' : 'text-sidebar-foreground'
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
      </Link>
    </div>
  );
}

type NavGroupProps = {
  groupKey: string;
  icon?: React.ReactNode;
  label: string;
  isOpen: boolean;
  basePath: string | string[];
  to?: string;
  onClick?: () => void;
  customColor?: boolean;
  defaultExpanded?: boolean;
  persistExpanded?: boolean;
  unmountChildrenWhenCollapsed?: boolean;
  children: React.ReactNode;
};

function isNavTargetActive(
  location: ReturnType<typeof useLocation>,
  target: string
): boolean {
  const [targetPath, targetHash] = target.split('#');
  if (targetHash) {
    return (
      location.pathname === targetPath && location.hash === `#${targetHash}`
    );
  }
  return (
    location.pathname === targetPath ||
    (targetPath !== '/' && location.pathname.startsWith(targetPath + '/'))
  );
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

function NavGroup({
  groupKey,
  icon,
  label,
  isOpen,
  basePath,
  to,
  onClick,
  customColor = false,
  defaultExpanded = false,
  persistExpanded = true,
  unmountChildrenWhenCollapsed = false,
  children,
}: NavGroupProps): React.ReactElement {
  const location = useLocation();
  const isChildActive = isBasePathActive(location, basePath);
  const isGroupTargetActive = to ? isNavTargetActive(location, to) : false;

  const [isExpanded, setIsExpanded] = React.useState(() => {
    try {
      if (persistExpanded) {
        const stored = localStorage.getItem(`navgroup_expanded_${groupKey}`);
        if (stored !== null) {
          return stored === 'true';
        }
      }
    } catch {
      /* ignore */
    }
    return defaultExpanded;
  });

  React.useEffect(() => {
    if (!persistExpanded) {
      return;
    }
    try {
      localStorage.setItem(
        `navgroup_expanded_${groupKey}`,
        isExpanded.toString()
      );
    } catch {
      /* ignore */
    }
  }, [isExpanded, groupKey, persistExpanded]);

  const effectivelyExpanded = isOpen && isExpanded;
  const shouldRenderChildren =
    !unmountChildrenWhenCollapsed || effectivelyExpanded;

  const headerClassName = cn(
    'flex items-center rounded-md px-2 group relative w-full',
    'h-9 gap-3',
    'text-sidebar-foreground',
    isChildActive && !effectivelyExpanded
      ? cn(getActiveLinkStyle(), sidebarItemActiveClassName)
      : sidebarItemClassName
  );

  const iconClassName = cn(
    'flex items-center justify-center flex-shrink-0',
    isChildActive ? getActiveIconStyle(customColor) : 'text-sidebar-foreground'
  );

  const labelStyle = {
    transition:
      'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1), max-width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
    opacity: isOpen ? 1 : 0,
    maxWidth: isOpen ? '180px' : '0px',
    transform: isOpen ? 'translateX(0)' : 'translateX(-8px)',
  };

  const chevronStyle = {
    transition:
      'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1)',
    opacity: isOpen ? 0.6 : 0,
    transform: effectivelyExpanded ? 'rotate(0deg)' : 'rotate(-90deg)',
  };

  const activeIndicator = isChildActive ? (
    <div
      className={cn(
        'absolute left-0 w-[3px] h-6 rounded-r-sm',
        getActiveIndicatorStyle()
      )}
      style={{ transition: 'opacity 200ms ease' }}
    />
  ) : null;

  const labelNode = (
    <>
      {icon && <div className={iconClassName}>{icon}</div>}
      <span
        className={cn(
          'text-sm font-medium whitespace-nowrap overflow-hidden',
          isChildActive ? 'text-sidebar-foreground' : 'text-sidebar-foreground'
        )}
        style={labelStyle}
      >
        {label}
      </span>
    </>
  );

  return (
    <div>
      <div className="px-1">
        {to ? (
          <div
            className={cn(headerClassName, 'gap-0 overflow-hidden px-0')}
            title={isOpen ? '' : label}
          >
            {activeIndicator}
            <Link
              to={to}
              onClick={onClick}
              className="flex h-full min-w-0 flex-1 items-center gap-3 px-2 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring"
              aria-current={isGroupTargetActive ? 'page' : undefined}
            >
              {labelNode}
            </Link>
            <button
              type="button"
              onClick={() => setIsExpanded((prev) => !prev)}
              className="flex h-full w-8 flex-shrink-0 items-center justify-center focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring"
              aria-expanded={effectivelyExpanded}
              aria-label={`Toggle ${label} section`}
            >
              <div style={chevronStyle}>
                <ChevronDown size={14} />
              </div>
            </button>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setIsExpanded((prev) => !prev)}
            className={headerClassName}
            title={isOpen ? '' : label}
            aria-expanded={effectivelyExpanded}
            aria-label={`${label} section`}
          >
            {activeIndicator}
            {labelNode}
            <div className="ml-auto flex-shrink-0" style={chevronStyle}>
              <ChevronDown size={14} />
            </div>
          </button>
        )}
      </div>
      <div
        style={{
          transition:
            'max-height 250ms cubic-bezier(0.4, 0, 0.2, 1), opacity 200ms cubic-bezier(0.4, 0, 0.2, 1)',
          maxHeight: effectivelyExpanded ? '640px' : '0px',
          opacity: effectivelyExpanded ? 1 : 0,
          overflow: 'hidden',
        }}
      >
        {shouldRenderChildren && (
          <div className={cn(isOpen && 'pl-4')}>{children}</div>
        )}
      </div>
    </div>
  );
}

export const mainListItems = React.forwardRef<
  HTMLDivElement,
  MainListItemsProps
>(function MainListItems(
  {
    isOpen = false,
    onAgentModeToggle,
    onNavItemClick,
    onToggle,
    customColor = false,
  },
  ref
) {
  const config = useConfig();
  const isAdmin = useIsAdmin();
  const { user } = useAuth();
  const hasRbac = useHasFeature('rbac');
  const hasAudit = useHasFeature('audit');
  const license = useLicense();
  const hasReusableNotifications =
    !license.community && (license.valid || license.gracePeriod);
  const canWrite =
    config.authMode !== 'builtin'
      ? config.permissions.writeDags
      : roleAtLeast(user?.role ?? null, UserRole.developer);
  const canManageNotifications =
    config.authMode !== 'builtin' ||
    roleAtLeast(user?.role ?? null, UserRole.developer);
  const canAccessSystemStatus = useCanAccessSystemStatus();
  const canManageWebhooks = useCanManageWebhooks();
  const canManageSecrets = useCanManageSecrets();
  const canViewEventLogs = useCanViewEventLogs();
  const canViewAuditLogs = useCanViewAuditLogs();
  const { preferences, updatePreference } = useUserPreferences();
  const { toggleChat } = useAgentChatContext();
  const handleAgentClick = onAgentModeToggle ?? toggleChat;

  const theme = preferences.theme || 'dark';
  const title = config.title || DEFAULT_TITLE;
  const titleInitial = getTitleInitial(title);

  function toggleTheme(): void {
    updatePreference('theme', theme === 'dark' ? 'light' : 'dark');
  }

  return (
    <div ref={ref} className="flex flex-col h-full">
      {/* Developer-tool Header - Clean & Minimal */}
      <div className="h-14 relative mb-4 flex items-center border-b border-sidebar-border px-1">
        <button
          onClick={onToggle}
          className={cn(
            'h-9 px-2 rounded-md flex-shrink-0 flex items-center justify-center',
            'text-sidebar-foreground',
            sidebarItemClassName
          )}
          aria-label={isOpen ? 'Collapse sidebar' : 'Expand sidebar'}
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
            'font-semibold tracking-tight text-sidebar-foreground select-none whitespace-nowrap leading-tight ml-1 overflow-hidden',
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

      {/* Developer-tool Navigation - Compact Spacing */}
      <nav className="flex-1 min-h-0 overflow-y-auto pr-1 flex flex-col gap-4">
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
                        aria-label="Remote node"
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

        <div className="space-y-1">
          <NavItem
            to="/"
            text="Overview"
            icon={<Gauge size={18} />}
            isOpen={isOpen}
            onClick={onNavItemClick}
            customColor={customColor}
            activePaths={['/', '/dashboard', '/cockpit']}
          />

          <NavGroup
            groupKey="workflows"
            icon={<Network size={18} />}
            label="Workflows"
            isOpen={isOpen}
            basePath={[
              '/dags',
              '/search',
              '/base-config',
              '/docs',
              '/git-sync',
            ]}
            to="/dags"
            onClick={onNavItemClick}
            customColor={customColor}
          >
            <NavItem
              to="/search"
              text="Search"
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
            {canWrite && (
              <NavItem
                to="/base-config"
                text="Base Config"
                isOpen={isOpen}
                onClick={onNavItemClick}
                customColor={customColor}
              />
            )}
            <NavItem
              to="/docs"
              text="Runbooks"
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
            {canWrite && config.gitSyncEnabled && (
              <NavItem
                to="/git-sync"
                text="Git Sync"
                isOpen={isOpen}
                onClick={onNavItemClick}
                customColor={customColor}
              />
            )}
          </NavGroup>

          <NavGroup
            groupKey="execution"
            icon={<History size={18} />}
            label="Executions"
            isOpen={isOpen}
            basePath={['/dag-runs', '/queues']}
            to="/dag-runs"
            onClick={onNavItemClick}
            customColor={customColor}
          >
            <NavItem
              to="/queues"
              text="Queues"
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
          </NavGroup>

          {(canAccessSystemStatus || canViewEventLogs || canViewAuditLogs) && (
            <NavGroup
              groupKey="monitor"
              icon={<Activity size={18} />}
              label="Monitor"
              isOpen={isOpen}
              basePath={['/event-logs', '/audit-logs', '/system-status']}
              to="/system-status"
              onClick={onNavItemClick}
              customColor={customColor}
            >
              {canViewEventLogs && (
                <NavItem
                  to="/event-logs"
                  text="Events"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
              )}
              {canViewAuditLogs && (
                <NavItem
                  to="/audit-logs"
                  text={hasAudit ? 'Audit Logs' : 'Audit Logs (Pro)'}
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
              )}
            </NavGroup>
          )}

          {canManageNotifications && (
            <NavItem
              to="/notifications"
              text={
                hasReusableNotifications
                  ? 'Notifications'
                  : 'Notifications (Pro)'
              }
              icon={<Bell size={18} />}
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
          )}

          <NavGroup
            groupKey="integrations"
            icon={<Webhook size={18} />}
            label="Integrations"
            isOpen={isOpen}
            basePath={['/integrations', '/webhooks', '/api-docs']}
            to="/integrations"
            onClick={onNavItemClick}
            customColor={customColor}
          >
            {canManageWebhooks && (
              <NavItem
                to="/webhooks"
                text="Webhooks"
                isOpen={isOpen}
                onClick={onNavItemClick}
                customColor={customColor}
              />
            )}
            <NavItem
              to="/api-docs"
              text="API Reference"
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
          </NavGroup>

          {canManageSecrets && (
            <NavItem
              to="/secrets"
              text="Secrets"
              icon={<KeyRound size={18} />}
              isOpen={isOpen}
              onClick={onNavItemClick}
              customColor={customColor}
            />
          )}

          {isAdmin && (
            <NavGroup
              groupKey="administration"
              icon={<Shield size={18} />}
              label="Administration"
              isOpen={isOpen}
              basePath={[
                '/users',
                '/api-keys',
                '/remote-nodes',
                '/terminal',
                '/license',
                '/agent',
                '/agent-settings',
                '/agent-tools',
                '/agent-memory',
                '/agent-souls',
                '/administration',
              ]}
              to="/administration"
              onClick={onNavItemClick}
              customColor={customColor}
              unmountChildrenWhenCollapsed
            >
              {config.authMode === 'builtin' && (
                <NavGroup
                  groupKey="administration-access"
                  label="Access"
                  isOpen={isOpen}
                  basePath={['/users', '/api-keys']}
                  customColor={customColor}
                  persistExpanded={false}
                >
                  <NavItem
                    to="/users"
                    text={hasRbac ? 'Users' : 'Users (Pro)'}
                    isOpen={isOpen}
                    onClick={onNavItemClick}
                    customColor={customColor}
                  />
                  <NavItem
                    to="/api-keys"
                    text="API Keys"
                    isOpen={isOpen}
                    onClick={onNavItemClick}
                    customColor={customColor}
                  />
                </NavGroup>
              )}

              <NavGroup
                groupKey="administration-infrastructure"
                label="Infrastructure"
                isOpen={isOpen}
                basePath={['/remote-nodes', '/terminal', '/license']}
                customColor={customColor}
                persistExpanded={false}
              >
                <NavItem
                  to="/remote-nodes"
                  text="Remote Nodes"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
                {config.terminalEnabled && (
                  <NavItem
                    to="/terminal"
                    text="Terminal"
                    isOpen={isOpen}
                    onClick={onNavItemClick}
                    customColor={customColor}
                  />
                )}
                <NavItem
                  to="/license"
                  text="License"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
              </NavGroup>

              <NavGroup
                groupKey="administration-agent"
                label="Agent"
                isOpen={isOpen}
                basePath={[
                  '/agent',
                  '/agent-settings',
                  '/agent-tools',
                  '/agent-memory',
                  '/agent-souls',
                ]}
                to="/agent"
                onClick={onNavItemClick}
                customColor={customColor}
                persistExpanded={false}
              >
                <NavItem
                  to="/agent-settings"
                  text="Models"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
                <NavItem
                  to="/agent-tools"
                  text="Tools"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
                <NavItem
                  to="/agent-memory"
                  text="Memory"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
                <NavItem
                  to="/agent-souls"
                  text="Souls"
                  isOpen={isOpen}
                  onClick={onNavItemClick}
                  customColor={customColor}
                />
              </NavGroup>
            </NavGroup>
          )}
        </div>
      </nav>

      {/* Developer-tool Footer - Clean Controls */}
      <div className="mt-auto pt-3 border-t border-sidebar-border flex flex-col gap-2">
        <div
          className={cn(
            'px-2',
            !isOpen && 'flex flex-col items-center gap-1.5'
          )}
        >
          {config.agentEnabled && (
            <SidebarButton
              onClick={handleAgentClick}
              icon={<Terminal size={18} />}
              label="Agent"
              isOpen={isOpen}
            />
          )}
          <SidebarButton
            onClick={toggleTheme}
            icon={theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
            label={theme === 'dark' ? 'Light Mode' : 'Dark Mode'}
            isOpen={isOpen}
          />
          <UserMenu isCollapsed={!isOpen} />
        </div>
        {config.version && (
          <div
            className={cn(
              'px-3 pb-3 text-[11px] font-mono text-sidebar-foreground/50',
              !isOpen && 'text-center px-0'
            )}
          >
            {isOpen ? `v${config.version}` : config.version.split('.')[0]}
          </div>
        )}
      </div>
    </div>
  );
});
