// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { MessageSquareText } from 'lucide-react';
import { memo, type KeyboardEvent, type MouseEvent } from 'react';
import { Markdown } from '@/components/ui/markdown';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasActions } from '../context';
import type { NodeProps } from '../engine';
import {
  PROMPT_PREVIEW_CHARS,
  SYSTEM_PREVIEW_CHARS,
  previewText,
  promptFit,
} from '../promptCard';
import { remarkReferences } from '../promptRefs';
import { promptCardParts } from '../subRow';
import type { StepFlowNode } from '../types';
import { CARD_OUTLINE } from './NodeParts';
import { SUB_SELECTED_CLASS, SubHandle, SubTile } from './SubLeaf';

/**
 * The preview has the size of a line of text: a heading is bold, not large,
 * and a list keeps its marks (the base style of the page removes them).
 */
const PREVIEW_MARKDOWN =
  'leading-snug [&_h1]:my-0 [&_h2]:my-0 [&_h3]:my-0 [&_h1]:text-[1em] [&_h2]:text-[1em] [&_h3]:text-[1em] [&_h1]:font-bold [&_h2]:font-bold [&_h3]:font-semibold [&_p]:my-0 [&_ul]:my-0 [&_ol]:my-0 [&_ul]:list-disc [&_ol]:list-decimal [&_ul]:pl-4 [&_ol]:pl-4';

/** The label of a block, and a preview of the first characters of its text, as Markdown. */
function Block({
  label,
  text,
  max,
  labelHeight,
  height,
  placeholder,
  testId,
}: {
  label: string;
  text: string;
  max: number;
  labelHeight: number;
  height: number;
  placeholder: string;
  testId: string;
}) {
  const filled = text.trim() !== '';
  return (
    <div data-testid={testId} data-empty={filled ? undefined : ''}>
      <div
        style={{ height: labelHeight }}
        className="flex items-end px-2.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground"
      >
        {label}
      </div>
      <div style={{ height }} className="relative overflow-hidden px-2.5">
        {filled ? (
          // The pointer goes through the text: a link in it must not open.
          <div className="pointer-events-none">
            <Markdown
              content={previewText(text, max)}
              remarkPlugins={[remarkReferences]}
              className={cn(PREVIEW_MARKDOWN, 'text-[11px]')}
            />
          </div>
        ) : (
          <span className="text-[11px] italic text-muted-foreground">
            {placeholder}
          </span>
        )}
        {filled && (
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-x-0 bottom-0 h-3 bg-gradient-to-t from-card to-transparent"
          />
        )}
      </div>
    </div>
  );
}

/**
 * The Prompt card of an agent step: the system message and the prompt, each
 * as the first characters of its text rendered as Markdown. A click, Enter or
 * Space opens the dock that edits both. The height of the card follows the
 * lines of the texts, in two steps for each (see `promptFit`): the text is cut,
 * never the card. A compact canvas draws a tile (`SubTile`).
 */
function PromptCardNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { node } = data;
  const card = node.promptCard;
  const { ts } = useI18n();
  const { compact = false, onCommand } = useCanvasActions();
  if (!card) return null;
  const parts = promptCardParts(promptFit(card.system, card.prompt));
  const open = () => onCommand({ kind: 'openPromptDock', step: card.owner });
  const empty = ts('Empty');
  if (compact) {
    return (
      <SubTile
        slot="prompt"
        selected={selected}
        icon={MessageSquareText}
        label={ts('Edit the prompt and the system message')}
        title={[
          ts('Prompt'),
          previewText(card.prompt, PROMPT_PREVIEW_CHARS) || empty,
        ].join('\n')}
        attrs={{
          'data-testid': `ve-canvas-node-${node.key}`,
          'data-shape': 'promptcard',
        }}
        openOnClick
        onOpen={open}
      />
    );
  }
  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="promptcard"
      role="button"
      tabIndex={0}
      aria-label={ts('Edit the prompt and the system message')}
      title={ts('Edit the prompt and the system message')}
      aria-current={selected || undefined}
      data-selected={selected || undefined}
      onClick={(event: MouseEvent) => {
        event.stopPropagation();
        open();
      }}
      onDoubleClick={(event) => event.stopPropagation()}
      onKeyDown={(event: KeyboardEvent) => {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        event.preventDefault();
        event.stopPropagation();
        open();
      }}
      className={cn(
        'group relative flex h-full w-full cursor-pointer flex-col rounded-md bg-card text-card-foreground outline-none hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring',
        CARD_OUTLINE,
        selected && SUB_SELECTED_CLASS
      )}
    >
      <SubHandle slot="prompt" />
      <div
        style={{ height: parts.header }}
        className="flex shrink-0 items-center border-b border-border/60 px-2.5"
      >
        <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {ts('Prompt')}
        </span>
      </div>
      <Block
        testId="ve-canvas-prompt-system"
        label={ts('System message')}
        text={card.system}
        max={SYSTEM_PREVIEW_CHARS}
        labelHeight={parts.label}
        height={parts.system}
        placeholder={empty}
      />
      <Block
        testId="ve-canvas-prompt-text"
        label={ts('Prompt')}
        text={card.prompt}
        max={PROMPT_PREVIEW_CHARS}
        labelHeight={parts.label}
        height={parts.prompt}
        placeholder={empty}
      />
    </div>
  );
}

export const PromptCardNode = memo(PromptCardNodeComponent);
