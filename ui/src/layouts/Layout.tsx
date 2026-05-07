// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { LicenseBanner } from '@/components/LicenseBanner';
import { UpdateBanner } from '@/components/UpdateBanner';
import { useConfig } from '@/contexts/ConfigContext';
import { cn } from '@/lib/utils';
import { getResponsiveTitleClass } from '@/lib/text-utils';
import { Menu, Terminal, X } from 'lucide-react';
import { AgentChatPanel, useAgentChatContext } from '@/features/agent';
import * as React from 'react';
import { useLocation } from 'react-router-dom';
import { ContentNavigation } from './ContentNavigation';
import { mainListItems as MainListItems } from '../menu';

/**
 * Choose a readable foreground color (black or white) that contrasts with the given background color.
 */
function getContrastColor(input?: string): string {
  if (!input) return '#000';

  let hex = input.trim();

  if (!/^#([A-Fa-f0-9]{3}){1,2}$/.test(hex)) {
    if (typeof window !== 'undefined') {
      const temp = document.createElement('div');
      temp.style.color = hex;
      document.body.appendChild(temp);
      const computed = getComputedStyle(temp).color;
      document.body.removeChild(temp);

      const rgbMatch = computed.match(/^rgba?\((\d+),\s*(\d+),\s*(\d+)/);
      if (rgbMatch && rgbMatch[1] && rgbMatch[2] && rgbMatch[3]) {
        const r = parseInt(rgbMatch[1], 10);
        const g = parseInt(rgbMatch[2], 10);
        const b = parseInt(rgbMatch[3], 10);
        const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
        return luminance > 0.4 ? '#000' : '#fff';
      }
    }
    return '#fff';
  }

  hex = hex.replace('#', '');
  let r = 0,
    g = 0,
    b = 0;
  if (hex.length === 3) {
    if (hex[0] && hex[1] && hex[2]) {
      r = parseInt(hex[0] + hex[0], 16);
      g = parseInt(hex[1] + hex[1], 16);
      b = parseInt(hex[2] + hex[2], 16);
    } else {
      return '#000';
    }
  } else if (hex.length === 6) {
    r = parseInt(hex.substring(0, 2), 16);
    g = parseInt(hex.substring(2, 4), 16);
    b = parseInt(hex.substring(4, 6), 16);
  } else {
    return '#000';
  }
  const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
  return luminance > 0.4 ? '#000' : '#fff';
}

function getSidebarOverlayColor(foreground: string, alpha: number): string {
  const channel = foreground === '#000' ? '0, 0, 0' : '255, 255, 255';
  return `rgba(${channel}, ${alpha})`;
}

// Constants
const NAV_SIDEBAR_EXPANDED_WIDTH = 240;
const NAV_SIDEBAR_COLLAPSED_WIDTH = 56;
const AGENT_SIDEBAR_DEFAULT_WIDTH = 420;
const AGENT_SIDEBAR_MIN_WIDTH = 320;
const AGENT_SIDEBAR_MAX_WIDTH = 720;
const AGENT_SIDEBAR_MIN_CONTENT_WIDTH = 360;
const AGENT_SIDEBAR_WIDTH_STORAGE_KEY = 'agentSidebarWidth';
const SIDEBAR_MODE_STORAGE_KEY = 'sidebarMode';

type SidebarMode = 'navigation' | 'agent';

type LayoutProps = {
  navbarColor?: string;
  children?: React.ReactElement | React.ReactElement[];
};

function getAgentSidebarMaxWidth(): number {
  if (typeof window === 'undefined') {
    return AGENT_SIDEBAR_MAX_WIDTH;
  }

  return Math.max(
    AGENT_SIDEBAR_MIN_WIDTH,
    Math.min(
      AGENT_SIDEBAR_MAX_WIDTH,
      window.innerWidth - AGENT_SIDEBAR_MIN_CONTENT_WIDTH
    )
  );
}

function clampAgentSidebarWidth(width: number): number {
  return Math.min(
    getAgentSidebarMaxWidth(),
    Math.max(AGENT_SIDEBAR_MIN_WIDTH, Math.round(width))
  );
}

function getInitialAgentSidebarWidth(): number {
  try {
    const saved = localStorage.getItem(AGENT_SIDEBAR_WIDTH_STORAGE_KEY);
    if (saved) {
      const parsed = Number(saved);
      if (Number.isFinite(parsed)) {
        return clampAgentSidebarWidth(parsed);
      }
    }
  } catch {
    // Ignore unavailable storage and fall back to the default width.
  }

  return clampAgentSidebarWidth(AGENT_SIDEBAR_DEFAULT_WIDTH);
}

function isSidebarMode(value: string | null): value is SidebarMode {
  return value === 'navigation' || value === 'agent';
}

function getInitialSidebarMode(): SidebarMode {
  try {
    const saved = localStorage.getItem(SIDEBAR_MODE_STORAGE_KEY);
    if (isSidebarMode(saved)) {
      return saved;
    }
  } catch {
    // Ignore unavailable storage and fall back to navigation.
  }

  return 'navigation';
}

/**
 * Render the application's main layout with a responsive sidebar and scrollable content area.
 *
 * The desktop sidebar expansion state is persisted to `localStorage` under `sidebarExpanded`.
 * The sidebar uses `navbarColor` when provided and computes an appropriate contrast color for its text.
 *
 * @param navbarColor - Optional CSS color used as the sidebar background
 * @param children - Content rendered in the main scrollable area of the layout
 * @returns The JSX element for the full layout (sidebar and main content)
 */
function Content({ navbarColor, children }: LayoutProps) {
  const config = useConfig();
  const { toggleChat } = useAgentChatContext();
  const location = useLocation();

  const hasCustomColor: boolean = Boolean(
    navbarColor && navbarColor.trim() !== ''
  );
  const contrastColor = hasCustomColor
    ? getContrastColor(navbarColor)
    : undefined;
  const sidebarStyle = hasCustomColor
    ? ({
        backgroundColor: navbarColor,
        color: contrastColor,
        '--sidebar-foreground': contrastColor,
        '--sidebar-primary': contrastColor,
        '--sidebar-ring': contrastColor,
        '--sidebar-hover': getSidebarOverlayColor(contrastColor ?? '#fff', 0.1),
        '--sidebar-active': getSidebarOverlayColor(
          contrastColor ?? '#fff',
          0.16
        ),
        '--sidebar-border': getSidebarOverlayColor(
          contrastColor ?? '#fff',
          0.18
        ),
      } as React.CSSProperties)
    : undefined;
  // Sidebar state with localStorage persistence
  const [isSidebarExpanded, setIsSidebarExpanded] = React.useState(() => {
    const saved = localStorage.getItem('sidebarExpanded');
    return saved ? saved === 'true' : true;
  });
  const [sidebarMode, setSidebarMode] =
    React.useState<SidebarMode>(getInitialSidebarMode);
  const [agentSidebarWidth, setAgentSidebarWidth] = React.useState(
    getInitialAgentSidebarWidth
  );
  const [isResizingAgentSidebar, setIsResizingAgentSidebar] =
    React.useState(false);
  // Mobile sidebar state (hidden by default)
  const [isMobileSidebarOpen, setIsMobileSidebarOpen] = React.useState(false);
  const isAgentSidebarOpen = sidebarMode === 'agent' && config.agentEnabled;

  // Save sidebar state to localStorage when it changes
  React.useEffect(() => {
    localStorage.setItem('sidebarExpanded', isSidebarExpanded.toString());
  }, [isSidebarExpanded]);

  React.useEffect(() => {
    try {
      localStorage.setItem(SIDEBAR_MODE_STORAGE_KEY, sidebarMode);
    } catch {
      // Ignore unavailable storage.
    }
  }, [sidebarMode]);

  React.useEffect(() => {
    try {
      localStorage.setItem(
        AGENT_SIDEBAR_WIDTH_STORAGE_KEY,
        agentSidebarWidth.toString()
      );
    } catch {
      // Ignore unavailable storage.
    }
  }, [agentSidebarWidth]);

  React.useEffect(() => {
    if (!config.agentEnabled && sidebarMode === 'agent') {
      setSidebarMode('navigation');
    }
  }, [config.agentEnabled, sidebarMode]);

  React.useEffect(() => {
    if (!isResizingAgentSidebar) {
      return undefined;
    }

    const handlePointerMove = (event: PointerEvent) => {
      setAgentSidebarWidth(clampAgentSidebarWidth(event.clientX));
    };
    const handlePointerUp = () => setIsResizingAgentSidebar(false);
    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;

    document.addEventListener('pointermove', handlePointerMove);
    document.addEventListener('pointerup', handlePointerUp);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    return () => {
      document.removeEventListener('pointermove', handlePointerMove);
      document.removeEventListener('pointerup', handlePointerUp);
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
    };
  }, [isResizingAgentSidebar]);

  React.useEffect(() => {
    const handleResize = () => {
      setAgentSidebarWidth((width) => clampAgentSidebarWidth(width));
    };

    window.addEventListener('resize', handleResize);
    return () => {
      window.removeEventListener('resize', handleResize);
    };
  }, []);

  const isDesignWorkspace =
    location.pathname === '/design' || location.pathname.startsWith('/design/');

  if (isDesignWorkspace) {
    return (
      <div className="h-screen w-full overflow-hidden bg-background">
        {children}
      </div>
    );
  }

  // Toggle sidebar function
  const toggleSidebar = () => {
    setIsSidebarExpanded(!isSidebarExpanded);
  };

  const openAgentSidebar = () => {
    if (config.agentEnabled) {
      setSidebarMode('agent');
    }
  };

  const closeAgentSidebar = () => {
    setSidebarMode('navigation');
  };

  const startAgentSidebarResize = (
    event: React.PointerEvent<HTMLDivElement>
  ) => {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    setIsResizingAgentSidebar(true);
  };

  const handleAgentSidebarResizeKeyDown = (
    event: React.KeyboardEvent<HTMLDivElement>
  ) => {
    const step = event.shiftKey ? 40 : 16;
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      setAgentSidebarWidth((width) => clampAgentSidebarWidth(width - step));
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      setAgentSidebarWidth((width) => clampAgentSidebarWidth(width + step));
    }
  };

  const desktopSidebarWidth = isAgentSidebarOpen
    ? agentSidebarWidth
    : isSidebarExpanded
      ? NAV_SIDEBAR_EXPANDED_WIDTH
      : NAV_SIDEBAR_COLLAPSED_WIDTH;
  const desktopSidebarStyle = {
    ...(isAgentSidebarOpen ? {} : sidebarStyle),
    width: desktopSidebarWidth,
    transition: isResizingAgentSidebar
      ? 'none'
      : 'width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
  } as React.CSSProperties;

  return (
    <div className="flex h-screen w-full overflow-hidden bg-background">
      {/* Sidebar - Desktop - Developer-tool */}
      <aside
        data-testid="app-sidebar"
        className={cn(
          'hidden md:block h-full shrink-0 border-r border-border z-20',
          isAgentSidebarOpen
            ? 'bg-card text-foreground'
            : [
                !hasCustomColor && 'bg-sidebar text-sidebar-foreground',
                hasCustomColor && 'custom-sidebar-color',
              ]
        )}
        style={desktopSidebarStyle}
      >
        <div className="flex flex-col h-full">
          {isAgentSidebarOpen ? (
            <AgentChatPanel
              active
              className="h-full"
              defaultSidebarOpen={false}
              onClose={closeAgentSidebar}
              placeholder="Ask me to create a DAG, run a command..."
            />
          ) : (
            <nav className="flex-1 overflow-y-auto min-h-0 px-2 py-3">
              <MainListItems
                isOpen={isSidebarExpanded}
                onAgentModeToggle={openAgentSidebar}
                onToggle={toggleSidebar}
                customColor={hasCustomColor}
              />
            </nav>
          )}
        </div>
      </aside>

      {isAgentSidebarOpen && (
        <div
          role="separator"
          aria-label="Resize agent panel"
          aria-orientation="vertical"
          aria-valuemin={AGENT_SIDEBAR_MIN_WIDTH}
          aria-valuemax={getAgentSidebarMaxWidth()}
          aria-valuenow={agentSidebarWidth}
          tabIndex={0}
          className={cn(
            'hidden md:flex h-full w-1 shrink-0 cursor-col-resize items-center justify-center z-20',
            'bg-border/30 transition-colors hover:bg-primary/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
            isResizingAgentSidebar && 'bg-primary/60'
          )}
          onPointerDown={startAgentSidebarResize}
          onKeyDown={handleAgentSidebarResizeKeyDown}
        >
          <div className="h-8 w-px rounded-full bg-muted-foreground/40" />
        </div>
      )}

      {/* Main Content Area - Developer-tool */}
      <div className="flex flex-col flex-1 h-full overflow-hidden relative bg-background">
        {/* Mobile Header Bar - Minimal Design */}
        <header
          className={cn(
            'md:hidden flex items-center justify-between h-14 px-4 flex-shrink-0 border-b border-border',
            !hasCustomColor && 'bg-background text-foreground',
            hasCustomColor && 'custom-sidebar-color'
          )}
          style={sidebarStyle}
        >
          <button
            className="p-2 rounded-md hover:bg-muted transition-colors"
            onClick={() => setIsMobileSidebarOpen(true)}
            aria-label="Open menu"
          >
            <Menu className="h-5 w-5" />
          </button>
          <span
            className={cn(
              'font-semibold tracking-tight whitespace-normal leading-tight text-center px-2',
              getResponsiveTitleClass(config.title || 'Dagu', 'header-mobile')
            )}
          >
            {config.title || 'Dagu'}
          </span>
          {config.agentEnabled ? (
            <button
              onClick={toggleChat}
              className="p-2 rounded-md hover:bg-muted transition-colors"
              aria-label="Agent Console"
            >
              <Terminal className="h-5 w-5" />
            </button>
          ) : (
            <div className="w-8" />
          )}
        </header>

        {/* Scrollable Content - More Compact Padding */}
        <main className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <ContentNavigation pathname={location.pathname} />
          <UpdateBanner />
          <LicenseBanner />
          <div className="min-h-0 flex-1 overflow-auto p-4 md:p-6 w-full">
            {children}
          </div>
        </main>
      </div>

      {/* Mobile Sidebar - Overlay - Developer-tool */}
      {isMobileSidebarOpen && (
        <div
          className="fixed inset-0 bg-background/60 z-50 md:hidden flex backdrop-blur-sm"
          onClick={() => setIsMobileSidebarOpen(false)}
        >
          <div
            className={cn(
              'h-full w-64 overflow-hidden shadow-lg border-r border-border',
              !hasCustomColor && 'bg-sidebar text-sidebar-foreground',
              hasCustomColor && 'custom-sidebar-color'
            )}
            style={sidebarStyle}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex justify-between items-center p-4 border-b border-sidebar-border">
              <span
                className={cn(
                  'font-semibold whitespace-normal leading-tight',
                  getResponsiveTitleClass(
                    config.title || 'Dagu',
                    'sidebar-mobile'
                  )
                )}
              >
                {config.title || 'Dagu'}
              </span>
              <button
                onClick={() => setIsMobileSidebarOpen(false)}
                className="p-1.5 hover:bg-sidebar-hover rounded-md transition-colors"
              >
                <X className="h-5 w-5" />
              </button>
            </div>
            <div className="flex flex-col h-full pt-2">
              <nav className="flex-1 overflow-y-auto min-h-0 px-2">
                <MainListItems
                  isOpen={true}
                  onNavItemClick={() => setIsMobileSidebarOpen(false)}
                  customColor={hasCustomColor}
                />
              </nav>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// Default export Layout component
export default function Layout({ navbarColor, children }: LayoutProps) {
  return <Content navbarColor={navbarColor}>{children}</Content>;
}
