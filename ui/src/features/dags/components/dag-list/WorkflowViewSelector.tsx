// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Bookmark,
  Check,
  ChevronDown,
  RotateCcw,
  Save,
  Settings2,
  Star,
  Trash2,
} from 'lucide-react';
import React from 'react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { WorkflowFilterView } from '@/contexts/UserPreference';

type Props = {
  views: WorkflowFilterView[];
  activeViewId: string | null;
  defaultViewId?: string;
  isAllView: boolean;
  isActiveViewEdited: boolean;
  onSelectView: (viewId: string) => void;
  onShowAll: () => void;
  onResetView: () => void;
  onSaveView: (name: string, makeDefault: boolean) => void;
  onUpdateView: () => void;
  onSetDefault: (viewId: string | undefined) => void;
  onDeleteView: (viewId: string) => void;
};

export function WorkflowViewSelector({
  views,
  activeViewId,
  defaultViewId,
  isAllView,
  isActiveViewEdited,
  onSelectView,
  onShowAll,
  onResetView,
  onSaveView,
  onUpdateView,
  onSetDefault,
  onDeleteView,
}: Props): React.ReactElement {
  const [saveDialogOpen, setSaveDialogOpen] = React.useState(false);
  const [manageDialogOpen, setManageDialogOpen] = React.useState(false);
  const [viewName, setViewName] = React.useState('');
  const [makeDefault, setMakeDefault] = React.useState(false);
  const [pendingDelete, setPendingDelete] =
    React.useState<WorkflowFilterView | null>(null);

  const activeView = views.find((view) => view.id === activeViewId);
  const isCustomView = !activeView && !isAllView;
  const selectedLabel =
    activeView?.name ?? (isCustomView ? 'Custom view' : 'All workflows');
  const normalizedName = viewName.trim();
  const duplicateName = views.some(
    (view) => view.name.toLowerCase() === normalizedName.toLowerCase()
  );
  const canSave = normalizedName.length > 0 && !duplicateName;

  const openSaveDialog = () => {
    setViewName('');
    setMakeDefault(false);
    setSaveDialogOpen(true);
  };

  const saveView = (event: React.FormEvent) => {
    event.preventDefault();
    if (!canSave) {
      return;
    }
    onSaveView(normalizedName, makeDefault);
    setSaveDialogOpen(false);
  };

  const requestDelete = (view: WorkflowFilterView) => {
    setManageDialogOpen(false);
    setPendingDelete(view);
  };

  const dismissDelete = () => {
    setPendingDelete(null);
    setManageDialogOpen(true);
  };

  const confirmDelete = () => {
    if (pendingDelete) {
      onDeleteView(pendingDelete.id);
    }
    setPendingDelete(null);
    setManageDialogOpen(true);
  };

  return (
    <>
      <div className="flex min-w-0 items-center gap-2">
        <span className="text-xs font-medium text-muted-foreground">View</span>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              className="h-9 min-w-[190px] max-w-[280px] justify-start px-3"
              aria-label={`Workflow view: ${selectedLabel}`}
            >
              {activeViewId === defaultViewId ? (
                <Star className="fill-current text-primary" />
              ) : (
                <Bookmark />
              )}
              <span className="min-w-0 flex-1 truncate text-left">
                {selectedLabel}
              </span>
              {activeViewId === defaultViewId && (
                <Badge variant="primary" className="hidden sm:inline-flex">
                  Default
                </Badge>
              )}
              {isActiveViewEdited && <Badge variant="secondary">Edited</Badge>}
              <ChevronDown className="text-muted-foreground" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-[280px]">
            <DropdownMenuItem onSelect={onShowAll}>
              <span className="mr-2 flex size-4 items-center justify-center">
                {isAllView && <Check />}
              </span>
              All workflows
            </DropdownMenuItem>

            {views.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  My views
                </DropdownMenuLabel>
                {views.map((view) => (
                  <DropdownMenuItem
                    key={view.id}
                    onSelect={() => onSelectView(view.id)}
                  >
                    <span className="mr-2 flex size-4 items-center justify-center">
                      {view.id === activeViewId ? (
                        <Check />
                      ) : view.id === defaultViewId ? (
                        <Star className="fill-current text-primary" />
                      ) : null}
                    </span>
                    <span className="min-w-0 flex-1 truncate">{view.name}</span>
                    {view.id === defaultViewId && (
                      <Badge variant="primary">Default</Badge>
                    )}
                  </DropdownMenuItem>
                ))}
              </>
            )}

            {activeView && isActiveViewEdited && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={onUpdateView}>
                  <Save className="mr-2" />
                  Update “{activeView.name}”
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={onResetView}>
                  <RotateCcw className="mr-2" />
                  Reset changes
                </DropdownMenuItem>
              </>
            )}

            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={openSaveDialog}>
              <Save className="mr-2" />
              Save current filters as view…
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setManageDialogOpen(true)}>
              <Settings2 className="mr-2" />
              Manage views…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <Dialog open={saveDialogOpen} onOpenChange={setSaveDialogOpen}>
        <DialogContent className="sm:max-w-[440px]">
          <form onSubmit={saveView}>
            <DialogHeader>
              <DialogTitle>Save workflow view</DialogTitle>
              <DialogDescription>
                Save the current name and label filters, plus the sort order,
                for this remote and workspace.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-5">
              <div className="space-y-2">
                <Label htmlFor="workflow-view-name">Name</Label>
                <Input
                  id="workflow-view-name"
                  value={viewName}
                  maxLength={80}
                  autoFocus
                  placeholder="Production operations"
                  onChange={(event) => setViewName(event.target.value)}
                />
                {duplicateName && (
                  <p className="text-xs text-destructive">
                    A view with this name already exists.
                  </p>
                )}
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="workflow-view-default"
                  checked={makeDefault}
                  onCheckedChange={(checked) =>
                    setMakeDefault(checked === true)
                  }
                />
                <Label htmlFor="workflow-view-default" className="font-normal">
                  Make this my default view
                </Label>
              </div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setSaveDialogOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" variant="primary" disabled={!canSave}>
                Save view
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={manageDialogOpen} onOpenChange={setManageDialogOpen}>
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>Manage workflow views</DialogTitle>
            <DialogDescription>
              Choose the view that opens by default, or remove views saved for
              this remote and workspace.
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[360px] space-y-2 overflow-y-auto py-2">
            {views.length > 0 ? (
              views.map((view) => {
                const isDefault = view.id === defaultViewId;
                return (
                  <div
                    key={view.id}
                    className="flex items-center gap-2 rounded-md border border-border px-3 py-2"
                  >
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-pressed={isDefault}
                      aria-label={
                        isDefault
                          ? `Remove ${view.name} as the default view`
                          : `Make ${view.name} the default view`
                      }
                      onClick={() =>
                        onSetDefault(isDefault ? undefined : view.id)
                      }
                    >
                      <Star
                        className={
                          isDefault ? 'fill-current text-primary' : undefined
                        }
                      />
                    </Button>
                    <span className="min-w-0 flex-1 truncate text-sm font-medium">
                      {view.name}
                    </span>
                    {isDefault && <Badge variant="primary">Default</Badge>}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Delete ${view.name}`}
                      onClick={() => requestDelete(view)}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                );
              })
            ) : (
              <div className="rounded-md border border-dashed border-border px-4 py-8 text-center text-sm text-muted-foreground">
                No saved workflow views yet.
              </div>
            )}
          </div>
          <DialogFooter>
            <Button type="button" onClick={() => setManageDialogOpen(false)}>
              Done
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        title="Delete workflow view?"
        buttonText="Delete view"
        visible={pendingDelete !== null}
        dismissModal={dismissDelete}
        onSubmit={confirmDelete}
      >
        <p className="text-sm text-muted-foreground">
          “{pendingDelete?.name}” will be removed from this browser. Workflows
          are not affected.
        </p>
      </ConfirmDialog>
    </>
  );
}
