// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useLocation } from 'react-router-dom';
import LoadingIndicator from '@/components/ui/loading-indicator';
import {
  PANEL_HEADER_CLASS,
  PANEL_ZONE_SHELL_CLASS,
  panelMaxWidth,
} from '@/features/dags/components/step-details/PanelShell';
import { getStoredDrawerWidth } from '@/features/dags/components/step-details/StepSideDrawer';
import { TOOLBAR_ZONE_CLASS } from '@/features/visual-editor/canvas/toolbarHost';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import {
  workflowPanelWasOpen,
  yamlPanelWasOpen,
} from './canvas/useYamlPanelMemory';
import { storedDockHeight, storedDockOpen } from './dock/DockContext';
import {
  MAIN_ZONE_CLASS,
  MAIN_ZONE_FLOOR_CLASS,
  ROOT_CLASS,
} from './layout/WorkspaceLayout';
import { isRunRedirect, parseWorkspaceUrl } from './state/workspaceUrl';

// The dots of the canvas at 100 %.
const CANVAS_DOTS_CLASS =
  'pointer-events-none absolute inset-0 text-border bg-[radial-gradient(circle,currentColor_1px,transparent_1.5px)] bg-size-[16px_16px]';

const WORKFLOW_TAB = /^\/dags\/[^/]+\/([^/]+)/;

/**
 * Which panels the page shows at its first render, by the rules of the
 * page: the YAML panel by ?ndv or as the user left it, else the Workflow
 * panel as the user left it (useYamlPanelMemory), and the Run panel by
 * ?dock, a ?run of a link or as the user left it (DockProvider). The Run
 * panel yields to the right panel and to the step view.
 */
function panelsAtLoad(
  pathname: string,
  search: string,
  routeState: unknown
): { right: 'yaml' | 'workflow' | null; dock: boolean } {
  const tab = WORKFLOW_TAB.exec(pathname)?.[1];
  const query = new URLSearchParams(search);
  const url = parseWorkspaceUrl(tab, query);
  const restorable =
    url.ndv === null &&
    url.step === null &&
    url.panel === null &&
    url.dock === null &&
    url.def === null &&
    tab === undefined &&
    !query.has('yaml');
  const right =
    url.ndv === 'docked' || (restorable && yamlPanelWasOpen())
      ? 'yaml'
      : restorable && workflowPanelWasOpen()
        ? 'workflow'
        : null;
  const stepView = url.ndv === 'expanded' && url.step !== null;
  const dock =
    right === null &&
    !stepView &&
    (url.dock !== null ||
      (url.run !== null && !isRunRedirect(routeState)) ||
      storedDockOpen());
  return { right, dock };
}

/**
 * What the workflow page shows until its data is there: its frame, with each
 * part in the place and at the size of the page. The bar holds the name of
 * the workflow, the zone the dots of the canvas, and the right panel and
 * the Run panel show as the page will show them, so that the page fills the
 * frame in and nothing moves. A load that takes long shows the loader in the
 * free part of the zone. `url` is the address of the page when it is not the
 * current one yet, as for a workflow that opens over a list. It is the
 * fallback of the route too, so the two loading states look the same.
 */
export function WorkspaceLoading({
  fileName,
  url,
}: {
  fileName: string;
  url?: string;
}) {
  const { ts } = useI18n();
  const location = useLocation();
  const target = url ? new URL(url, window.location.origin) : location;
  const { right, dock } = panelsAtLoad(
    target.pathname,
    target.search,
    url ? null : location.state
  );
  const panelWidth = getStoredDrawerWidth();
  return (
    <div
      role="status"
      aria-label={ts('Loading...')}
      data-testid="ww-loading"
      className={ROOT_CLASS}
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-border px-3">
        {/* The place of the pin button of the bar. */}
        <span aria-hidden="true" className="hidden size-7 shrink-0 md:block" />
        <h1 className="min-w-0 truncate text-base font-semibold tracking-[-0.01em]">
          {fileName}
        </h1>
      </div>
      <div
        className={cn(
          MAIN_ZONE_CLASS,
          MAIN_ZONE_FLOOR_CLASS,
          TOOLBAR_ZONE_CLASS,
          'relative'
        )}
      >
        <div aria-hidden="true" className={CANVAS_DOTS_CLASS} />
        <div
          className="relative flex min-h-0 flex-1"
          style={right ? { marginRight: panelWidth } : undefined}
        >
          <LoadingIndicator fill delayed />
        </div>
        {right && (
          <div
            aria-hidden="true"
            data-testid={`ww-loading-${right}`}
            className={PANEL_ZONE_SHELL_CLASS}
            style={{ width: `${panelWidth}px`, maxWidth: panelMaxWidth() }}
          >
            <div className={PANEL_HEADER_CLASS}>
              <span className="truncate text-base font-semibold text-foreground">
                {right === 'yaml' ? ts('YAML') : ts('Workflow')}
              </span>
            </div>
          </div>
        )}
      </div>
      {dock && (
        <div
          aria-hidden="true"
          data-testid="ww-loading-dock"
          className="relative min-h-8 shrink bg-card"
          style={{ height: storedDockHeight() }}
        >
          <div className="h-8 border-t border-border" />
        </div>
      )}
    </div>
  );
}
