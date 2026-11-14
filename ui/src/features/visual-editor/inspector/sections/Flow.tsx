// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import type { SectionProps } from '../types';
import { ContinueOn } from './flow/ContinueOn';
import { Dependencies } from './flow/Dependencies';
import { EnvEditor } from './flow/EnvEditor';
import { Outputs } from './flow/Outputs';
import { Preconditions } from './flow/Preconditions';
import { Timing } from './flow/Timing';

/**
 * Flow settings of a step: dependencies (read-only), timeout and retry,
 * `continue_on`, preconditions, environment and declared outputs.
 */
export function FlowSection(props: SectionProps): ReactElement {
  return (
    <div className="space-y-3">
      <Dependencies {...props} />
      <Timing {...props} />
      <ContinueOn {...props} />
      <Preconditions {...props} />
      <EnvEditor {...props} />
      <Outputs {...props} />
    </div>
  );
}
