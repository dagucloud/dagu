// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { LicenseBanner } from '@/components/LicenseBanner';
import { UpdateBanner } from '@/components/UpdateBanner';
import { useConfig } from '@/contexts/ConfigContext';
import { cn } from '@/lib/utils';
import { getResponsiveTitleClass } from '@/lib/text-utils';
import { Menu, X } from 'lucide-react';
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

type LayoutProps = {
  navbarColor?: string;
  children?: React.ReactElement | React.ReactElement[];
};

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
  // Mobile sidebar state (hidden by default)
  const [isMobileSidebarOpen, setIsMobileSidebarOpen] = React.useState(false);

  // Save sidebar state to localStorage when it changes
  React.useEffect(() => {
    localStorage.setItem('sidebarExpanded', isSidebarExpanded.toString());
  }, [isSidebarExpanded]);

  // Toggle sidebar function
  const toggleSidebar = () => {
    setIsSidebarExpanded(!isSidebarExpanded);
  };

  const desktopSidebarWidth = isSidebarExpanded
    ? NAV_SIDEBAR_EXPANDED_WIDTH
    : NAV_SIDEBAR_COLLAPSED_WIDTH;
  const desktopSidebarStyle = {
    ...sidebarStyle,
    width: desktopSidebarWidth,
    transition: 'width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
  } as React.CSSProperties;

  return (
    <div className="flex h-screen w-full overflow-hidden bg-background">
      {/* Sidebar - Desktop - Developer-tool */}
      <aside
        data-testid="app-sidebar"
        className={cn(
          'hidden md:block h-full shrink-0 border-r border-border z-20',
          !hasCustomColor && 'bg-sidebar text-sidebar-foreground',
          hasCustomColor && 'custom-sidebar-color'
        )}
        style={desktopSidebarStyle}
      >
        <div className="flex flex-col h-full">
          <nav className="flex-1 overflow-y-auto min-h-0 px-2 py-3">
            <MainListItems
              isOpen={isSidebarExpanded}
              onToggle={toggleSidebar}
              customColor={hasCustomColor}
            />
          </nav>
        </div>
      </aside>

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
          <div className="w-8" />
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
