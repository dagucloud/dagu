// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useContext, useEffect, useMemo, useState } from 'react';
import { ViewSpecType } from '@/api/v1/schema';
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

const ALL_WORKSPACES = '__all__';
const DEFAULT_INTERVAL = 1;
const MAX_INTERVAL = 30;

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** When set, the dialog edits the given view; otherwise it creates a new one. */
  view?: View | null;
  onSaved?: (view: View) => void;
  onDeleted?: () => void;
}

export function ViewEditorDialog({
  open,
  onOpenChange,
  view,
  onSaved,
  onDeleted,
}: Props): React.ReactElement {
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const { createView, updateView, deleteView } = useViews();

  const isEdit = !!view;
  const [name, setName] = useState('');
  const [workspace, setWorkspace] = useState(ALL_WORKSPACES);
  const [labels, setLabels] = useState<string[]>([]);
  const [dagName, setDagName] = useState('');
  const [intervalDays, setIntervalDays] = useState(DEFAULT_INTERVAL);
  const [pinned, setPinned] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Re-seed form state whenever the dialog opens or the edited view changes.
  useEffect(() => {
    if (!open) {
      return;
    }
    setName(view?.name ?? '');
    setWorkspace(view?.workspace ? view.workspace : ALL_WORKSPACES);
    setLabels(view?.labels ?? []);
    setDagName(view?.dagName ?? '');
    setIntervalDays(view?.intervalDays ?? DEFAULT_INTERVAL);
    setPinned(view?.pinned ?? false);
    setError(null);
  }, [open, view]);

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
  const canSave = trimmedName.length > 0 && !saving;

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
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? 'Edit view' : 'New view'}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              Name
            </label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="My view"
              autoFocus
            />
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              Workspace
            </label>
            <Select value={workspace} onValueChange={setWorkspace}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_WORKSPACES}>All workspaces</SelectItem>
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
              Labels
            </label>
            <LabelCombobox
              selectedLabels={labels}
              onLabelsChange={setLabels}
              availableLabels={availableLabels}
            />
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              DAG name filter
            </label>
            <Input
              value={dagName}
              onChange={(e) => setDagName(e.target.value)}
              placeholder="Any"
            />
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium text-muted-foreground">
              Interval (days per row)
            </label>
            <Input
              type="number"
              min={1}
              max={MAX_INTERVAL}
              value={intervalDays}
              onChange={(e) => setIntervalDays(Number(e.target.value))}
            />
            <p className="text-[11px] text-muted-foreground">
              Each row groups this many days, scrolling back in time.
            </p>
          </div>
          <label className="flex items-center gap-2 text-sm text-foreground">
            <Checkbox
              checked={pinned}
              onCheckedChange={(checked) => setPinned(checked === true)}
            />
            Pin to sidebar
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
              Delete
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
              Cancel
            </Button>
            <Button onClick={handleSave} disabled={!canSave}>
              {isEdit ? 'Save' : 'Create'}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
