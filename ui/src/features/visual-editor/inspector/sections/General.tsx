// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { I18nText } from '@/i18n/I18nText';
import { TextField } from '../TextField';
import type { SectionProps } from '../types';

/**
 * Name, id and description of a step. A new name gives `renameStep` (the
 * document layer rewrites the references to the step), a new id gives
 * `setStepId` (an empty id removes it) and a new description gives one
 * `setStepField` (an empty description removes the key). An empty name
 * is not sent.
 */
export function GeneralSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  derivedName,
}: SectionProps): ReactElement {
  const name = step.explicitName ?? '';
  // The event gives a handler its name (onSuccess): it is not edited.
  const eventName = step.handler
    ? `on${step.handler.charAt(0).toUpperCase()}${step.handler.slice(1)}`
    : null;
  const description =
    typeof step.value.description === 'string'
      ? step.value.description
      : (step.description ?? '');

  return (
    <>
      <div data-field-path="name">
        <TextField
          label={<I18nText text="Name" />}
          value={name}
          placeholder={eventName ?? derivedName ?? step.key}
          readOnly={readOnly || (eventName !== null && name === '')}
          issues={fieldIssues.name}
          onCommit={(text) => {
            const newName = text.trim();
            if (newName && newName !== name) {
              onEdit({ kind: 'renameStep', step: step.key, newName });
            }
          }}
        />
      </div>
      <div data-field-path="id">
        <TextField
          label={<I18nText text="ID" />}
          value={step.id ?? ''}
          readOnly={readOnly}
          issues={fieldIssues.id}
          onCommit={(text) => {
            const id = text.trim();
            if (id !== (step.id ?? '')) {
              onEdit({ kind: 'setStepId', step: step.key, id: id || null });
            }
          }}
        />
      </div>
      <div data-field-path="description">
        <TextField
          label={<I18nText text="Description" />}
          value={description}
          multiline
          readOnly={readOnly}
          issues={fieldIssues.description}
          onCommit={(text) =>
            onEdit({
              kind: 'setStepField',
              step: step.key,
              path: ['description'],
              value: text.trim() ? text : undefined,
            })
          }
        />
      </div>
    </>
  );
}
