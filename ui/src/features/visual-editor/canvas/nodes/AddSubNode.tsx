// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Bot, Plus } from 'lucide-react';
import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasActions } from '../context';
import type { NodeProps } from '../engine';
import { ADD_HOVER_CLASS, LinkButton } from '../LinkButton';
import { SUB_ICON_SIZE, type CardSlot } from '../subRow';
import type { StepFlowNode } from '../types';
import type { CanvasNode, StepKey } from '../../contracts';
import { CARD_OUTLINE } from './NodeParts';
import { SubHandle, useOpenSubNode } from './SubLeaf';

/**
 * The small round "+" of a Tools, Skills, DAGs or Prompt slot that has no entry. It is the
 * round "+" of a link (`LinkButton`), and it opens the dock that adds one. A
 * read-only canvas has no button; the empty slot stays, dashed, as the sign of
 * the gap.
 */
function SlotAddNode({
  node,
  owner,
  slot,
}: {
  node: CanvasNode;
  owner: StepKey;
  slot: CardSlot | 'prompt';
}) {
  const { ts } = useI18n();
  const { editable, onCommand } = useCanvasActions();
  const label =
    slot === 'prompt'
      ? ts('Add a prompt')
      : slot === 'tools'
        ? ts('Add a tool')
        : slot === 'dags'
          ? ts('Add a DAG')
          : ts('Add a skill');
  const idle =
    slot === 'prompt'
      ? ts('No prompt')
      : slot === 'tools'
        ? ts('No tools')
        : slot === 'dags'
          ? ts('No DAGs')
          : ts('No skills');
  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="add"
      data-slot={slot}
      className="group relative flex h-full w-full items-center justify-center"
    >
      <SubHandle slot={slot} />
      {editable ? (
        <LinkButton
          kind="add"
          movable
          aria-label={label}
          title={label}
          data-testid="ve-canvas-sub-add"
          onClick={(event) => {
            event.stopPropagation();
            onCommand(
              slot === 'prompt'
                ? { kind: 'openPromptDock', step: owner }
                : { kind: 'openSlotDock', step: owner, slot }
            );
          }}
          onDoubleClick={(event) => event.stopPropagation()}
        />
      ) : (
        <span
          role="img"
          aria-label={idle}
          title={idle}
          className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border-[1.5px] border-dashed border-muted-foreground bg-card text-muted-foreground"
        >
          <Plus className="h-3 w-3" aria-hidden="true" />
        </span>
      )}
    </div>
  );
}

/**
 * The dashed round "+" under the Model slot of a chat step while the step
 * names no model, in a canvas with no controls of the model (compact, or with
 * no edit data); the edit view draws the pickers instead. The button opens the
 * form of the step. A read-only canvas has no button; the empty slot stays, dashed, as the sign of the gap. The
 * Tools and Skills slots have the small round "+" of `SlotAddNode`.
 */
function AddSubNodeComponent({ data }: NodeProps<StepFlowNode>) {
  const { ts } = useI18n();
  const { editable } = useCanvasActions();
  const { node } = data;
  const open = useOpenSubNode(node);
  const add = node.addSat;
  if (!add) return null;
  if (add.slot !== 'model') {
    return <SlotAddNode node={node} owner={add.owner} slot={add.slot} />;
  }
  const label = ts('Add a model');
  const idle = ts('No model set');
  const box = cn(
    'flex shrink-0 items-center justify-center rounded-full border-dashed bg-card text-muted-foreground',
    CARD_OUTLINE
  );
  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="add"
      data-slot={add.slot}
      className="group relative flex h-full w-full flex-col items-center"
    >
      <SubHandle slot="model" leaf />
      {editable ? (
        <button
          type="button"
          aria-label={label}
          title={label}
          data-testid="ve-canvas-sub-add"
          style={{ width: SUB_ICON_SIZE, height: SUB_ICON_SIZE }}
          className={cn(
            box,
            ADD_HOVER_CLASS,
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
          )}
          onClick={(event) => {
            event.stopPropagation();
            open();
          }}
          onDoubleClick={(event) => event.stopPropagation()}
        >
          <Plus className="h-7 w-7" aria-hidden="true" />
        </button>
      ) : (
        <span
          role="img"
          aria-label={idle}
          title={idle}
          style={{ width: SUB_ICON_SIZE, height: SUB_ICON_SIZE }}
          className={box}
        >
          <Bot className="h-7 w-7" aria-hidden="true" />
        </span>
      )}
      <span className="mt-1.5 line-clamp-2 w-full break-words text-center text-xs leading-4 text-muted-foreground">
        {editable ? label : idle}
      </span>
    </div>
  );
}

export const AddSubNode = memo(AddSubNodeComponent);
