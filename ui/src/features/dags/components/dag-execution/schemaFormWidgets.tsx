import SelectWidget from '@rjsf/shadcn/lib/SelectWidget/SelectWidget.js';
import TextareaWidget from '@rjsf/shadcn/lib/TextareaWidget/TextareaWidget.js';
import type {
  FormContextType,
  RegistryWidgetsType,
  RJSFSchema,
  StrictRJSFSchema,
  WidgetProps,
} from '@rjsf/utils';
import {
  ariaDescribedByIds,
  descriptionId,
  enumOptionValueDecoder,
  enumOptionValueEncoder,
  enumOptionsIndexForValue,
  getOptionValueFormat,
  getTemplate,
  labelValue,
  optionId,
  schemaRequiresTrueValue,
} from '@rjsf/utils';
import {
  RadioGroup,
  RadioGroupItem,
} from '@rjsf/shadcn/lib/components/ui/radio-group.js';
import React from 'react';

import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import { autoGrowTextarea } from './textareaAutoGrow';

/**
 * Applies Dagu's card background styling to RJSF's shadcn select widget.
 */
function SchemaSelectWidget<
  T = any,
  S extends StrictRJSFSchema = RJSFSchema,
  F extends FormContextType = any,
>(props: WidgetProps<T, S, F>) {
  return <SelectWidget {...props} className={cn('bg-card', props.className)} />;
}

/**
 * Wraps RJSF's shadcn textarea so schema-backed text parameters auto-resize.
 */
function SchemaTextareaWidget<
  T = any,
  S extends StrictRJSFSchema = RJSFSchema,
  F extends FormContextType = any,
>(props: WidgetProps<T, S, F>) {
  const containerRef = React.useRef<HTMLDivElement>(null);
  const options = React.useMemo(
    () => ({ ...props.options, rows: props.options.rows ?? 1 }),
    [props.options]
  );

  React.useLayoutEffect(() => {
    // RJSF's shadcn textarea widget does not expose a ref, so the wrapper
    // applies the shared auto-grow behavior to the rendered textarea.
    const textarea = containerRef.current?.querySelector('textarea');
    if (textarea) {
      autoGrowTextarea(textarea);
    }
  }, [props.value]);

  return (
    <div
      ref={containerRef}
      onInput={(event) => {
        if (event.target instanceof HTMLTextAreaElement) {
          autoGrowTextarea(event.target);
        }
      }}
    >
      <TextareaWidget
        {...props}
        options={options}
        className={cn(
          'bg-card min-h-9 resize-none overflow-hidden',
          props.className
        )}
      />
    </div>
  );
}

/**
 * Renders boolean schema fields with the local checkbox component styling.
 */
function SchemaCheckboxWidget<
  T = any,
  S extends StrictRJSFSchema = RJSFSchema,
  F extends FormContextType = any,
>(props: WidgetProps<T, S, F>) {
  const {
    id,
    htmlName,
    value,
    disabled,
    readonly,
    label,
    hideLabel,
    schema,
    autofocus,
    options,
    onChange,
    onBlur,
    onFocus,
    registry,
    uiSchema,
    className,
  } = props;

  const required = schemaRequiresTrueValue(schema);
  const DescriptionFieldTemplate = getTemplate(
    'DescriptionFieldTemplate',
    registry,
    options
  );
  const description = options.description || schema.description;

  return (
    <div
      className={cn(
        'relative',
        (disabled || readonly) && 'cursor-not-allowed opacity-50'
      )}
      aria-describedby={ariaDescribedByIds(id)}
    >
      {!hideLabel && description && (
        <DescriptionFieldTemplate
          id={descriptionId(id)}
          description={description}
          schema={schema}
          uiSchema={uiSchema}
          registry={registry}
        />
      )}

      <div className="my-2 flex items-center gap-2 py-0.5">
        <Checkbox
          id={id}
          name={htmlName || id}
          checked={typeof value === 'undefined' ? false : Boolean(value)}
          required={required}
          disabled={disabled || readonly}
          autoFocus={autofocus}
          onCheckedChange={(checked) => onChange(Boolean(checked))}
          onBlur={() => onBlur(id, value)}
          onFocus={() => onFocus(id, value)}
          className={cn(
            'border-input bg-card shadow-sm',
            'focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-offset-1 focus-visible:ring-offset-background',
            'data-[state=checked]:border-primary data-[state=checked]:bg-primary-fill data-[state=checked]:text-primary-foreground',
            className
          )}
        />
        <Label className="leading-tight" htmlFor={id}>
          {labelValue(label, hideLabel || !label)}
        </Label>
      </div>
    </div>
  );
}

/**
 * Renders small fixed-choice schema fields as accessible radio groups.
 */
function SchemaRadioWidget<
  T = any,
  S extends StrictRJSFSchema = RJSFSchema,
  F extends FormContextType = any,
>({
  id,
  options,
  value,
  required,
  disabled,
  readonly,
  onChange,
  onBlur,
  onFocus,
  className,
}: WidgetProps<T, S, F>) {
  const { enumOptions, enumDisabled, emptyValue } = options;
  const optionValueFormat = getOptionValueFormat(options);
  const inline = Boolean(options.inline);

  const currentValue = React.useMemo(() => {
    if (!Array.isArray(enumOptions)) {
      return undefined;
    }
    const selectedIndex = enumOptionsIndexForValue(value, enumOptions);
    // Keep stale or out-of-range values visually unselected instead of
    // coercing them back into an enum option.
    if (typeof selectedIndex !== 'string') {
      return undefined;
    }
    const index = Number.parseInt(selectedIndex, 10);
    if (Number.isNaN(index) || !enumOptions[index]) {
      return undefined;
    }
    return enumOptionValueEncoder(
      enumOptions[index].value,
      index,
      optionValueFormat
    );
  }, [enumOptions, optionValueFormat, value]);

  return (
    <div className="mb-0">
      <RadioGroup
        value={currentValue}
        required={required}
        disabled={disabled || readonly}
        onValueChange={(nextValue: string) =>
          onChange(
            enumOptionValueDecoder(
              nextValue,
              enumOptions,
              optionValueFormat,
              emptyValue
            ) as T
          )
        }
        onBlur={(event: React.FocusEvent<HTMLElement>) => {
          const target = event.target as HTMLButtonElement | null;
          onBlur(
            id,
            target?.value
              ? (enumOptionValueDecoder(
                  target.value,
                  enumOptions,
                  optionValueFormat,
                  emptyValue
                ) as T)
              : value
          );
        }}
        onFocus={(event: React.FocusEvent<HTMLElement>) => {
          const target = event.target as HTMLButtonElement | null;
          onFocus(
            id,
            target?.value
              ? (enumOptionValueDecoder(
                  target.value,
                  enumOptions,
                  optionValueFormat,
                  emptyValue
                ) as T)
              : value
          );
        }}
        aria-describedby={ariaDescribedByIds(id)}
        orientation={inline ? 'horizontal' : 'vertical'}
        className={cn('flex flex-wrap', !inline && 'flex-col', className)}
      >
        {Array.isArray(enumOptions) &&
          enumOptions.map((option, index) => {
            const itemDisabled =
              Array.isArray(enumDisabled) &&
              enumDisabled.indexOf(option.value) !== -1;
            const encodedValue = enumOptionValueEncoder(
              option.value,
              index,
              optionValueFormat
            );
            const isSelected = currentValue === encodedValue;

            return (
              <div
                key={optionId(id, index)}
                className={cn(
                  'flex items-center gap-2 rounded-md border border-transparent px-2 py-1.5 transition-colors',
                  isSelected && 'border-primary/20 bg-primary/5'
                )}
              >
                <RadioGroupItem
                  value={encodedValue}
                  id={optionId(id, index)}
                  disabled={itemDisabled}
                  className={cn(
                    'bg-card shadow-sm',
                    'data-[state=checked]:border-primary data-[state=checked]:bg-primary/10',
                    'focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-offset-1 focus-visible:ring-offset-background'
                  )}
                />
                <Label
                  className={cn(
                    'leading-tight',
                    isSelected && 'font-medium text-primary'
                  )}
                  htmlFor={optionId(id, index)}
                >
                  {option.label}
                </Label>
              </div>
            );
          })}
      </RadioGroup>
    </div>
  );
}

export const schemaFormWidgets: RegistryWidgetsType = {
  CheckboxWidget: SchemaCheckboxWidget,
  RadioWidget: SchemaRadioWidget,
  SelectWidget: SchemaSelectWidget,
  TextareaWidget: SchemaTextareaWidget,
};
