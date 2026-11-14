// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, X } from 'lucide-react';
import { useEffect, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

export interface RouteTableProps {
  routes: Record<string, string[]>;
  readOnly: boolean;
  onSetTargets(pattern: string, targets: string[]): void;
}

/** Target step names of a comma-separated text, without blanks. */
export function parseTargets(text: string): string[] {
  return text
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean);
}

function TargetsInput({
  pattern,
  targets,
  readOnly,
  onSetTargets,
}: {
  pattern: string;
  targets: string[];
  readOnly: boolean;
  onSetTargets(pattern: string, targets: string[]): void;
}) {
  const { ts } = useI18n();
  const joined = targets.join(', ');
  const [draft, setDraft] = useState(joined);
  useEffect(() => setDraft(joined), [joined]);
  // A step name can hold a comma, which the text cannot tell from a
  // separator: an untouched text keeps the targets as they are.
  const commit = () => {
    if (draft === joined) {
      return;
    }
    const next = parseTargets(draft);
    if (
      next.length !== targets.length ||
      next.some((name, i) => name !== targets[i])
    ) {
      onSetTargets(pattern, next);
    }
  };
  return (
    <Input
      className="h-7 px-2 text-xs"
      aria-label={ts('Targets of {pattern}', { pattern })}
      value={draft}
      disabled={readOnly}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter') {
          commit();
        }
      }}
    />
  );
}

/**
 * Routes of a `router.route` step: one row for each pattern with its
 * target steps. An edit sends `onSetTargets(pattern, targets)`; a removed
 * row sends an empty target list. A new row is sent when it has both a
 * pattern and a target.
 */
export function RouteTable({
  routes,
  readOnly,
  onSetTargets,
}: RouteTableProps): ReactElement {
  const { ts } = useI18n();
  const [pattern, setPattern] = useState('');
  const [targets, setTargets] = useState('');
  const canAdd = pattern.trim() !== '' && parseTargets(targets).length > 0;

  const add = () => {
    if (!canAdd) {
      return;
    }
    onSetTargets(pattern.trim(), parseTargets(targets));
    setPattern('');
    setTargets('');
  };

  return (
    <table className="w-full text-xs">
      <thead>
        <tr className="text-left text-muted-foreground">
          <th className="w-1/3 py-0.5 font-medium">
            <I18nText text="Pattern" />
          </th>
          <th className="py-0.5 font-medium">
            <I18nText text="Targets" />
          </th>
          <th className="w-7" />
        </tr>
      </thead>
      <tbody>
        {Object.entries(routes).map(([key, list]) => (
          <tr key={key}>
            <td className="py-0.5 pr-1">
              {/* A pattern is the key of its route: it has the box of the
                  new row, and a change is a removed row and a new one. */}
              <Input
                readOnly
                className="h-7 px-2 font-mono text-xs"
                aria-label={ts('Pattern {pattern}', { pattern: key })}
                value={key}
              />
            </td>
            <td className="py-0.5">
              <TargetsInput
                pattern={key}
                targets={Array.isArray(list) ? list : []}
                readOnly={readOnly}
                onSetTargets={onSetTargets}
              />
            </td>
            <td>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={ts('Remove route {pattern}', { pattern: key })}
                disabled={readOnly}
                onClick={() => onSetTargets(key, [])}
              >
                <X />
              </Button>
            </td>
          </tr>
        ))}
        {!readOnly && (
          <tr>
            <td className="py-0.5 pr-1">
              <Input
                className="h-7 px-2 text-xs"
                aria-label={ts('New pattern')}
                placeholder={ts('Pattern')}
                value={pattern}
                onChange={(event) => setPattern(event.target.value)}
              />
            </td>
            <td className="py-0.5">
              <Input
                className="h-7 px-2 text-xs"
                aria-label={ts('New targets')}
                placeholder={ts('step-a, step-b')}
                value={targets}
                onChange={(event) => setTargets(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    add();
                  }
                }}
              />
            </td>
            <td>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={ts('Add route')}
                disabled={!canAdd}
                onClick={add}
              >
                <Plus />
              </Button>
            </td>
          </tr>
        )}
      </tbody>
    </table>
  );
}
