// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowDown, ArrowUp, Trash2 } from 'lucide-react';
import { useState, type ReactElement } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import { SELECT_CLASS } from '../RunEditor';
import type { ChatSectionProps } from './ModelSection';
import {
  AddButton,
  ChatField,
  makeSetField,
  ReferenceField,
  YamlOnlyValue,
} from './fields';
import { MESSAGE_ROLES, readMessages, withOf } from './model';

/** Lines that the system message box shows before the user drags it taller. */
const SYSTEM_ROWS = 8;
/** Lines that the prompt box shows before the user drags it taller. */
const PROMPT_ROWS = 10;
const MESSAGE_ROWS = 5;

/** The system message of a chat step: `with.system`, in a large box. */
export function SystemSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  candidates,
  rows = SYSTEM_ROWS,
}: ChatSectionProps & { rows?: number }): ReactElement {
  const { ts } = useI18n();
  const system = withOf(step.value).system;
  if (system !== undefined && typeof system !== 'string') {
    return <YamlOnlyValue name="system" value={system} />;
  }
  return (
    <ReferenceField
      label={ts('System message')}
      help={ts(
        'Instructions that set the role and the rules of the model. It runs before the prompt.'
      )}
      value={system ?? ''}
      candidates={candidates}
      readOnly={readOnly}
      multiline
      rows={rows}
      resizable
      highlight
      issues={issuesUnder(fieldIssues, 'with.system')}
      path="with.system"
      onCommit={(text) =>
        onEdit({
          kind: 'setStepField',
          step: step.key,
          path: ['with', 'system'],
          value: text,
        })
      }
    />
  );
}

/**
 * The prompt of a chat step: one large box for `with.prompt`, or the list
 * of `with.messages` (a role and a text in each row). The step takes the
 * prompt first, so a step that has both shows the prompt and a note. An
 * agent step has no `messages` key: with `messages` false the section is
 * the box of the prompt only.
 */
export function PromptSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  candidates,
  messages: withMessages = true,
  rows = PROMPT_ROWS,
}: ChatSectionProps & { messages?: boolean; rows?: number }): ReactElement {
  const { ts } = useI18n();
  const set = makeSetField(step.key, onEdit);
  const withMap = withOf(step.value);
  const prompt = withMap.prompt;
  const rawMessages = withMap.messages;
  const messages = readMessages(rawMessages);
  const hasPrompt = typeof prompt === 'string' && prompt !== '';
  const hasMessages = messages !== null && messages.length > 0;
  const [mode, setMode] = useState<'prompt' | 'messages'>(
    hasMessages && !hasPrompt ? 'messages' : 'prompt'
  );
  const last = (messages?.length ?? 0) - 1;
  const replace = (next: unknown[]) =>
    set(['with', 'messages'], next.length === 0 ? undefined : next);
  const list = Array.isArray(rawMessages) ? rawMessages : [];
  const move = (from: number, to: number) => {
    const next = [...list];
    [next[from], next[to]] = [next[to], next[from]];
    replace(next);
  };
  const promptField =
    prompt !== undefined && typeof prompt !== 'string' ? (
      <YamlOnlyValue name="prompt" value={prompt} />
    ) : (
      <ReferenceField
        label={ts('Prompt')}
        help={ts(
          'The request that you send to the model. Use + to insert a value from another step.'
        )}
        value={prompt ?? ''}
        candidates={candidates}
        readOnly={readOnly}
        multiline
        rows={rows}
        resizable
        highlight
        required={!hasMessages}
        issues={issuesUnder(fieldIssues, 'with.prompt')}
        path="with.prompt"
        onCommit={(text) => set(['with', 'prompt'], text)}
      />
    );
  if (!withMessages) {
    return (
      <div className="space-y-2">
        <FieldIssues issues={fieldIssues.with} />
        {promptField}
      </div>
    );
  }
  return (
    <div className="space-y-2">
      <FieldIssues issues={fieldIssues.with} />
      <ToggleGroup aria-label={ts('Form of the prompt')}>
        <ToggleButton
          value="prompt"
          groupValue={mode}
          className="h-6 px-2"
          onClick={() => setMode('prompt')}
        >
          <I18nText text="One prompt" />
        </ToggleButton>
        <ToggleButton
          value="messages"
          groupValue={mode}
          className="h-6 px-2"
          onClick={() => setMode('messages')}
        >
          <I18nText text="Messages" />
        </ToggleButton>
      </ToggleGroup>
      {hasPrompt && hasMessages && (
        <p className="text-[11px] text-muted-foreground">
          <I18nText text="This step has a prompt and messages. The step uses the prompt, not the messages." />
        </p>
      )}
      {mode === 'prompt' ? (
        promptField
      ) : (
        <ChatField
          label={ts('Messages')}
          help={ts(
            'A conversation that starts the model. The step sends the rows in order.'
          )}
          issues={issuesUnder(fieldIssues, 'with.messages')}
          path="with.messages"
        >
          {rawMessages !== undefined && messages === null ? (
            <YamlOnlyValue name="messages" value={rawMessages} />
          ) : (
            <>
              <ol className="space-y-2">
                {(messages ?? []).map((message, index) => {
                  const title = ts('Message {index}', { index: index + 1 });
                  return (
                    <li
                      key={index}
                      data-testid="ve-message-row"
                      data-field-path={`with.messages[${index}]`}
                      className="space-y-1 rounded border border-border p-1.5"
                    >
                      <div className="flex items-center gap-1">
                        <select
                          aria-label={ts('Role of {name}', { name: title })}
                          className={cn(SELECT_CLASS, 'w-28')}
                          value={message.role}
                          disabled={readOnly}
                          onChange={(event) =>
                            set(
                              ['with', 'messages', index, 'role'],
                              event.target.value
                            )
                          }
                        >
                          {!(MESSAGE_ROLES as readonly string[]).includes(
                            message.role
                          ) && (
                            <option value={message.role}>{message.role}</option>
                          )}
                          {MESSAGE_ROLES.map((role) => (
                            <option key={role} value={role}>
                              {role}
                            </option>
                          ))}
                        </select>
                        <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">
                          {title}
                        </span>
                        {!readOnly && (
                          <>
                            <IconButton
                              size="sm"
                              label={ts('Move {name} up', { name: title })}
                              icon={ArrowUp}
                              disabled={index === 0}
                              onClick={() => move(index, index - 1)}
                            />
                            <IconButton
                              size="sm"
                              label={ts('Move {name} down', { name: title })}
                              icon={ArrowDown}
                              disabled={index === last}
                              onClick={() => move(index, index + 1)}
                            />
                            <IconButton
                              size="sm"
                              tone="destructive"
                              label={ts('Remove {item}', { item: title })}
                              icon={Trash2}
                              onClick={() =>
                                replace(
                                  list.filter(
                                    (_, position) => position !== index
                                  )
                                )
                              }
                            />
                          </>
                        )}
                      </div>
                      <ReferenceField
                        label={ts('Text of {name}', { name: title })}
                        labelHidden
                        value={message.content}
                        candidates={candidates}
                        readOnly={readOnly}
                        multiline
                        rows={MESSAGE_ROWS}
                        resizable
                        highlight
                        issues={issuesUnder(
                          fieldIssues,
                          `with.messages[${index}]`
                        )}
                        onCommit={(text) =>
                          set(
                            ['with', 'messages', index, 'content'],
                            text ?? ''
                          )
                        }
                      />
                    </li>
                  );
                })}
              </ol>
              <div className="pt-1">
                <AddButton
                  label={ts('Add a message')}
                  disabled={readOnly}
                  onClick={() =>
                    replace([
                      ...list,
                      {
                        role: list.length === 0 ? 'user' : 'assistant',
                        content: '',
                      },
                    ])
                  }
                />
              </div>
            </>
          )}
        </ChatField>
      )}
    </div>
  );
}
