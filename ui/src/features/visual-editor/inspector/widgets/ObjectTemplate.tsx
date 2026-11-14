// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Templates } from '@rjsf/shadcn';
import type { ObjectFieldTemplateProps } from '@rjsf/utils';
import { useState, type ComponentType, type ReactElement } from 'react';

const BaseObjectFieldTemplate =
  Templates.ObjectFieldTemplate as ComponentType<ObjectFieldTemplateProps>;

function hasData(formData: unknown): boolean {
  return (
    typeof formData === 'object' &&
    formData !== null &&
    Object.values(formData).some((value) => value !== undefined)
  );
}

/**
 * Object template of the parameters form. The root object renders as in
 * the theme. A nested object renders in a collapsible block, which starts
 * closed when the object has no value.
 */
export function CollapsibleObjectFieldTemplate(
  props: ObjectFieldTemplateProps
): ReactElement {
  if (props.fieldPathId.path.length === 0) {
    return <BaseObjectFieldTemplate {...props} />;
  }
  return <NestedObjectFieldTemplate {...props} />;
}

function NestedObjectFieldTemplate(
  props: ObjectFieldTemplateProps
): ReactElement {
  const filled = hasData(props.formData);
  const [open, setOpen] = useState(filled);
  // A value that appears opens the block. A value that goes keeps it open,
  // so that clearing the last field does not hide it while the user types.
  const [seenFilled, setSeenFilled] = useState(filled);
  if (filled !== seenFilled) {
    setSeenFilled(filled);
    if (filled) {
      setOpen(true);
    }
  }
  const label =
    props.title ||
    String(props.fieldPathId.path[props.fieldPathId.path.length - 1] ?? '');
  return (
    <details
      className="rounded border border-border"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="cursor-pointer select-none px-2 py-1 text-xs font-medium text-foreground">
        {label}
      </summary>
      <div className="px-2 pb-2">
        <BaseObjectFieldTemplate {...props} title="" />
      </div>
    </details>
  );
}
