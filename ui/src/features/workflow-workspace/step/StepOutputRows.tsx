// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { type components } from '@/api/v1/schema';
import { JsonValue } from '@/features/visual-editor/references/JsonValue';
import { formatOutputSize } from '@/features/visual-editor/run/stepRunInfo';
import { useI18n } from '@/i18n/I18nProvider';
import { toneClasses } from '@/lib/statusPalette';

type OutputValue = components['schemas']['StepOutputValue'];

const warning = toneClasses('warning');

function OutputRow({ value, prefix }: { value: OutputValue; prefix?: string }) {
  const { ts } = useI18n();
  return (
    <li className="space-y-0.5">
      <div className="flex flex-wrap items-center gap-x-2">
        <span className="font-medium text-foreground">
          {prefix && <>{ts(prefix)}: </>}
          {value.name}
        </span>
        {value.truncated && (
          <span className={warning.text}>
            {ts('truncated ({size})', { size: formatOutputSize(value.size) })}
          </span>
        )}
      </div>
      <JsonValue value={value.value} />
    </li>
  );
}

/**
 * The outputs of one step as a list: the output variable first, then the
 * named outputs. The answer of GET .../steps/{step}/outputs, loaded by the
 * caller (useStepOutputs).
 */
export function StepOutputRows({
  outputVariable,
  outputs,
}: {
  outputVariable?: OutputValue;
  outputs: readonly OutputValue[];
}) {
  return (
    <ul className="m-0 list-none space-y-1.5 p-0">
      {outputVariable && (
        <OutputRow value={outputVariable} prefix="Output variable" />
      )}
      {outputs.map((o) => (
        <OutputRow key={o.name} value={o} />
      ))}
    </ul>
  );
}
