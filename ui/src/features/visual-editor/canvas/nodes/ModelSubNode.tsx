// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { useEndpointChoices } from '@/features/ai-endpoints/useEndpointChoices';
import { useI18n } from '@/i18n/I18nProvider';
import { useCanvasActions } from '../context';
import type { NodeProps } from '../engine';
import { modelIcon } from '../providerLogo';
import type { StepFlowNode } from '../types';
import {
  ModelCard,
  ProblemMark,
  problemText,
  useModelProblems,
} from './ModelCard';
import { SubLeaf } from './SubLeaf';

/**
 * The Model node of a chat step or of an agent step. In the edit view of a
 * canvas with details it is a card with an Endpoint select and a Model field
 * (`ModelCard`), also while the step names no model. A compact canvas and a
 * run view draw the round node of text:
 * the logo of the provider (a bot icon when it has none), the name of the model
 * and its provider under it. With a list of models, which the step tries in
 * order, it names the first and counts the others ("+2"); the tooltip lists
 * all. An agent step has one model, with its provider under it, or with its AI
 * endpoint when it names one. The round node of an edit view has an amber
 * marker when the server would fail the step at its start.
 */
function ModelSubNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { ts } = useI18n();
  const { node } = data;
  const { compact, readOnly, dag } = useCanvasActions();
  const model = node.modelSat;
  const problems = useModelProblems(model?.edit, dag, !readOnly);
  const { choices } = useEndpointChoices(dag, !readOnly);
  const first = model?.models[0];
  if (!model) return null;
  if (model.card && model.edit && !compact) {
    return <ModelCard node={node} model={model} selected={!!selected} />;
  }
  if (!first) return null;
  const problem = problems.find((item) => item !== null) ?? null;
  const more = model.models.length - 1;
  const title = model.models
    .map((item) =>
      [item.name, item.endpoint, item.provider].filter(Boolean).join('\n')
    )
    .join('\n');
  return (
    <SubLeaf
      node={node}
      selected={selected}
      icon={modelIcon(
        choices.find((choice) => choice.name === first.endpoint)?.preset,
        first.provider
      )}
      label={first.name}
      labelExtra={
        more > 0 ? (
          <span
            data-testid="ve-canvas-model-more"
            className="shrink-0 text-[10px] font-medium text-muted-foreground"
          >
            +{more}
          </span>
        ) : undefined
      }
      detail={first.endpoint ?? first.provider}
      title={title}
      overlays={
        problem ? (
          <ProblemMark
            text={problemText(ts, problem)}
            className="absolute -right-1 -top-1 rounded-full bg-card p-0.5"
          />
        ) : undefined
      }
      attrs={{
        'data-testid': `ve-canvas-node-${node.key}`,
        'data-shape': 'model',
      }}
    />
  );
}

export const ModelSubNode = memo(ModelSubNodeComponent);
