// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BookOpen, KeyRound, Plug } from 'lucide-react';
import {
  useContext,
  useEffect,
  type KeyboardEvent,
  type ReactElement,
} from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Tab, Tabs } from '@/components/ui/tabs';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useHideBreadcrumb } from '@/contexts/LayoutChromeContext';
import { ENDPOINTS_PATH } from '@/features/ai-endpoints/endpoint';
import { MCP_PATH } from '@/features/ai-endpoints/mcpServer';
import { SKILLS_PATH } from '@/features/skills/skill';
import {
  SettingsLeaveGuardProvider,
  useSettingsLeaveGuard,
} from '@/features/workflow-workspace/settings/useSettingsLeaveGuard';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { McpTab } from './McpTab';
import { EndpointsTab } from './EndpointsTab';
import { SkillsTab } from './SkillsTab';

type TabId = 'endpoints' | 'mcp' | 'skills';

/**
 * The AI page: the endpoints (the model APIs) and the MCP
 * servers (URLs with credentials), both for an administrator, and the skills
 * of the server, instructions that the model loads when it needs them. A user
 * who is not an administrator sees the skills only, with no tab row.
 */
export default function AIPage(): ReactElement {
  return (
    <SettingsLeaveGuardProvider>
      <AIPageContent />
    </SettingsLeaveGuardProvider>
  );
}

function AIPageContent(): ReactElement {
  const { guard } = useSettingsLeaveGuard();
  const { ts } = useI18n();
  const appBarContext = useContext(AppBarContext);
  const isAdmin = useIsAdmin();
  const location = useLocation();
  const navigate = useNavigate();
  // The tabs name the place: the breadcrumb strip above them adds nothing.
  useHideBreadcrumb(true);

  useEffect(() => {
    appBarContext.setTitle('AI');
  }, [appBarContext]);

  let active: TabId = 'skills';
  if (isAdmin && location.pathname.startsWith(MCP_PATH)) active = 'mcp';
  else if (isAdmin && !location.pathname.startsWith(SKILLS_PATH)) {
    active = 'endpoints';
  }
  // A skill that the user edits can hold a draft: the guard asks first.
  const select = (tab: TabId) =>
    guard(() =>
      navigate(
        tab === 'endpoints'
          ? ENDPOINTS_PATH
          : tab === 'mcp'
            ? MCP_PATH
            : SKILLS_PATH
      )
    );
  const tabs: TabId[] = ['endpoints', 'mcp', 'skills'];
  const onKeyDown = (event: KeyboardEvent<HTMLElement>, current: TabId) => {
    const index = tabs.indexOf(current);
    let next: TabId | undefined;
    if (event.key === 'ArrowRight') next = tabs[(index + 1) % tabs.length];
    else if (event.key === 'ArrowLeft') {
      next = tabs[(index + tabs.length - 1) % tabs.length];
    } else if (event.key === 'Home') next = tabs[0];
    else if (event.key === 'End') next = tabs[tabs.length - 1];
    if (!next) return;
    event.preventDefault();
    document.getElementById(`ai-tab-${next}`)?.focus();
    select(next);
  };

  return (
    // The skills tab fills the height: its editor sits in a pane of that height.
    <div
      className={cn(
        'flex flex-col gap-4',
        active === 'skills' && 'h-full min-h-[34rem]'
      )}
    >
      <h1 className="text-2xl font-semibold tracking-[-0.01em]">{ts('AI')}</h1>
      {isAdmin && (
        <Tabs role="tablist" aria-label={ts('AI')} className="shrink-0">
          {(
            [
              ['endpoints', ts('Endpoints'), KeyRound],
              ['mcp', ts('MCP'), Plug],
              ['skills', ts('Skills'), BookOpen],
            ] as const
          ).map(([id, label, Icon]) => (
            <Tab
              key={id}
              id={`ai-tab-${id}`}
              role="tab"
              aria-selected={active === id}
              aria-controls={`ai-panel-${id}`}
              isActive={active === id}
              tabIndex={active === id ? 0 : -1}
              className="cursor-pointer gap-2"
              onClick={() => select(id)}
              onKeyDown={(event) => onKeyDown(event, id)}
            >
              <Icon className="h-4 w-4" aria-hidden="true" />
              {label}
            </Tab>
          ))}
        </Tabs>
      )}
      <div
        className={cn(active === 'skills' && 'min-h-0 flex-1')}
        id={`ai-panel-${active}`}
        role={isAdmin ? 'tabpanel' : undefined}
        aria-labelledby={isAdmin ? `ai-tab-${active}` : undefined}
      >
        {active === 'endpoints' ? (
          <EndpointsTab />
        ) : active === 'mcp' ? (
          <McpTab />
        ) : (
          <SkillsTab />
        )}
      </div>
    </div>
  );
}
