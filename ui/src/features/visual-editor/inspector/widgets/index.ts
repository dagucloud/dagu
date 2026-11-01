// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  RegistryFieldsType,
  RegistryWidgetsType,
  TemplatesType,
} from '@rjsf/utils';
import { schemaFormWidgets } from '@/features/dags/components/dag-execution/schemaFormWidgets';
import type { ReferenceCandidate } from '../../contracts';
import { KeyValueField } from './KeyValueWidget';
import { CollapsibleObjectFieldTemplate } from './ObjectTemplate';
import {
  ReferenceTextareaWidget,
  ReferenceTextWidget,
} from './ReferenceTextWidget';
import { StringOrMapField } from './StringOrMap';
import { FreeValueField, ValueOrReferenceField } from './ValueOrReference';

export { KeyValueEditor, KeyValueField } from './KeyValueWidget';
export type { KeyValueEditorProps } from './KeyValueWidget';
export { RouteTable, parseTargets } from './RouteTable';
export type { RouteTableProps } from './RouteTable';
export { buildUiSchema, isStringMap } from './uiSchema';
export { scalarUnionKind } from './ValueOrReference';

/** rjsf `formContext` of the parameters form. */
export interface InspectorFormContext {
  candidates: ReferenceCandidate[];
}

/** Widgets of the parameters form: the Dagu widgets with reference-aware text. */
export const inspectorWidgets: RegistryWidgetsType = {
  ...schemaFormWidgets,
  TextWidget: ReferenceTextWidget,
  TextareaWidget: ReferenceTextareaWidget,
};

/** Custom fields that `buildUiSchema` selects with `ui:field`. */
export const inspectorFields: RegistryFieldsType = {
  KeyValue: KeyValueField,
  ValueOrReference: ValueOrReferenceField,
  FreeValue: FreeValueField,
  StringOrMap: StringOrMapField,
};

/** Templates merged over `schemaFormTemplates`: nested objects collapse. */
export const inspectorTemplates: Partial<TemplatesType> = {
  ObjectFieldTemplate: CollapsibleObjectFieldTemplate,
};
