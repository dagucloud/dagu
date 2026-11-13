// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useId, type ReactElement, type ReactNode } from 'react';
import { useIsAdminIfSignedIn } from '@/contexts/AuthContext';
import { ENDPOINTS_PATH } from '@/features/ai-endpoints/endpoint';
import { modelNotListed } from '@/features/ai-endpoints/modelCheck';
import type { DagIdentity } from '@/features/ai-endpoints/useEndpointChoices';
import { useEndpointModels } from '@/features/ai-endpoints/useEndpointModels';
import { useI18n } from '@/i18n/I18nProvider';
import type { ReferenceCandidate, ValidationIssue } from '../../../contracts';
import { ReferenceInput } from '../../../references';
import { OpenTextLink } from '../../../tools/OpenLink';
import { ChatField } from '../chat/fields';
import { useCommittedDraft } from './useCommittedDraft';

/**
 * The Model field of a step that can name an endpoint (Spec 093, "Use in a
 * step"). It is free text that takes `${...}` references, as the other fields
 * of the form do. When the step names an endpoint, the field offers the models
 * of that endpoint as suggestions. The text is never an error here: a model
 * that the list lacks is as valid as a listed one. A muted line says that the
 * server fails the step when it starts, unless a refresh of the list finds the
 * model. A reference, and an alias of the CLI claude, never get this line.
 *
 * It keeps a draft and sends it on blur, and when the field goes away, only
 * when the text differs from `value`. `onCommit` receives undefined for an
 * empty text, so that the key goes. With no endpoint it looks and behaves as
 * the plain reference field.
 */
export function EndpointModelField({
  label,
  help,
  value,
  endpoint,
  cliProvider,
  dag,
  candidates,
  readOnly,
  placeholder,
  issues,
  path,
  required,
  onCommit,
}: {
  label: string;
  help?: ReactNode;
  value: string;
  /** The endpoint of the step, or an empty text for none. */
  endpoint: string;
  /** The CLI provider of an agent step: its aliases need no entry in the list. */
  cliProvider?: string | undefined;
  dag: DagIdentity | undefined;
  candidates: ReferenceCandidate[];
  readOnly: boolean;
  placeholder?: string;
  issues?: ValidationIssue[];
  path?: string;
  required?: boolean;
  onCommit(text: string | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const isAdmin = useIsAdminIfSignedIn();
  const { models, loaded, known, cliModelAliases } = useEndpointModels(
    endpoint,
    dag
  );
  const { draft, setDraft, commit } = useCommittedDraft({
    value,
    readOnly,
    onCommit,
  });

  let hint: ReactElement | null = null;
  if (loaded && known && models.length === 0) {
    hint = (
      <p
        data-testid="ve-model-no-list"
        className="text-[11px] text-muted-foreground"
      >
        {ts('This endpoint has no model list. Type the name of the model.')}
        {isAdmin && (
          <>
            {' '}
            <OpenTextLink page={{ name: 'AI', path: ENDPOINTS_PATH }}>
              {ts('Reload the list on the AI page')}
            </OpenTextLink>
          </>
        )}
      </p>
    );
  } else if (
    modelNotListed({
      models,
      model: draft,
      cliProvider,
      aliases: cliModelAliases,
    })
  ) {
    hint = (
      <p
        data-testid="ve-model-not-listed"
        className="text-[11px] text-muted-foreground"
      >
        {ts(
          'Not in the list of this endpoint. The step fails when it starts, unless a refresh of the list finds it.'
        )}
      </p>
    );
  }

  return (
    <ChatField
      label={label}
      htmlFor={id}
      help={help}
      issues={issues}
      path={path}
      required={required}
      empty={value === ''}
    >
      <div className="flex items-start gap-1">
        <div className="min-w-0 flex-1">
          <ReferenceInput
            id={id}
            aria-label={label}
            value={draft}
            candidates={candidates}
            disabled={readOnly}
            placeholder={placeholder}
            suggestions={models}
            suggestionsLabel={ts('Models of the endpoint')}
            onChange={setDraft}
            onBlur={commit}
          />
        </div>
      </div>
      {hint}
    </ChatField>
  );
}
