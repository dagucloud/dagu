import { cn } from '@/lib/utils';
import { X, BookOpen, RefreshCw } from 'lucide-react';
import { useSchema } from '@/contexts/SchemaContext';
import { useSchemaLookup } from '@/hooks/useSchemaLookup';
import type { JSONSchema } from '@/lib/schema-utils';
import type { YamlPathSegment } from '@/hooks/useYamlCursorPath';
import { SchemaPathBreadcrumb } from './SchemaPathBreadcrumb';
import { SchemaPropertyInfo } from './SchemaPropertyInfo';
import { NestedPropertiesTree } from './NestedPropertiesTree';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

interface SchemaDocSidebarProps {
  isOpen: boolean;
  onClose: () => void;
  path: string[];
  segments: YamlPathSegment[];
  className?: string;
  /** YAML content for context-aware schema resolution */
  yamlContent?: string;
  /** Optional document-specific schema override */
  schema?: JSONSchema | null;
}

export function SchemaDocSidebar({
  isOpen,
  onClose,
  path,
  segments,
  className,
  yamlContent,
  schema: schemaOverride,
}: SchemaDocSidebarProps) {
  const { ts } = useI18n();
  const {
    schema: baseSchema,
    loading: schemaLoading,
    error: schemaError,
    reload,
  } = useSchema();
  const activeSchema = schemaOverride ?? baseSchema;
  const hasSchemaOverride = !!schemaOverride;
  const { propertyInfo, siblingProperties, loading, error } = useSchemaLookup(
    path,
    yamlContent,
    schemaOverride
  );
  const isLoading = hasSchemaOverride ? loading : loading || schemaLoading;
  const currentError = hasSchemaOverride ? error : error || schemaError;

  if (!isOpen) {
    return null;
  }

  return (
    <div
      className={cn(
        'w-80 border-l border-border bg-background flex flex-col shrink-0 overflow-hidden',
        className
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-border bg-muted/30">
        <div className="flex items-center gap-1.5">
          <BookOpen className="w-4 h-4 text-muted-foreground" />
          <span className="text-xs font-medium text-foreground">
            <I18nText text={'Schema Docs'} />
          </span>
        </div>
        <I18nProps>
          <button
            onClick={onClose}
            className="p-1 rounded hover:bg-muted text-muted-foreground hover:text-foreground transition-colors"
            title="Close (Ctrl+Shift+D)"
          >
            <X className="w-4 h-4" />
          </button>
        </I18nProps>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-3 min-h-0">
        {/* Loading State */}
        {isLoading && (
          <div className="flex items-center justify-center py-8 text-muted-foreground">
            <RefreshCw className="w-4 h-4 animate-spin mr-2" />
            <span className="text-xs">
              <I18nText text={'Loading schema...'} />
            </span>
          </div>
        )}

        {/* Error State */}
        {currentError && !isLoading && (
          <div className="flex flex-col items-center justify-center h-full py-8 px-4">
            {/* Icon container */}
            <div className="w-12 h-12 rounded-full bg-muted/50 flex items-center justify-center mb-4">
              <BookOpen className="w-5 h-5 text-muted-foreground/60" />
            </div>

            {/* Message */}
            <p className="text-sm font-medium text-foreground mb-1">
              <I18nText text={'Schema unavailable'} />
            </p>
            <p className="text-xs text-muted-foreground text-center mb-4 max-w-[200px]">
              <I18nText
                text={
                  "Documentation couldn't be loaded. The editor still works normally."
                }
              />
            </p>

            {/* Retry button */}
            <button
              onClick={reload}
              className="
                inline-flex items-center gap-1.5
                px-3 py-1.5
                text-xs font-medium
                bg-muted hover:bg-muted/80
                text-foreground
                rounded-md
                border border-border
                transition-colors
              "
            >
              <RefreshCw className="w-3 h-3" />
              <I18nText text={'Try again'} />
            </button>
          </div>
        )}

        {/* Content */}
        {!isLoading && !currentError && (
          <>
            {/* Current Path */}
            <div className="mb-3">
              <SchemaPathBreadcrumb segments={segments} />
            </div>

            {/* Property Info */}
            {propertyInfo ? (
              <SchemaPropertyInfo propertyInfo={propertyInfo} />
            ) : path.length === 0 && activeSchema?.properties ? (
              // Root level - show all top-level properties
              <div>
                <h4 className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-2">
                  <I18nText text={'DAG Properties'} />
                </h4>
                <div className="border border-border rounded p-1">
                  <NestedPropertiesTree
                    properties={Object.fromEntries(
                      Object.entries(activeSchema.properties).map(
                        ([key, value]) => [
                          key,
                          {
                            name: key,
                            path: [key],
                            type: value.type || 'unknown',
                            description: value.description,
                            required:
                              activeSchema.required?.includes(key) || false,
                            properties: value.properties
                              ? Object.fromEntries(
                                  Object.entries(value.properties).map(
                                    ([k, v]) => [
                                      k,
                                      {
                                        name: k,
                                        path: [key, k],
                                        type: v.type || 'unknown',
                                        description: v.description,
                                        required:
                                          value.required?.includes(k) || false,
                                      },
                                    ]
                                  )
                                )
                              : undefined,
                          },
                        ]
                      )
                    )}
                    maxDepth={1}
                  />
                </div>
              </div>
            ) : (
              <div className="text-xs text-muted-foreground text-center py-4 italic">
                <I18nText
                  text={'Move cursor to a property to see documentation'}
                />
              </div>
            )}

            {/* Sibling Properties */}
            {siblingProperties.length > 0 && path.length > 0 && (
              <div className="mt-4 pt-3 border-t border-border">
                <h4 className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-1">
                  {ts('Sibling Properties ({count})', {
                    count: siblingProperties.length,
                  })}
                </h4>
                <div className="flex flex-wrap gap-1">
                  {siblingProperties
                    .filter((p) => p !== path[path.length - 1])
                    .slice(0, 20)
                    .map((prop) => (
                      <span
                        key={prop}
                        className="text-xs bg-muted text-foreground px-1.5 py-0.5 rounded"
                      >
                        {prop}
                      </span>
                    ))}
                  {siblingProperties.length > 21 && (
                    <span className="text-xs text-muted-foreground">
                      {ts('+{count} more', {
                        count: siblingProperties.length - 20,
                      })}
                    </span>
                  )}
                </div>
              </div>
            )}
          </>
        )}
      </div>

      {/* Footer */}
      <div className="px-3 py-1.5 border-t border-border bg-muted/20">
        <span className="text-xs text-muted-foreground">
          <I18nText text={'Press'} />{' '}
          <kbd className="px-1 py-0.5 bg-muted text-foreground rounded text-xs">
            <I18nText text={'Ctrl'} />
          </kbd>
          +
          <kbd className="px-1 py-0.5 bg-muted text-foreground rounded text-xs">
            <I18nText text={'Shift'} />
          </kbd>
          +
          <kbd className="px-1 py-0.5 bg-muted text-foreground rounded text-xs">
            <I18nText text={'D'} />
          </kbd>{' '}
          <I18nText text={'to toggle'} />
        </span>
      </div>
    </div>
  );
}
