// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Activity, FileCode, Workflow, type LucideIcon } from 'lucide-react';
import { useCloseRightPanel } from '@/contexts/RightPanelContext';
import { useI18n } from '@/i18n/I18nProvider';
import { cn, FOCUS_RING } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { DOCK_PANEL_ID } from './DockTabStrip';
import { useDock } from './DockContext';

type PhoneView = 'graph' | 'activity' | 'yaml';

function ViewButton({
  icon: Icon,
  label,
  active,
  disabled,
  onClick,
  ...aria
}: {
  icon: LucideIcon;
  label: string;
  active: boolean;
  disabled?: boolean;
  onClick(): void;
  'aria-pressed'?: boolean;
  'aria-expanded'?: boolean;
  'aria-controls'?: string;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      {...aria}
      className={cn(
        'relative flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 text-[11px] font-medium disabled:opacity-50',
        active ? 'text-foreground' : 'text-muted-foreground',
        FOCUS_RING
      )}
    >
      {active && (
        <span
          aria-hidden="true"
          className="absolute inset-x-6 top-0 h-0.5 rounded-full bg-primary"
        />
      )}
      <Icon className="size-5" aria-hidden="true" />
      <span className="truncate">{label}</span>
    </button>
  );
}

/**
 * The views of the workflow page on a phone, as the bar at the bottom of the
 * Run panel: the graph, the Activity panel at full height and the YAML. One
 * shows at a time. Activity is the toggle of the panel, as on a wider page:
 * a second tap goes back to the graph.
 */
export function PhoneViewNav() {
  const { ts } = useI18n();
  const dock = useDock();
  const [url, setUrl] = useWorkspaceUrl();
  const { handle } = useWorkspaceEditor();
  const closePanel = useCloseRightPanel();
  const yamlOpen = url.ndv === 'docked';
  const view: PhoneView = dock.open ? 'activity' : yamlOpen ? 'yaml' : 'graph';

  // The YAML closes through ?ndv, the state of the panel: the editor may
  // not have rendered the URL that opened it yet.
  const closeYaml = () => {
    if (yamlOpen) setUrl({ ndv: null }, { replace: true });
  };
  const show = (next: PhoneView) => {
    if (next === 'activity') {
      if (!dock.open) closeYaml();
      dock.toggle();
      return;
    }
    if (next === view) return;
    if (dock.open) dock.toggle();
    if (next === 'yaml') {
      handle?.toggleYaml(true);
      return;
    }
    closeYaml();
    // A step panel or the Workflow panel covers the graph too.
    closePanel();
  };

  return (
    <nav
      aria-label={ts('Workflow views')}
      data-testid="ww-phone-nav"
      className="flex h-12 shrink-0 border-t border-border bg-card pb-[env(safe-area-inset-bottom)]"
    >
      <ViewButton
        icon={Workflow}
        label={ts('Graph')}
        active={view === 'graph'}
        aria-pressed={view === 'graph'}
        onClick={() => show('graph')}
      />
      <ViewButton
        icon={Activity}
        label={ts('Activity')}
        active={view === 'activity'}
        aria-expanded={dock.open}
        aria-controls={DOCK_PANEL_ID}
        onClick={() => show('activity')}
      />
      <ViewButton
        icon={FileCode}
        label={ts('YAML')}
        active={view === 'yaml'}
        aria-pressed={view === 'yaml'}
        disabled={!handle}
        onClick={() => show('yaml')}
      />
    </nav>
  );
}
