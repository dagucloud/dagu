// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The two text tools of a YAML view: apply every quick fix, and format.
 *
 * @module features/dags/components/dag-editor
 */
import { AlignLeft, Wand2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';

export interface YamlToolsProps {
  /**
   * What the tools act on: the whole file (default) or the one step that
   * the view shows. The buttons are the same; the labels and the test ids
   * say the scope.
   */
  scope?: 'file' | 'step';
  /** Number of quick fixes that the text, or the step, has. */
  fixCount: number;
  /** False when the text has a YAML syntax error: it cannot be formatted. */
  canFormat: boolean;
  /** Step scope only: why the format button is off when no step is known. */
  formatOffReason?: string;
  /** The text, or the step, is already formatted: the format button is off. */
  formatted?: boolean;
  onFixAll(): void;
  onFormat(): void;
}

/** Two icon buttons for the header of a YAML view. */
export function YamlTools({
  scope = 'file',
  fixCount,
  canFormat,
  formatOffReason,
  formatted = false,
  onFixAll,
  onFormat,
}: YamlToolsProps) {
  const { ts } = useI18n();
  const step = scope === 'step';
  const fixLabel = step
    ? ts('Apply quick fixes to this step')
    : ts('Apply all quick fixes');
  const formatLabel = step ? ts('Format this step') : ts('Format YAML');
  return (
    <>
      <Button
        type="button"
        variant="ghost"
        className="h-7 w-7 p-0"
        data-testid={step ? 'step-yaml-fix' : 'yaml-fix-all'}
        aria-label={fixLabel}
        title={
          fixCount > 0
            ? fixLabel
            : step
              ? ts('No quick fix')
              : ts('No quick fix to apply')
        }
        disabled={fixCount === 0}
        onClick={onFixAll}
      >
        <Wand2 className="h-4 w-4" aria-hidden="true" />
      </Button>
      <Button
        type="button"
        variant="ghost"
        className="h-7 w-7 p-0"
        data-testid={step ? 'step-yaml-format' : 'yaml-format'}
        aria-label={formatLabel}
        title={
          !canFormat
            ? (formatOffReason ??
              ts('Fix the YAML syntax error before you format'))
            : formatted
              ? step
                ? ts('The step is already formatted')
                : ts('The YAML is already formatted')
              : formatLabel
        }
        disabled={!canFormat || formatted}
        onClick={onFormat}
      >
        <AlignLeft className="h-4 w-4" aria-hidden="true" />
      </Button>
    </>
  );
}
