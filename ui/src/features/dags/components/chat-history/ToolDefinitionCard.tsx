import { components } from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';

type ToolDefinition = components['schemas']['ToolDefinition'];

interface ToolDefinitionCardProps {
  tool: ToolDefinition;
}

interface ParameterSchema {
  type?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
}

export function ToolDefinitionCard({ tool }: ToolDefinitionCardProps) {
  const parameters = tool.parameters as {
    type?: string;
    properties?: Record<string, ParameterSchema>;
    required?: string[];
  } | undefined;

  const properties = parameters?.properties || {};
  const required = parameters?.required || [];
  const propertyEntries = Object.entries(properties);

  return (
    <div className="text-xs border-l-2 border-l-purple-500 pl-2 py-0.5">
      <div className="font-mono font-medium text-purple-500">{tool.name}</div>
      {tool.description && (
        <div className="text-muted-foreground">{tool.description}</div>
      )}
      {propertyEntries.length > 0 ? (
        <div className="mt-1 space-y-0.5">
          {propertyEntries.map(([name, schema]) => (
            <div key={name} className="font-mono text-muted-foreground">
              <span className="text-foreground">{name}</span>
              <span className="ml-1">({schema.type || <I18nText text={"any"} />})</span>
              {required.includes(name) ? (
                <span className="ml-1 text-amber-500"><I18nText text={"required"} /></span>
              ) : schema.default !== undefined ? (
                <span className="ml-1">= {JSON.stringify(schema.default)}</span>
              ) : null}
            </div>
          ))}
        </div>
      ) : (
        <div className="text-muted-foreground italic"><I18nText text={"no parameters"} /></div>
      )}
    </div>
  );
}
