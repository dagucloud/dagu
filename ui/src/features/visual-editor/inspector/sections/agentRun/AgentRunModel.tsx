// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { CLI_PROVIDERS } from '../../../agentRun/agentRun';
import { readAgentRunInfo } from '../../../canvas/chatModel';
import { issuesUnder } from '../../issues';
import { errorIssue, makeSetField, YamlOnlyValue } from '../chat/fields';
import { withOf } from '../chat/model';
import { ProviderField, type ChatSectionProps } from '../chat/ModelSection';
import { EffortField } from '../endpointModel/EffortField';
import { EndpointModelField } from '../endpointModel/EndpointModelField';
import { useEndpointBlock } from '../endpointModel/useEndpointBlock';
import { EndpointField } from '../EndpointField';

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export interface AgentRunModelProps extends ChatSectionProps {
  /** The DAG that holds the step, for the endpoints that it may use. */
  dag?: { name: string; workspace?: string | undefined } | undefined;
}

/**
 * Model of an agent step: the provider, which is required and is a CLI
 * provider, and the block `with.endpoint`: the name of the endpoint, which is
 * required and names the key that the step uses, and the model, which is
 * required free text. The model field suggests the models of the endpoint when
 * the step names one. A change of the endpoint removes a model that the new
 * endpoint does not list. The form shows the values of the step only.
 */
export function AgentRunModel({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  candidates,
  dag,
}: AgentRunModelProps): ReactElement {
  const { ts } = useI18n();
  const set = makeSetField(step.key, onEdit);
  const { provider, endpoint } = withOf(step.value);
  const edit = readAgentRunInfo(step.value).edit;
  const { row, levels, setEndpoint, commitModel, setEffort } = useEndpointBlock(
    step.key,
    edit,
    dag,
    onEdit
  );
  const issues = (key: string) => issuesUnder(fieldIssues, `with.${key}`);
  return (
    <div className="space-y-3">
      {typeof provider === 'string' || provider === undefined ? (
        <ProviderField
          value={text(provider)}
          readOnly={readOnly}
          candidates={candidates}
          issues={issues('provider')}
          onSet={(value) => set(['with', 'provider'], value)}
          providers={CLI_PROVIDERS}
          emptyLabel={ts('(choose a provider)')}
          help={ts('Required. The CLI that runs the step.')}
          required
        />
      ) : (
        <YamlOnlyValue name="provider" value={provider} />
      )}
      {edit && row ? (
        <>
          <EndpointField
            value={row.endpoint}
            readOnly={readOnly}
            dag={dag}
            issues={[
              ...(fieldIssues['with.endpoint'] ?? []),
              ...issues('endpoint.name'),
            ]}
            required
            requiredMessage={
              edit.shape === 'block'
                ? ts('agent.run needs the name of an endpoint')
                : undefined
            }
            path="with.endpoint.name"
            onSet={setEndpoint}
          />
          <EndpointModelField
            label={ts('Model')}
            help={ts('The name of the model. It can be a ${...} reference.')}
            value={row.name}
            endpoint={row.endpoint}
            cliProvider={text(provider)}
            dag={dag}
            candidates={candidates}
            readOnly={readOnly}
            issues={[
              ...issues('endpoint.model'),
              ...(row.endpoint !== '' && row.name === ''
                ? [
                    errorIssue(
                      ts(
                        'agent.run needs a model; choose one that the endpoint lists'
                      )
                    ),
                  ]
                : []),
            ]}
            path="with.endpoint.model"
            required
            onCommit={commitModel}
          />
          <EffortField
            help={ts('Optional. Default uses the level of the CLI.')}
            value={edit.effort ?? ''}
            levels={levels}
            readOnly={readOnly}
            issues={issues('endpoint.effort')}
            onSet={setEffort}
          />
        </>
      ) : (
        <YamlOnlyValue name="endpoint" value={endpoint} />
      )}
    </div>
  );
}
