// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FileCode, SlidersHorizontal } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * The way from one right panel of the editor to the other, in its header:
 * the Workflow panel and the YAML panel take the same place, so one click
 * swaps them.
 */
export function PanelSwitchButton({
  to,
  onClick,
}: {
  to: 'workflow' | 'yaml';
  onClick(): void;
}) {
  const { ts } = useI18n();
  const name = ts(to === 'workflow' ? 'Workflow' : 'YAML');
  const Icon = to === 'workflow' ? SlidersHorizontal : FileCode;
  return (
    <Button
      size="xs"
      variant="ghost"
      data-testid={`ve-panel-switch-${to}`}
      aria-label={ts('Switch to {panel}', { panel: name })}
      title={ts('Switch to {panel}', { panel: name })}
      onClick={onClick}
      className="shrink-0 gap-1 text-muted-foreground"
    >
      <Icon className="size-3.5" aria-hidden="true" />
      {name}
    </Button>
  );
}
