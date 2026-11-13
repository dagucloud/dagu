// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEndpointChoices,
  type DagIdentity,
} from '@/features/ai-endpoints/useEndpointChoices';
import { useEndpointModels } from '@/features/ai-endpoints/useEndpointModels';
import type { ModelEditData, ModelEditRow } from '../../../canvas/chatModel';
import {
  effortEdits,
  endpointEdits,
  modelEdits,
  modelLevels,
} from '../../../canvas/modelEdits';
import type { DagEditOp, StepKey } from '../../../contracts';

/** What the Model section of a form reads and writes for the single model of a step. */
export interface EndpointBlock {
  /** The single model of the step. Undefined for a list, or a value that only the YAML edits. */
  row: ModelEditRow | undefined;
  /** The effort levels that the model of the row supports. */
  levels: string[];
  setEndpoint(value: string | undefined): void;
  commitModel(text: string | undefined): void;
  setEffort(level: string | undefined): void;
}

/**
 * The endpoint, the model and the effort of the single model of a step, for
 * the forms of an agent step and of a chat step. The edits are the ones of the
 * Model node of the canvas (`modelEdits.ts`), so the form and the node write
 * the same keys.
 */
export function useEndpointBlock(
  step: StepKey,
  edit: ModelEditData | undefined,
  dag: DagIdentity | undefined,
  onEdit: (op: DagEditOp) => void
): EndpointBlock {
  const first = edit?.rows[0];
  const row = first?.index === null ? first : undefined;
  const { choices, cliModelAliases } = useEndpointChoices(dag);
  const { infos } = useEndpointModels(row?.endpoint, dag);
  const agent = edit?.kind === 'agent';
  const apply = (ops: DagEditOp[]) => {
    for (const op of ops) onEdit(op);
  };
  return {
    row,
    levels: modelLevels(infos, cliModelAliases, agent, row?.name ?? ''),
    setEndpoint: (value) => {
      if (!edit || !row) return;
      apply(
        endpointEdits(step, edit, row, value, {
          choices,
          aliases: cliModelAliases,
        })
      );
    },
    commitModel: (text) => {
      if (edit && row) apply(modelEdits(step, edit, row, text));
    },
    setEffort: (level) => {
      if (edit) apply(effortEdits(step, edit, level));
    },
  };
}
