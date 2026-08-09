import React, { useCallback, useEffect, useRef, useState } from 'react';
import { readMigratedLocalStorage } from '@/lib/local-storage-migration';

// Context to provide panel width to children
export const PanelWidthContext = React.createContext<number | null>(null);

interface SplitLayoutProps {
  leftPanel: React.ReactNode;
  rightPanel: React.ReactNode | null;
  defaultLeftWidth?: number; // percentage (0-100)
  minLeftWidth?: number; // percentage
  maxLeftWidth?: number; // percentage
  storageKey?: string; // localStorage key for persistence
  legacyStorageKey?: string;
  emptyRightMessage?: string;
}

/**
 * SplitLayout component provides a master-detail view with
 * a list on the left and details on the right.
 * The divider is draggable and position is saved to localStorage.
 *
 * On mobile (<768px), only the left panel is shown and
 * detail navigation should happen via full-page routing.
 */
function SplitLayout({
  leftPanel,
  rightPanel,
  defaultLeftWidth = 40,
  minLeftWidth = 20,
  maxLeftWidth = 70,
  storageKey = 'splitLayoutWidth',
  legacyStorageKey,
  emptyRightMessage = 'Select an item to view details',
}: SplitLayoutProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const leftPanelRef = useRef<HTMLDivElement>(null);
  const [leftWidth, setLeftWidth] = useState<number>(() => {
    const saved = legacyStorageKey
      ? readMigratedLocalStorage(storageKey, legacyStorageKey)
      : localStorage.getItem(storageKey);
    if (saved) {
      const parsed = parseFloat(saved);
      if (!isNaN(parsed) && parsed >= minLeftWidth && parsed <= maxLeftWidth) {
        return parsed;
      }
    }
    return defaultLeftWidth;
  });
  const [isDragging, setIsDragging] = useState(false);
  const [leftPanelPixelWidth, setLeftPanelPixelWidth] = useState<number | null>(
    null
  );

  // Track actual pixel width of left panel
  useEffect(() => {
    if (!leftPanelRef.current) return;

    const updateWidth = () => {
      if (leftPanelRef.current) {
        setLeftPanelPixelWidth(leftPanelRef.current.offsetWidth);
      }
    };

    // Initial measurement
    updateWidth();

    // Use ResizeObserver to track size changes
    const resizeObserver = new ResizeObserver(updateWidth);
    resizeObserver.observe(leftPanelRef.current);

    return () => resizeObserver.disconnect();
  }, []);

  // Save to localStorage when width changes
  useEffect(() => {
    localStorage.setItem(storageKey, leftWidth.toString());
  }, [leftWidth, storageKey]);

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setIsDragging(true);
  }, []);

  const handleMouseMove = useCallback(
    (e: MouseEvent) => {
      if (!isDragging || !containerRef.current) return;

      const containerRect = containerRef.current.getBoundingClientRect();
      const newWidth =
        ((e.clientX - containerRect.left) / containerRect.width) * 100;

      // Clamp to min/max
      const clampedWidth = Math.min(
        maxLeftWidth,
        Math.max(minLeftWidth, newWidth)
      );
      setLeftWidth(clampedWidth);
    },
    [isDragging, minLeftWidth, maxLeftWidth]
  );

  const handleMouseUp = useCallback(() => {
    setIsDragging(false);
  }, []);

  // Add/remove global mouse listeners when dragging
  useEffect(() => {
    if (isDragging) {
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    } else {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    }

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
  }, [isDragging, handleMouseMove, handleMouseUp]);

  return (
    <div ref={containerRef} className="flex h-full overflow-hidden">
      {/* Left Panel - Full width on mobile, percentage width on desktop */}
      <div
        ref={leftPanelRef}
        className="h-full overflow-y-auto overflow-x-hidden flex-shrink-0 w-full md:w-[var(--left-width)]"
        style={{ '--left-width': `${leftWidth}%` } as React.CSSProperties}
      >
        <PanelWidthContext.Provider value={leftPanelPixelWidth}>
          {leftPanel}
        </PanelWidthContext.Provider>
      </div>

      {/* Draggable Divider - Hidden on mobile */}
      <div
        className={`hidden md:flex items-center justify-center w-[1px] h-full bg-border/20 hover:bg-primary/50 hover:shadow-[0_0_10px_var(--color-primary)] cursor-col-resize flex-shrink-0 transition-all duration-300 group z-30 ${
          isDragging ? 'bg-primary shadow-[0_0_15px_var(--color-primary)]' : ''
        }`}
        onMouseDown={handleMouseDown}
      >
        <div
          className={`w-1 h-8 rounded-full transition-all duration-300 ${
            isDragging
              ? 'bg-primary shadow-[0_0_10px_var(--color-primary)] scale-110'
              : 'bg-muted-foreground/30 group-hover:bg-primary group-hover:shadow-[0_0_8px_var(--color-primary)]'
          }`}
        />
      </div>

      {/* Right Panel - Details (hidden on mobile) */}
      <div className="hidden md:flex flex-1 h-full overflow-hidden min-w-0">
        {rightPanel ? (
          <div className="flex flex-col w-full h-full overflow-hidden bg-background">
            {rightPanel}
          </div>
        ) : (
          <div className="flex items-center justify-center w-full h-full bg-muted/20">
            <p className="text-muted-foreground text-sm">{emptyRightMessage}</p>
          </div>
        )}
      </div>
    </div>
  );
}

export default SplitLayout;
