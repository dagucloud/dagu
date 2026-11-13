// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { I18nText } from '@/i18n/I18nText';
import type { ToolSelection } from '../../../contracts';
import { Section } from '../../Section';
import type { SectionProps } from '../../types';
import { OutputSection } from '../chat/OutputSection';
import { PromptSection, SystemSection } from '../chat/PromptSections';
import { OtherKeys } from '../Parameters';
import { SkillsSection } from '../skills/SkillsSection';
import { AgentRunLimits } from './AgentRunLimits';
import { AgentRunModel, type AgentRunModelProps } from './AgentRunModel';
import { ToolsSection } from '../tools/ToolsSection';
import { BuiltinTools } from './BuiltinTools';
import { agentRunIssueCounts, otherAgentKeys } from './model';

/** The sections of the form of an agent step. */
export type AgentRunSectionId =
  | 'model'
  | 'system'
  | 'prompt'
  | 'tools'
  | 'skills'
  | 'limits'
  | 'output';

export interface AgentRunSectionsProps extends SectionProps {
  /** The DAG that holds the step, for the endpoints that it may use. */
  dag?: AgentRunModelProps['dag'];
  /** A new value opens the section, so that a field in it can take focus. */
  revealKey(section: AgentRunSectionId): number | undefined;
  /** Selects a tool entry: its panel opens at the right. */
  onOpenTool?(tool: ToolSelection): void;
  /** Opens the dock that picks a tool to add. */
  onAddTool?(slot: 'tools' | 'dags'): void;
}

/**
 * The sections of an agent step, in this order: Model, System message,
 * Prompt, Tools (the DAG, action and MCP tools, then the built-in tools),
 * Skills, Limits and Output. A key of `with` that no section edits shows in a list
 * of other keys. The
 * sections edit the keys of Spec 091; where the key is the key of a chat
 * step, they are the sections of the chat form.
 */
export function AgentRunSections({
  dag,
  revealKey,
  onOpenTool,
  onAddTool,
  ...common
}: AgentRunSectionsProps): ReactElement {
  const { step, fieldIssues } = common;
  const counts = agentRunIssueCounts(fieldIssues);
  const other = otherAgentKeys(step.value);
  return (
    <>
      <Section
        title={<I18nText text="Model" />}
        count={counts.model}
        revealKey={revealKey('model')}
      >
        <AgentRunModel {...common} dag={dag} />
      </Section>
      <Section
        title={<I18nText text="System message" />}
        count={counts.system}
        revealKey={revealKey('system')}
      >
        <SystemSection {...common} />
      </Section>
      <Section
        title={<I18nText text="Prompt" />}
        count={counts.prompt}
        revealKey={revealKey('prompt')}
      >
        <PromptSection {...common} messages={false} />
      </Section>
      <Section
        title={<I18nText text="Tools" />}
        count={counts.tools}
        revealKey={revealKey('tools')}
      >
        <div className="space-y-3">
          <ToolsSection
            {...common}
            entriesOnly
            {...(onOpenTool ? { onOpenTool } : {})}
            {...(onAddTool ? { onAddTool } : {})}
          />
          <BuiltinTools {...common} />
        </div>
      </Section>
      <Section
        title={<I18nText text="Skills" />}
        count={counts.skills}
        revealKey={revealKey('skills')}
      >
        <SkillsSection {...common} />
      </Section>
      <Section
        title={<I18nText text="Limits" />}
        count={counts.limits}
        revealKey={revealKey('limits')}
      >
        <AgentRunLimits {...common} />
      </Section>
      <Section
        title={<I18nText text="Output" />}
        count={counts.output}
        revealKey={revealKey('output')}
      >
        <OutputSection {...common} />
      </Section>
      {other.length > 0 && (
        <div className="border-b border-border px-3 py-2">
          <OtherKeys entries={other} fieldIssues={fieldIssues} />
        </div>
      )}
    </>
  );
}
