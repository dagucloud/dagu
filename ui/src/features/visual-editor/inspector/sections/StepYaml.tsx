// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, type ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import type {
  DagDocModel,
  DagEditOp,
  JSONSchema,
  ReferenceCandidate,
  StepView,
} from '../../contracts';
import { checkStepSchema } from '../stepSchemaCheck';
import { parseStepYaml, stepYamlFromModel } from '../stepYamlText';
import { YamlBox } from './YamlBox';

export interface StepYamlSectionProps {
  model: DagDocModel;
  step: StepView;
  schema: JSONSchema;
  readOnly: boolean;
  onEdit(op: DagEditOp): void;
  /** The references that a field can offer, by its path below the step entry. */
  candidatesAtPath(path: string[]): ReferenceCandidate[];
}

/**
 * YAML of one step in a small editor. "Apply" parses the text: a syntax
 * error, or a text that is not a mapping, shows the error and sends
 * nothing. A valid text sends one `replaceStepYaml` with the text at
 * column 0; messages of the DAG schema show as warnings and do not stop
 * the edit.
 */
export function StepYamlSection({
  model,
  step,
  schema,
  readOnly,
  onEdit,
  candidatesAtPath,
}: StepYamlSectionProps): ReactElement {
  const { ts } = useI18n();
  const original = useMemo(() => stepYamlFromModel(model, step), [model, step]);
  return (
    <YamlBox
      original={original}
      readOnly={readOnly}
      ariaLabel={ts('Step YAML')}
      check={parseStepYaml}
      candidatesAtPath={candidatesAtPath}
      warnings={(value) => checkStepSchema(schema, value)}
      onApply={(text) =>
        onEdit({ kind: 'replaceStepYaml', step: step.key, yaml: text })
      }
    />
  );
}
