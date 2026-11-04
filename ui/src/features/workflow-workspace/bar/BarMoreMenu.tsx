// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Copy,
  FileCode,
  Link2,
  MoreHorizontal,
  PanelRight,
  Pin,
  RefreshCw,
  Trash2,
  Webhook,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { setDAGPinned } from '@/features/dags/lib/dagPins';
import { useDAGEditActions } from '@/features/dags/components/dag-editor/DAGEditButtons';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { copyText } from '@/lib/clipboard';
import { cn } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceRefresh } from '../data';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useSettingsLeaveGuard } from '../settings/useSettingsLeaveGuard';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { Z } from '../zLayers';
import { usePinChanged } from './DAGIdentity';
import { useWorkflowShare } from './useWorkflowShare';

const menuIcon = 'size-4 shrink-0';

/**
 * The controls that a narrow bar hides, in one menu: the copy of the name
 * and of the link, the pin, Refresh, the Workflow panel with the labels and
 * the description, the webhook, and Delete below md. A bar of 672px in a
 * window of md or more shows them all and has no menu.
 */
export function BarMoreMenu(props: BarSlotProps & { pinned?: boolean }) {
  const { ts } = useI18n();
  // The menu closes when Delete is chosen: its dialog lives outside it.
  const edit = useDAGEditActions(props.fileName, props.workspaceName);
  return (
    // The pin, Refresh and the webhook hide below md, the copy buttons and
    // the labels in a bar under 672px: the menu shows while either does.
    <div data-testid="ww-bar-more" className="flex shrink-0 md:@2xl:hidden">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={ts('More actions')}
            title={ts('More actions')}
          >
            <MoreHorizontal aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className={cn(Z.menu, 'w-56')}>
          <MoreMenuItems
            {...props}
            onDelete={edit.canWrite ? edit.openDelete : undefined}
          />
        </DropdownMenuContent>
      </DropdownMenu>
      {edit.canWrite && edit.dialogs}
    </div>
  );
}

/** The items of the menu; they mount, and read what they need, when it opens. */
function MoreMenuItems({
  fileName,
  dag,
  pinned,
  onDelete,
}: BarSlotProps & { pinned?: boolean; onDelete?: () => void }) {
  const { ts } = useI18n();
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  const { canWrite: canPin, canManageWebhooks } = useWorkspacePermissions();
  const { handle } = useWorkspaceEditor();
  const [, setUrl] = useWorkspaceUrl();
  const { guard } = useSettingsLeaveGuard();
  const refresh = useWorkspaceRefresh();
  const onPinChanged = usePinChanged();
  const { getPageLink } = useWorkflowShare(fileName);
  const { showToast } = useSimpleToast();
  const name = dag.name;

  // The menu closes on the choice: a copy that fails says so. A success
  // shows nothing, as everywhere in the app.
  const copy = async (text: string) => {
    if (!(await copyText(text))) {
      showToast(ts('Copy failed'), { variant: 'error' });
    }
  };

  const togglePin = async () => {
    try {
      await setDAGPinned(client, { fileName, pinned: !pinned, remoteNode });
      onPinChanged();
    } catch (error) {
      showError(
        error instanceof Error ? error.message : String(error),
        ts('Please try again or check the server connection.')
      );
    }
  };

  return (
    <>
      <DropdownMenuItem className="gap-2" onSelect={() => void copy(name)}>
        <Copy aria-hidden="true" className={menuIcon} />
        {ts('Copy name')}
      </DropdownMenuItem>
      <DropdownMenuItem
        className="gap-2"
        onSelect={() => void copy(getPageLink())}
      >
        <Link2 aria-hidden="true" className={menuIcon} />
        {ts('Copy link to this workflow')}
      </DropdownMenuItem>
      {pinned !== undefined && canPin && (
        <DropdownMenuItem className="gap-2" onSelect={() => void togglePin()}>
          <Pin aria-hidden="true" className={menuIcon} />
          {ts(pinned ? 'Unpin workflow' : 'Pin workflow')}
        </DropdownMenuItem>
      )}
      <DropdownMenuItem
        className="gap-2"
        onSelect={() => void refresh().catch(() => undefined)}
      >
        <RefreshCw aria-hidden="true" className={menuIcon} />
        {ts('Refresh')}
      </DropdownMenuItem>
      <DropdownMenuItem
        className="gap-2"
        disabled={!handle}
        onSelect={() => handle?.openWorkflowPanel()}
      >
        <PanelRight aria-hidden="true" className={menuIcon} />
        {ts('Workflow panel')}
      </DropdownMenuItem>
      <DropdownMenuItem
        className="gap-2"
        disabled={!handle}
        onSelect={() => handle?.toggleYaml(true)}
      >
        <FileCode aria-hidden="true" className={menuIcon} />
        {ts('Open {tab}', { tab: ts('YAML') })}
      </DropdownMenuItem>
      {canManageWebhooks && (
        <DropdownMenuItem
          className="gap-2"
          onSelect={() => guard(() => setUrl({ panel: 'webhook', def: null }))}
        >
          <Webhook aria-hidden="true" className={menuIcon} />
          {ts('Open {tab}', { tab: ts('Webhook') })}
        </DropdownMenuItem>
      )}
      {onDelete && (
        // Below md the bar has no room for the Delete button.
        <DropdownMenuItem
          data-testid="ww-bar-more-delete"
          className="gap-2 text-destructive md:hidden"
          onSelect={onDelete}
        >
          <Trash2 aria-hidden="true" className={menuIcon} />
          {ts('Delete workflow')}
        </DropdownMenuItem>
      )}
    </>
  );
}
