export const AGENT_MODEL_PROVIDER_VALUES = [
  'anthropic',
  'openai',
  'openai-codex',
  'gemini',
  'openrouter',
  'local',
  'zai',
  'opencode',
] as const;

export type AgentModelProvider = (typeof AGENT_MODEL_PROVIDER_VALUES)[number];
export type AgentModelProviderAuthMode = 'direct' | 'subscription';
export type AgentModelProviderAPIKeyMode = 'hidden' | 'required' | 'optional';

export type AgentModelProviderMeta = {
  value: AgentModelProvider;
  label: string;
  authMode: AgentModelProviderAuthMode;
  apiKeyMode: AgentModelProviderAPIKeyMode;
  modelPlaceholder: string;
  baseUrlPlaceholder?: string;
  apiKeyHelperText?: string;
};

export const AGENT_MODEL_PROVIDERS: readonly AgentModelProviderMeta[] = [
  {
    value: 'anthropic',
    label: 'Anthropic',
    authMode: 'direct',
    apiKeyMode: 'required',
    modelPlaceholder: 'claude-sonnet-4-6',
    baseUrlPlaceholder: 'https://api.anthropic.com',
  },
  {
    value: 'openai',
    label: 'OpenAI',
    authMode: 'direct',
    apiKeyMode: 'required',
    modelPlaceholder: 'gpt-5.5',
    baseUrlPlaceholder: 'https://api.openai.com/v1',
  },
  {
    value: 'openai-codex',
    label: 'OpenAI Codex',
    authMode: 'subscription',
    apiKeyMode: 'hidden',
    modelPlaceholder: 'gpt-5.5',
  },
  {
    value: 'gemini',
    label: 'Google Gemini',
    authMode: 'direct',
    apiKeyMode: 'required',
    modelPlaceholder: 'gemini-3-pro-preview',
    baseUrlPlaceholder: 'https://generativelanguage.googleapis.com/v1beta',
  },
  {
    value: 'openrouter',
    label: 'OpenRouter',
    authMode: 'direct',
    apiKeyMode: 'required',
    modelPlaceholder: 'anthropic/claude-sonnet-4-6',
    baseUrlPlaceholder: 'https://openrouter.ai/api/v1',
  },
  {
    value: 'local',
    label: 'Local',
    authMode: 'direct',
    apiKeyMode: 'optional',
    modelPlaceholder: 'llama3.2',
    baseUrlPlaceholder: 'http://localhost:11434/v1',
    apiKeyHelperText: 'Leave empty for local endpoints that do not require authentication.',
  },
  {
    value: 'zai',
    label: 'Z.AI',
    authMode: 'direct',
    apiKeyMode: 'required',
    modelPlaceholder: 'glm-5',
    baseUrlPlaceholder: 'https://api.z.ai/api/paas/v4',
  },
  {
    value: 'opencode',
    label: 'OpenCode',
    authMode: 'direct',
    apiKeyMode: 'required',
    modelPlaceholder: 'kimi-k2.6',
    baseUrlPlaceholder: 'https://opencode.ai/zen/go/v1',
    apiKeyHelperText: 'API key from opencode.ai subscription.',
  },
] as const;

const AGENT_MODEL_PROVIDER_META_MAP: Readonly<
  Record<AgentModelProvider, AgentModelProviderMeta>
> = Object.fromEntries(
  AGENT_MODEL_PROVIDERS.map((provider) => [provider.value, provider])
) as Record<AgentModelProvider, AgentModelProviderMeta>;

export function isAgentModelProvider(
  value: string
): value is AgentModelProvider {
  return (AGENT_MODEL_PROVIDER_VALUES as readonly string[]).includes(value);
}

export function getAgentModelProviderMeta(
  provider: AgentModelProvider
): AgentModelProviderMeta {
  return AGENT_MODEL_PROVIDER_META_MAP[provider];
}

export function getAgentModelProviderLabel(provider: string): string {
  if (!isAgentModelProvider(provider)) {
    return provider.charAt(0).toUpperCase() + provider.slice(1);
  }
  return getAgentModelProviderMeta(provider).label;
}
