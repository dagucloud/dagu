// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useI18n } from '@/i18n/I18nProvider';
import { buildAgentRunInfo } from '@/features/visual-editor/run/agentRun';
import type { ApiDAGRunDetails } from '../../contracts';

/**
 * One line over the decision timeline of an agent run: the turns of the
 * agent, the tool calls and how many tasks are settled. The canvas hub shows
 * the same facts. Nothing shows for a run that is not an agent run.
 */
export function AgentRunSummary({ details }: { details: ApiDAGRunDetails }) {
  const { ts } = useI18n();
  const info = buildAgentRunInfo(details);
  if (!info) {
    return null;
  }
  const calls = Object.values(info.calls).reduce((sum, n) => sum + n, 0);
  const states = Object.values(info.tasks);
  const settled = states.filter((state) => state !== 'open').length;
  return (
    <div
      data-testid="agent-run-summary"
      className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground"
    >
      <span data-testid="agent-run-iterations">
        {ts('Iterations: {count}', { count: info.iterations })}
      </span>
      <span data-testid="agent-run-calls">
        {ts('Tool calls: {count}', { count: calls })}
      </span>
      {states.length > 0 && (
        <span data-testid="agent-run-tasks">
          {ts('{settled} of {total} tasks settled', {
            settled,
            total: states.length,
          })}
        </span>
      )}
    </div>
  );
}
