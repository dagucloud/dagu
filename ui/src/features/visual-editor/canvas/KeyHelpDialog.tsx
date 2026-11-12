// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Kbd } from '@/components/ui/kbd';
import { useI18n } from '@/i18n/I18nProvider';
import { CANVAS_KEY_TABLE } from './useCanvasKeymap';

interface HelpLine {
  keys: readonly string[];
  /** True when the keys are English i18n source texts, not key names. */
  keyLabels?: boolean;
  /** English i18n source text. */
  label: string;
}

const POINTER_LINES: readonly HelpLine[] = [
  { keys: ['Click'], keyLabels: true, label: 'Select a step' },
  { keys: ['Double-click'], keyLabels: true, label: 'Open a step' },
  { keys: ['Right-click'], keyLabels: true, label: 'Open the menu' },
  { keys: ['Drag'], keyLabels: true, label: 'Move a step or the view' },
  {
    keys: ['Wheel', 'Ctrl/Cmd+wheel or pinch'],
    keyLabels: true,
    label: 'Zoom',
  },
];

const SET_STATUS_LINE: HelpLine = {
  keys: ['Right-click'],
  keyLabels: true,
  label: 'Change the status of a step from its menu',
};

export interface KeyHelpDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  /** Shows the line about the status of a step. */
  showSetStatus: boolean;
}

/** The keys and the pointer gestures of the canvas. */
export function KeyHelpDialog({
  open,
  onOpenChange,
  showSetStatus,
}: KeyHelpDialogProps): ReactElement {
  const { ts } = useI18n();
  const keyLines: HelpLine[] = CANVAS_KEY_TABLE.map((binding) => ({
    keys: binding.keys,
    label: binding.label,
  }));
  const pointerLines = showSetStatus
    ? [...POINTER_LINES, SET_STATUS_LINE]
    : POINTER_LINES;

  const section = (title: string, lines: readonly HelpLine[]) => (
    <section className="space-y-1.5">
      <h3 className="text-xs font-medium uppercase text-muted-foreground">
        {title}
      </h3>
      <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 text-sm">
        {lines.map((line) => (
          <div key={line.label} className="contents">
            <dt>{ts(line.label)}</dt>
            <dd className="flex flex-wrap justify-end gap-1">
              {line.keys.map((key) => (
                <Kbd key={key}>{line.keyLabels ? ts(key) : key}</Kbd>
              ))}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent aria-describedby={undefined} className="max-w-md">
        <DialogHeader>
          <DialogTitle>{ts('Keyboard shortcuts')}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {section(ts('Keys'), keyLines)}
          {section(ts('Pointer'), pointerLines)}
        </div>
      </DialogContent>
    </Dialog>
  );
}
