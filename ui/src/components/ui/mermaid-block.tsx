import { useEffect, useId, useRef, useState } from 'react';

type MermaidApi = (typeof import('mermaid'))['default'];

// Mermaid is large: it loads on the first diagram, not with the page bundle.
let mermaidLoader: Promise<MermaidApi> | null = null;

function loadMermaid(): Promise<MermaidApi> {
  if (!mermaidLoader) {
    mermaidLoader = import('mermaid').then((module) => module.default);
    // A failed load is not cached, so the next diagram tries again.
    mermaidLoader.catch(() => {
      mermaidLoader = null;
    });
  }
  return mermaidLoader;
}

// Helper function to get computed CSS variable value with fallback
function getCSSVariable(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
  return value || fallback;
}

function isDarkMode(): boolean {
  if (typeof document === 'undefined') return false;
  return document.documentElement.classList.contains('dark');
}

function initializeMermaid(mermaid: MermaidApi): void {
  const dark = isDarkMode();
  mermaid.initialize({
    securityLevel: 'loose',
    startOnLoad: false,
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
      secondaryColor: getCSSVariable(
        '--secondary',
        dark ? '#242936' : '#eef2f7'
      ),
      tertiaryColor: getCSSVariable(
        '--background',
        dark ? '#111318' : '#f5f7fb'
      ),
    },
    fontFamily: 'Arial',
    logLevel: 4,
  });
}

let mermaidIdCounter = 0;

interface MermaidBlockProps {
  code: string;
}

export function MermaidBlock({ code }: MermaidBlockProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  const idPrefix = useId().replace(/:/g, '');

  useEffect(() => {
    let cancelled = false;

    async function render() {
      if (!containerRef.current) return;

      // Generate a unique ID per render call to avoid mermaid's internal
      // diagram cache conflicts when the code or diagram type changes.
      const renderId = `mermaid-block-${idPrefix}-${mermaidIdCounter++}`;

      try {
        const mermaid = await loadMermaid();
        if (cancelled) return;
        initializeMermaid(mermaid);
        const { svg } = await mermaid.render(renderId, code.trim());
        if (!cancelled && containerRef.current) {
          containerRef.current.innerHTML = svg;
          setError(null);
        }
      } catch (err) {
        // Mermaid injects error elements into the document body on parse
        // failures. Remove them to prevent layout pollution.
        document.getElementById(renderId)?.remove();

        if (!cancelled) {
          setError(String(err));
        }
      }
    }

    render();
    return () => {
      cancelled = true;
    };
  }, [code, idPrefix]);

  // The container stays mounted under the fallback so that a later code
  // change can render into it again.
  return (
    <>
      {error && (
        <pre className="text-xs p-2 rounded bg-muted overflow-x-auto font-mono">
          <code>{code}</code>
        </pre>
      )}
      <div
        ref={containerRef}
        hidden={error !== null}
        className="my-1 overflow-x-auto [&>svg]:max-w-full"
      />
    </>
  );
}
