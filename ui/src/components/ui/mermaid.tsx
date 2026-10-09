import mermaid from 'mermaid';
import React, { CSSProperties } from 'react';

type Props = {
  def: string;
  style?: CSSProperties;
  scale: number;
  nodeIds?: readonly string[];
  onClick?: (id: string) => void;
  onDoubleClick?: (id: string) => void;
  onRightClick?: (id: string) => void;
  onRender?: (container: HTMLDivElement) => void;
  fallback?: React.ReactNode;
  /** Receives the scrollable viewport that wraps the diagram. */
  viewportRef?: React.RefObject<HTMLDivElement | null>;
  /** Called after each render with the unscaled diagram size, or null when rendering fails. */
  onContentSize?: (size: DiagramSize | null) => void;
};

type DiagramSize = { width: number; height: number };

// Helper function to get computed CSS variable value with fallback
function getCSSVariable(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
  return value || fallback;
}

// Detect dark mode from Tailwind's .dark class on the root element
function isDarkMode(): boolean {
  if (typeof document === 'undefined') return false;
  return document.documentElement.classList.contains('dark');
}

// Initialize Mermaid with theme-aware colors
function initializeMermaid(): void {
  const dark = isDarkMode();
  mermaid.initialize({
    securityLevel: 'loose',
    startOnLoad: false,
    maxTextSize: 99999999,
    theme: dark ? 'dark' : 'default',
    themeVariables: {
      background: 'transparent',
      primaryColor: getCSSVariable('--card', dark ? '#181b22' : '#ffffff'),
      primaryTextColor: getCSSVariable(
        '--foreground',
        dark ? '#e5e7eb' : '#111827'
      ),
      primaryBorderColor: getCSSVariable(
        '--border',
        dark ? '#303746' : '#d7dde6'
      ),
      lineColor: getCSSVariable(
        '--muted-foreground',
        dark ? '#9ca3af' : '#64748b'
      ),
      sectionBkgColor: 'transparent',
      altSectionBkgColor: 'transparent',
      gridColor: 'transparent',
      secondaryColor: getCSSVariable(
        '--secondary',
        dark ? '#242936' : '#eef2f7'
      ),
      tertiaryColor: getCSSVariable(
        '--background',
        dark ? '#111318' : '#f5f7fb'
      ),
    },
    flowchart: {
      curve: 'basis',
      useMaxWidth: false,
      htmlLabels: true,
      nodeSpacing: 50,
      rankSpacing: 50,
    },
    gantt: {
      leftPadding: 150,
      gridLineStartPadding: 35,
      fontSize: 12,
      sectionFontSize: 14,
      numberSectionStyles: 2,
    },
    fontFamily: 'Arial',
    logLevel: 4, // ERROR
  });
}

function normalizeNodeIds(nodeIds?: readonly string[]): string[] {
  if (!nodeIds?.length) {
    return [];
  }
  return [...new Set(nodeIds)]
    .filter(Boolean)
    .sort((a, b) => b.length - a.length);
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function matchesRenderedNodeId(renderedId: string, nodeId: string): boolean {
  if (renderedId === nodeId) {
    return true;
  }

  // Dagu node ids use underscores internally, so only non-id characters can
  // delimit the node id inside Mermaid's generated SVG element id.
  const escapedNodeId = escapeRegExp(nodeId);
  return new RegExp(
    `(^|[^A-Za-z0-9_])${escapedNodeId}($|[^A-Za-z0-9_])`
  ).test(renderedId);
}

function resolveRenderedNodeId(
  renderedId: string,
  knownNodeIds: readonly string[]
): string | null {
  for (const nodeId of knownNodeIds) {
    if (matchesRenderedNodeId(renderedId, nodeId)) {
      return nodeId;
    }
  }

  // Backward compatibility for older callers that do not pass known ids.
  // Older Mermaid versions rendered ids as "flowchart-${nodeId}-${index}".
  if (knownNodeIds.length === 0) {
    return renderedId.split('-')[1] || null;
  }

  return null;
}

function findRenderedNodeElement(
  target: EventTarget | null,
  container: HTMLDivElement | null
): Element | null {
  if (!(target instanceof Element) || !container) {
    return null;
  }
  const nodeElement = target.closest('.node');
  return nodeElement && container.contains(nodeElement) ? nodeElement : null;
}

function applyNodeInteractionStyles(
  container: HTMLDivElement,
  knownNodeIds: readonly string[],
  hasNodeInteractions: boolean
): void {
  container.querySelectorAll('.node').forEach((node) => {
    const nodeElement = node as HTMLElement;
    const nodeId = resolveRenderedNodeId(node.id, knownNodeIds);
    if (hasNodeInteractions && nodeId) {
      nodeElement.style.cursor = 'pointer';
      nodeElement.style.userSelect = 'none';
    } else {
      nodeElement.style.removeProperty('cursor');
      nodeElement.style.removeProperty('user-select');
    }
  });
}

// Mermaid writes the padded drawing size into the viewBox; the width and
// height attributes are the fallback for SVGs without one.
function readSvgNaturalSize(svg: SVGSVGElement): DiagramSize | null {
  const viewBox = svg
    .getAttribute('viewBox')
    ?.trim()
    .split(/[\s,]+/);
  const [, , viewBoxWidth, viewBoxHeight] = (viewBox ?? []).map(Number);
  if (viewBoxWidth && viewBoxWidth > 0 && viewBoxHeight && viewBoxHeight > 0) {
    return { width: viewBoxWidth, height: viewBoxHeight };
  }
  const width = Number(svg.getAttribute('width'));
  const height = Number(svg.getAttribute('height'));
  return width > 0 && height > 0 ? { width, height } : null;
}

// Sizing the SVG element itself, rather than transforming it, keeps the
// scrollable area equal to the visible drawing.
function applySvgScale(
  svg: SVGSVGElement,
  size: DiagramSize | null,
  scale: number
): void {
  if (!size) {
    return;
  }
  svg.style.width = `${size.width * scale}px`;
  svg.style.height = `${size.height * scale}px`;
  svg.style.flex = 'none';
}

// Initialize on load
initializeMermaid();

function Mermaid({
  def,
  style = {},
  scale,
  nodeIds,
  onClick,
  onDoubleClick,
  onRightClick,
  onRender,
  fallback,
  viewportRef,
  onContentSize,
}: Props) {
  const mermaidRef = React.useRef<HTMLDivElement>(null); // Ref for the inner div holding the SVG
  const ownScrollContainerRef = React.useRef<HTMLDivElement>(null);
  const scrollContainerRef = viewportRef ?? ownScrollContainerRef; // Ref for the outer scrollable div
  const scrollPosRef = React.useRef({ top: 0, left: 0 }); // Ref to store scroll position
  const scaleRef = React.useRef(scale);
  const naturalSizeRef = React.useRef<DiagramSize | null>(null);
  const onContentSizeRef = React.useRef(onContentSize);
  const handlersRef = React.useRef({ onClick, onDoubleClick, onRightClick });
  const knownNodeIds = React.useMemo(() => normalizeNodeIds(nodeIds), [nodeIds]);
  const knownNodeIdsRef = React.useRef(knownNodeIds);
  const hasNodeInteractions = Boolean(onClick || onDoubleClick || onRightClick);
  const hasNodeInteractionsRef = React.useRef(hasNodeInteractions);
  const renderGenerationRef = React.useRef(0);
  const clickTimeoutsRef = React.useRef<
    Map<string, ReturnType<typeof setTimeout>>
  >(new Map()); // Persistent timeout storage
  const [uniqueId] = React.useState(
    () => `mermaid-${Math.random().toString(36).substr(2, 9)}`
  );
  const [renderError, setRenderError] = React.useState<string | null>(null);

  // Extract background-related styles for the scroll container
  const { background, backgroundSize, backgroundColor, ...contentStyle } =
    style;

  const dStyle: CSSProperties = {
    overflow: 'auto',
    position: 'relative',
    // Removed hardcoded maxHeight to allow parent flex containers or explicit heights to work correctly
    // Apply background styles to the scroll container so grid scrolls with content
    background,
    backgroundSize,
    backgroundAttachment: 'local', // Makes background scroll with content
    backgroundColor,
    height: '100%', // Ensure it takes full height of parent
  };

  const mStyle: CSSProperties = {
    padding: '2em',
    ...contentStyle,
    // Grow with the drawing so the viewport scrolls over all of it, and fill
    // the viewport when the drawing is smaller.
    width: 'max-content',
    minWidth: '100%',
    minHeight: '100%',
  };

  React.useEffect(() => {
    handlersRef.current = { onClick, onDoubleClick, onRightClick };
  }, [onClick, onDoubleClick, onRightClick]);

  React.useEffect(() => {
    onContentSizeRef.current = onContentSize;
  }, [onContentSize]);

  React.useEffect(() => {
    knownNodeIdsRef.current = knownNodeIds;
  }, [knownNodeIds]);

  React.useEffect(() => {
    hasNodeInteractionsRef.current = hasNodeInteractions;
  }, [hasNodeInteractions]);

  const resolveEventNodeId = React.useCallback(
    (target: EventTarget | null): string | null => {
      const nodeElement = findRenderedNodeElement(target, mermaidRef.current);
      if (!nodeElement) {
        return null;
      }
      return resolveRenderedNodeId(nodeElement.id, knownNodeIdsRef.current);
    },
    []
  );

  const clearPendingClick = React.useCallback((nodeId: string) => {
    const existingTimeout = clickTimeoutsRef.current.get(nodeId);
    if (existingTimeout) {
      clearTimeout(existingTimeout);
      clickTimeoutsRef.current.delete(nodeId);
    }
  }, []);

  const handleNodeClick = React.useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      if (!handlersRef.current.onClick) {
        return;
      }
      const nodeId = resolveEventNodeId(event.target);
      if (!nodeId) {
        return;
      }
      clearPendingClick(nodeId);
      const timeout = setTimeout(() => {
        clickTimeoutsRef.current.delete(nodeId);
        handlersRef.current.onClick?.(nodeId);
      }, 250);
      clickTimeoutsRef.current.set(nodeId, timeout);
    },
    [clearPendingClick, resolveEventNodeId]
  );

  const handleNodeDoubleClick = React.useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      if (!handlersRef.current.onDoubleClick) {
        return;
      }
      const nodeId = resolveEventNodeId(event.target);
      if (!nodeId) {
        return;
      }
      event.stopPropagation();
      clearPendingClick(nodeId);
      handlersRef.current.onDoubleClick(nodeId);
    },
    [clearPendingClick, resolveEventNodeId]
  );

  const handleNodeRightClick = React.useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      if (!handlersRef.current.onRightClick) {
        return;
      }
      const nodeId = resolveEventNodeId(event.target);
      if (!nodeId) {
        return;
      }
      event.preventDefault();
      clearPendingClick(nodeId);
      handlersRef.current.onRightClick(nodeId);
    },
    [clearPendingClick, resolveEventNodeId]
  );

  const render = async () => {
    const renderGeneration = ++renderGenerationRef.current;
    if (!mermaidRef.current) {
      return;
    }
    if (def.startsWith('<')) {
      console.error('invalid definition!!');
      return;
    }

    // Hold the current height while the old SVG is gone and the new one is
    // not ready, so content below the graph does not collapse and jump.
    const releaseHeight = () => {
      if (mermaidRef.current) {
        mermaidRef.current.style.minHeight = String(mStyle.minHeight ?? '');
      }
    };
    mermaidRef.current.style.minHeight = `${mermaidRef.current.offsetHeight}px`;

    try {
      setRenderError(null);
      // Reinitialize Mermaid to pick up current theme
      initializeMermaid();

      // Clear previous content
      mermaidRef.current.innerHTML = '';

      // Generate SVG
      const { svg, bindFunctions } = await mermaid.render(uniqueId, def);

      if (
        renderGeneration !== renderGenerationRef.current ||
        !mermaidRef.current
      ) {
        return;
      }

      mermaidRef.current.innerHTML = svg;
      releaseHeight();
      onRender?.(mermaidRef.current);
      applyNodeInteractionStyles(
        mermaidRef.current,
        knownNodeIdsRef.current,
        hasNodeInteractionsRef.current
      );

      // The scale may have changed while this render was in flight.
      const svgEl = mermaidRef.current.querySelector('svg');
      naturalSizeRef.current = svgEl ? readSvgNaturalSize(svgEl) : null;
      if (svgEl) {
        applySvgScale(svgEl, naturalSizeRef.current, scaleRef.current);
      }

      // Restore scroll position *after* SVG is rendered
      if (scrollContainerRef.current) {
        scrollContainerRef.current.scrollTop = scrollPosRef.current.top;
        scrollContainerRef.current.scrollLeft = scrollPosRef.current.left;
      }
      onContentSizeRef.current?.(naturalSizeRef.current);

      // Bind standard Mermaid event handlers
      // This is still needed for other functionality
      setTimeout(() => {
        if (
          renderGeneration === renderGenerationRef.current &&
          mermaidRef.current &&
          bindFunctions
        ) {
          bindFunctions(mermaidRef.current);
        }
      }, 100); // Reduced timeout slightly
    } catch (error: unknown) {
      if (
        renderGeneration !== renderGenerationRef.current ||
        !mermaidRef.current
      ) {
        return;
      }
      console.error('Mermaid render error:', error);
      setRenderError(String(error));
      releaseHeight();
      if (mermaidRef.current) {
        mermaidRef.current.innerHTML = '';
      }
      naturalSizeRef.current = null;
      onContentSizeRef.current?.(null);
    }
  };

  React.useEffect(() => {
    // Save scroll position before re-rendering
    if (scrollContainerRef.current) {
      scrollPosRef.current = {
        top: scrollContainerRef.current.scrollTop,
        left: scrollContainerRef.current.scrollLeft,
      };
    }
    render();
  }, [def, onRender]); // Re-render when the definition or SVG post-processing changes

  React.useEffect(() => {
    if (!mermaidRef.current) {
      return;
    }
    applyNodeInteractionStyles(
      mermaidRef.current,
      knownNodeIds,
      hasNodeInteractions
    );
  }, [knownNodeIds, hasNodeInteractions]);

  // Layout effect so the new size is in place before an owner adjusts scroll
  // after a zoom.
  React.useLayoutEffect(() => {
    scaleRef.current = scale;
    const svg = mermaidRef.current?.querySelector('svg');
    if (svg) {
      applySvgScale(svg, naturalSizeRef.current, scale);
    }
  }, [scale]);

  // Cleanup timeouts on unmount or when def changes
  React.useEffect(() => {
    return () => {
      clickTimeoutsRef.current.forEach((timeout) => clearTimeout(timeout));
      clickTimeoutsRef.current.clear();
    };
  }, [def]);

  return (
    // Attach ref to the scrollable container
    <div ref={scrollContainerRef} style={dStyle}>
      <div
        className="mermaid no-text-select"
        onClick={handleNodeClick}
        onContextMenu={handleNodeRightClick}
        onDoubleClick={handleNodeDoubleClick}
        ref={mermaidRef} // Keep ref for mermaid rendering target
        style={{
          ...mStyle,
          display: renderError ? 'none' : mStyle.display,
          // Remove overflow from inner div, let outer div handle it
          // overflow: 'auto',
          // maxHeight: '80vh', // Max height is now on the outer div
        }}
      />
      {renderError && fallback}
    </div>
  );
}

export default Mermaid;
