// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import type { SpecViewMode } from '@/contexts/UserPreference';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { Columns2, FileCode, GitGraph, LayoutDashboard } from 'lucide-react';
import React from 'react';

type Props = {
  value: SpecViewMode;
  onChange: (value: SpecViewMode) => void;
  /** Whether the side-by-side view fits; the Split button is hidden otherwise. */
  canSplit: boolean;
  /** Agent DAGs preview an overview rather than a step graph. */
  isAgent: boolean;
};

/** Chooses which panes the DAG spec tab shows. */
function SpecViewSwitcher({ value, onChange, canSplit, isAgent }: Props) {
  const PreviewIcon = isAgent ? LayoutDashboard : GitGraph;
  return (
    <I18nProps>
      <ToggleGroup aria-label="View mode" className="h-9 p-0.5">
        <ToggleButton
          value="graph"
          groupValue={value}
          onClick={() => onChange('graph')}
          position="first"
          className="h-8 px-3"
        >
          <PreviewIcon size={16} className="mr-1.5" />
          <I18nText text={isAgent ? 'Overview' : 'Graph'} />
        </ToggleButton>
        <ToggleButton
          value="yaml"
          groupValue={value}
          onClick={() => onChange('yaml')}
          position={canSplit ? 'middle' : 'last'}
          className="h-8 px-3"
        >
          <FileCode size={16} className="mr-1.5" />
          <I18nText text="YAML" />
        </ToggleButton>
        {canSplit && (
          <ToggleButton
            value="split"
            groupValue={value}
            onClick={() => onChange('split')}
            position="last"
            className="h-8 px-3"
          >
            <Columns2 size={16} className="mr-1.5" />
            <I18nText text="Split" />
          </ToggleButton>
        )}
      </ToggleGroup>
    </I18nProps>
  );
}

export default SpecViewSwitcher;
