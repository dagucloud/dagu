// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useContext, useEffect, useMemo, useState } from 'react';
import { ViewColumn, ViewSpecType } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { LabelCombobox } from '@/components/ui/label-combobox';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { View, ViewSpec, useViews } from '@/hooks/useViews';
import { withoutWorkspaceLabels } from '@/lib/workspace';
import { workspaceRoleTarget } from '@/lib/workspaceAccess';
import { ArrowDown, ArrowUp } from 'lucide-react';
import {
  createViewColumnSettings,
  moveVisibleViewColumn,
  setViewColumnVisibility,
  type ViewColumnSetting,
  VIEW_COLUMN_LABELS,
} from './viewColumns';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

const ALL_WORKSPACES = '__all__';
const DEFAULT_INTERVAL = 1;
const MAX_INTERVAL = 30;

function formWorkspace(
  view: View | null | undefined,
  selectedWorkspace: string
) {
  if (view) {
    return view.workspace && view.workspace.length > 0
      ? view.workspace
      : ALL_WORKSPACES;
  }
  return selectedWorkspace || ALL_WORKSPACES;
}

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** When set, the dialog edits the given view; otherwise it creates a new one. */
  view?: View | null;
  onSaved?: (view: View) => void;
  onDeleted?: () => void;
}

interface ColumnSettingRowProps {
  setting: ViewColumnSetting;
  visibleCount: number;
  visibleIndex?: number;
  onMove: (column: ViewColumn, direction: -1 | 1) => void;
  onVisibilityChange: (column: ViewColumn, visible: boolean) => void;
}

function ColumnSettingRow({
  setting,
  visibleCount,
  visibleIndex,
  onMove,
  onVisibilityChange,
}: ColumnSettingRowProps): React.ReactElement {
  const { column, visible } = setting;
  const label = VIEW_COLUMN_LABELS[column];

  return (
    <div className="flex items-center gap-2 rounded border border-border px-2 py-1">
      <Checkbox
        id={`view-column-${column}`}
        checked={visible}
        disabled={visible && visibleCount === 1}
        onCheckedChange={(checked) =>
          onVisibilityChange(column, checked === true)
        }
      />
      <label
        htmlFor={`view-column-${column}`}
        className="min-w-0 flex-1 text-sm text-foreground"
      >
        {label}
      </label>
      {visibleIndex !== undefined && (
        <>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={`Move ${label} up`}
            disabled={visibleIndex === 0}
            onClick={() => onMove(column, -1)}
          >
            <ArrowUp />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={`Move ${label} down`}
            disabled={visibleIndex === visibleCount - 1}
            onClick={() => onMove(column, 1)}
          >
            <ArrowDown />
          </Button>
        </>
      )}
    </div>
  );
}

export function ViewEditorDialog({
  open,
  onOpenChange,
  view,
  onSaved,
  onDeleted,
}: Props): React.ReactElement {
  const appBar = useContext(AppBarContext);
  // New views default to the workspace currently selected in the AppBar (if a
  // specific one is selected), so workspace-scoped users create writable views.
  const selectedWorkspace = workspaceRoleTarget(appBar.workspaceSelection);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const { createView, updateView, deleteView } = useViews();

  const isEdit = !!view;
  const [name, setName] = useState('');
  const [workspace, setWorkspace] = useState(ALL_WORKSPACES);
  const [labels, setLabels] = useState<string[]>([]);
  const [dagName, setDagName] = useState('');
  const [intervalDays, setIntervalDays] = useState(DEFAULT_INTERVAL);
  const [columnSettings, setColumnSettings] = useState<ViewColumnSetting[]>(
    () => createViewColumnSettings()
  );
  const [pinned, setPinned] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Re-seed form state whenever the dialog opens or the edited view changes.
  useEffect(() => {
    if (!open) {
      return;
    }
    setName(view?.name ?? '');
    setWorkspace(formWorkspace(view, selectedWorkspace));
    setLabels(view?.labels ?? []);
    setDagName(view?.dagName ?? '');
    setIntervalDays(view?.intervalDays ?? DEFAULT_INTERVAL);
    setColumnSettings(createViewColumnSettings(view?.columns));
    setPinned(view?.pinned ?? false);
    setError(null);
  }, [open, view, selectedWorkspace]);

  const { data: labelsData } = useQuery(
    '/dags/labels',
    whenEnabled(open, { params: { query: { remoteNode } } })
  );
  const availableLabels = useMemo(
    () => withoutWorkspaceLabels(labelsData?.labels ?? []),
    [labelsData?.labels]
  );

  const workspaces = appBar.workspaces ?? [];
  const trimmedName = name.trim();
  const visibleColumnSettings = columnSettings.filter(
    (setting) => setting.visible
  );
  const hiddenColumnSettings = columnSettings.filter(
    (setting) => !setting.visible
  );
  const canSave =
    trimmedName.length > 0 && visibleColumnSettings.length > 0 && !saving;

  const setColumnVisible = (column: ViewColumn, visible: boolean) => {
    setColumnSettings((current) =>
      setViewColumnVisibility(current, column, visible)
    );
  };

  const moveColumn = (column: ViewColumn, direction: -1 | 1) => {
    setColumnSettings((current) =>
      moveVisibleViewColumn(current, column, direction)
    );
  };

  const handleSave = async () => {
    if (!canSave) {
      return;
    }
    setSaving(true);
    setError(null);
    const spec: ViewSpec = {
      name: trimmedName,
      type: ViewSpecType.kanban,
      workspace: workspace === ALL_WORKSPACES ? '' : workspace,
      labels,
      dagName: dagName.trim(),
      intervalDays: Math.max(
        1,
        Math.min(intervalDays || DEFAULT_INTERVAL, MAX_INTERVAL)
      ),
      columns: visibleColumnSettings.map((setting) => setting.column),
      pinned,
    };
    try {
      const saved = isEdit
        ? await updateView(view!.id, spec)
        : await createView(spec);
      onSaved?.(saved);
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save view');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    if (!isEdit) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await deleteView(view!.id);
      onDeleted?.();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete view');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-md overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{isEdit ? <I18nText text={"Edit view"} /> : <I18nText text={"New view"} />}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              <I18nText text={"Name"} />
            </label>
            <I18nProps><Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="My view"
              autoFocus
            /></I18nProps>
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              <I18nText text={"Workspace"} />
            </label>
            <Select value={workspace} onValueChange={setWorkspace}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_WORKSPACES}><I18nText text={"All workspaces"} /></SelectItem>
                {workspaces.map((ws) => (
                  <SelectItem key={ws.id} value={ws.name}>
                    {ws.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              <I18nText text={"Labels"} />
            </label>
            <LabelCombobox
              selectedLabels={labels}
              onLabelsChange={setLabels}
              availableLabels={availableLabels}
            />
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              <I18nText text={"DAG name filter"} />
            </label>
            <I18nProps><Input
              value={dagName}
              onChange={(e) => setDagName(e.target.value)}
              placeholder="Any"
            /></I18nProps>
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              <I18nText text={"Interval (days per row)"} />
            </label>
            <Input
              type="number"
              min={1}
              max={MAX_INTERVAL}
              value={intervalDays}
              onChange={(e) => setIntervalDays(Number(e.target.value))}
            />
            <p className="text-[11px] text-muted-foreground">
              <I18nText text={"Each row groups this many days, scrolling back in time."} />
            </p>
          </div>
          <fieldset className="space-y-2">
            <legend className="text-xs font-medium text-muted-foreground">
              <I18nText text={"Columns"} />
            </legend>
            <p className="text-[11px] text-muted-foreground">
              <I18nText text={"Choose visible columns and arrange their display order."} />
            </p>
            <div className="space-y-1">
              <p className="text-[11px] font-medium text-muted-foreground">
                <I18nText text={"Visible"} />
              </p>
              {visibleColumnSettings.map((setting, index) => (
                <ColumnSettingRow
                  key={setting.column}
                  setting={setting}
                  visibleCount={visibleColumnSettings.length}
                  visibleIndex={index}
                  onMove={moveColumn}
                  onVisibilityChange={setColumnVisible}
                />
              ))}
              {hiddenColumnSettings.length > 0 && (
                <p className="pt-1 text-[11px] font-medium text-muted-foreground">
                  <I18nText text={"Hidden"} />
                </p>
              )}
              {hiddenColumnSettings.map((setting) => (
                <ColumnSettingRow
                  key={setting.column}
                  setting={setting}
                  visibleCount={visibleColumnSettings.length}
                  onMove={moveColumn}
                  onVisibilityChange={setColumnVisible}
                />
              ))}
            </div>
          </fieldset>
          <label className="flex items-center gap-2 text-sm text-foreground">
            <Checkbox
              checked={pinned}
              onCheckedChange={(checked) => setPinned(checked === true)}
            />
            <I18nText text={"Pin to sidebar"} />
          </label>
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
        <DialogFooter className="flex items-center justify-between sm:justify-between">
          {isEdit ? (
            <Button
              variant="ghost"
              className="text-destructive hover:text-destructive"
              onClick={handleDelete}
              disabled={saving}
            >
              <I18nText text={"Delete"} />
            </Button>
          ) : (
            <span />
          )}
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={saving}
            >
              <I18nText text={"Cancel"} />
            </Button>
            <Button onClick={handleSave} disabled={!canSave}>
              {isEdit ? <I18nText text={"Save"} /> : <I18nText text={"Create"} />}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
