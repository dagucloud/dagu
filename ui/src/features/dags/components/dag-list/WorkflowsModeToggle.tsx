// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { History, Network } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

type Mode = 'workflows' | 'runs';

/**
 * The two modes of the Workflows page: the list of workflows, and the list of
 * the runs of all the workflows (the former Executions page).
 */
export function WorkflowsModeToggle({ mode }: { mode: Mode }) {
  const navigate = useNavigate();
  const go = (next: Mode) => {
    if (next !== mode) navigate(next === 'runs' ? '/dag-runs' : '/dags');
  };
  return (
    <I18nProps>
      <ToggleGroup aria-label="Show" className="h-9 p-0.5">
        <ToggleButton
          value="workflows"
          groupValue={mode}
          onClick={() => go('workflows')}
          position="first"
          aria-label="Workflows"
          className="h-8 px-3"
        >
          <Network size={16} className="sm:mr-1.5" />
          <span className="hidden sm:inline">
            <I18nText text={'Workflows'} />
          </span>
        </ToggleButton>
        <ToggleButton
          value="runs"
          groupValue={mode}
          onClick={() => go('runs')}
          position="last"
          aria-label="Runs"
          className="h-8 px-3"
        >
          <History size={16} className="sm:mr-1.5" />
          <span className="hidden sm:inline">
            <I18nText text={'Runs'} />
          </span>
        </ToggleButton>
      </ToggleGroup>
    </I18nProps>
  );
}
