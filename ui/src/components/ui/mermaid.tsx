import mermaid from 'mermaid';
import React, { CSSProperties } from 'react';

type Props = {
  def: string;
  style?: CSSProperties;
  scale: number;
  onClick?: (id: string) => void;
  onDoubleClick?: (id: string) => void;
  onRightClick?: (id: string) => void;
  onRender?: (container: HTMLDivElement) => void;
  fallback?: React.ReactNode;
};

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

// Initialize on load
initializeMermaid();

function Mermaid({
  def,
  style = {},
  scale,
  onClick,
  onDoubleClick,
  onRightClick,
  onRender,
  fallback,
}: Props) {
  const mermaidRef = React.useRef<HTMLDivElement>(null); // Ref for the inner div holding the SVG
  const scrollContainerRef = React.useRef<HTMLDivElement>(null); // Ref for the outer scrollable div
  const scrollPosRef = React.useRef({ top: 0, left: 0 }); // Ref to store scroll position
  const handlersRef = React.useRef({ onClick, onDoubleClick, onRightClick });
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
    ...contentStyle,
    padding: '2em',
    minHeight: '100%', // Ensure inner div also takes full height
  };

  React.useEffect(() => {
    handlersRef.current = { onClick, onDoubleClick, onRightClick };
  }, [onClick, onDoubleClick, onRightClick]);

  const render = async () => {
    const renderGeneration = ++renderGenerationRef.current;
    if (!mermaidRef.current) {
      return;
    }
    if (def.startsWith('<')) {
      console.error('invalid definition!!');
      return;
    }

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
      onRender?.(mermaidRef.current);

      // Apply scale transform immediately after SVG is rendered
      const svgEl = mermaidRef.current.querySelector('svg');
      if (svgEl) {
        svgEl.style.overflow = 'visible';
        svgEl.style.transform = `scale(${scale})`;
        svgEl.style.transformOrigin = 'top left';

        // Adjust the SVG's wrapper div to account for the scale
        // This ensures the horizontal scrollbar properly reflects the scaled size
        const parent = svgEl.parentElement;
        if (parent && scale !== 1) {
          const bbox = svgEl.getBBox();
          parent.style.width = `${bbox.width * scale}px`;
          parent.style.height = `${bbox.height * scale}px`;
        } else if (parent && scale === 1) {
          // Reset to auto when scale is 1
          parent.style.width = 'auto';
          parent.style.height = 'auto';
        }
      }

      // Restore scroll position *after* SVG is rendered
      if (scrollContainerRef.current) {
        scrollContainerRef.current.scrollTop = scrollPosRef.current.top;
        scrollContainerRef.current.scrollLeft = scrollPosRef.current.left;
      }

      // Attach custom event handlers if provided
      if ((onClick || onDoubleClick || onRightClick) && mermaidRef.current) {
        // Clear existing timeouts before setting up new handlers
        clickTimeoutsRef.current.forEach((timeout) => clearTimeout(timeout));
        clickTimeoutsRef.current.clear();

        // Find all nodes in the SVG (typically these are <g> elements with class="node")
        const nodeElements = mermaidRef.current.querySelectorAll('.node');

        nodeElements.forEach((node) => {
          // Extract the node ID from the element
          // The ID is typically in the format "flowchart-nodeId-number"
          const nodeId = node.id.split('-')[1];

          if (nodeId) {
            // Attach single-click event listener if provided
            if (onClick) {
              node.addEventListener('click', () => {
                // Clear any existing timeout for this node
                const existingTimeout = clickTimeoutsRef.current.get(nodeId);
                if (existingTimeout) {
                  clearTimeout(existingTimeout);
                }
                // Set timeout to allow double-click to cancel
                const timeout = setTimeout(() => {
                  clickTimeoutsRef.current.delete(nodeId);
                  handlersRef.current.onClick?.(nodeId);
                }, 250);
                clickTimeoutsRef.current.set(nodeId, timeout);
              });
            }

            // Attach double-click event listener if provided
            if (onDoubleClick) {
              node.addEventListener('dblclick', (event) => {
                event.stopPropagation();
                // Cancel pending single-click action
                const existingTimeout = clickTimeoutsRef.current.get(nodeId);
                if (existingTimeout) {
                  clearTimeout(existingTimeout);
                  clickTimeoutsRef.current.delete(nodeId);
                }
                handlersRef.current.onDoubleClick?.(nodeId);
              });
            }

            // Attach right-click (contextmenu) event listener if provided
            if (onRightClick) {
              node.addEventListener('contextmenu', (event) => {
                event.preventDefault(); // Prevent default context menu
                // Also cancel pending single-click
                const existingTimeout = clickTimeoutsRef.current.get(nodeId);
                if (existingTimeout) {
                  clearTimeout(existingTimeout);
                  clickTimeoutsRef.current.delete(nodeId);
                }
                handlersRef.current.onRightClick?.(nodeId);
              });
            }

            // Add pointer cursor and disable text selection
            const nodeElement = node as HTMLElement;
            nodeElement.style.cursor = 'pointer';
            nodeElement.style.userSelect = 'none';
          }
        });
      }

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
      if (mermaidRef.current) {
        mermaidRef.current.innerHTML = '';
      }
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
    // Apply scale transformation when scale prop changes
    if (mermaidRef.current) {
      const svg = mermaidRef.current.querySelector('svg');
      if (svg) {
        // Ensure the SVG itself doesn't cause overflow issues conflicting with the container
        svg.style.overflow = 'visible';
        svg.style.transform = `scale(${scale})`;
        svg.style.transformOrigin = 'top left'; // Keep origin consistent

        // Adjust the SVG's wrapper div to account for the scale
        // This ensures the horizontal scrollbar properly reflects the scaled size
        const parent = svg.parentElement;
        if (parent && scale !== 1) {
          const bbox = svg.getBBox();
          parent.style.width = `${bbox.width * scale}px`;
          parent.style.height = `${bbox.height * scale}px`;
        } else if (parent && scale === 1) {
          // Reset to auto when scale is 1
          parent.style.width = 'auto';
          parent.style.height = 'auto';
        }
      }
    }
  }, [scale]); // Apply scale separately

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

export default React.memo(Mermaid, (prev, next) => {
  return (
    prev.def === next.def &&
    prev.scale === next.scale &&
    prev.onRender === next.onRender
  );
});
