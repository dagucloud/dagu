/**
 * Utility functions for executor-specific display logic.
 *
 * @module lib/executor-utils
 */
import type { components } from '@/api/v1/schema';
import { stripAnsi } from './ansi';

/**
 * Get displayable command from executor config when step.commands is empty.
 * Returns null if no displayable command can be extracted.
 */
export function getExecutorCommand(
  step: components['schemas']['Step']
): string | null {
  const type = step.executorConfig?.type;
  const config = step.executorConfig?.config as Record<string, unknown>;

  if (!type || !config) return null;

  switch (type) {
    case 'redis': {
      if (!config.command) return null;
      const cmd = config.command as string;
      return config.key ? `${cmd} ${config.key}` : cmd;
    }
    case 's3': {
      const parts: string[] = [];
      if (config.command) {
        parts.push(String(config.command));
      }
      if (config.bucket) {
        parts.push(`s3://${config.bucket}`);
      }
      if (config.key) {
        parts.push(String(config.key));
      } else if (config.prefix) {
        parts.push(`${config.prefix}*`);
      }
      return parts.length > 0 ? parts.join(' ') : null;
    }
    case 'sql':
      return config.query ? String(config.query) : null;
    case 'http':
      return config.url ? `${config.method || 'GET'} ${config.url}` : null;
    case 'mail':
      if (config.to) return `Mail to ${config.to}`;
      return config.mailbox ? `mail: ${config.mailbox}` : null;
    case 'xlsx':
      return config.path ? `xlsx: ${config.path}` : null;
    case 'jq':
      return config.expression ? `jq: ${config.expression}` : null;
    case 'js':
      return config.input_file ? `js: ${config.input_file}` : 'js';
    case 'docker':
      return config.image ? `docker: ${config.image}` : null;
    case 'router':
      return config.value ? `route: ${config.value}` : 'router';
    case 'browser':
      return config.url ? `browser: ${config.url}` : 'browser';
    case 'computer': {
      const launch = computerLaunchCommand(config.do);
      return launch ? `computer: ${launch}` : 'computer';
    }
    default:
      return null;
  }
}

// computerLaunchCommand returns the application a computer step launches
// first, if any.
function computerLaunchCommand(operations: unknown): string | null {
  if (!Array.isArray(operations)) {
    return null;
  }
  for (const operation of operations) {
    const launch = (operation as Record<string, unknown> | null)?.launch;
    if (typeof launch === 'string') {
      return launch;
    }
    if (launch && typeof launch === 'object') {
      const command = (launch as Record<string, unknown>).command;
      if (typeof command === 'string') {
        return command;
      }
    }
  }
  return null;
}

export function getLogStepMessage(
  step: components['schemas']['Step']
): string | null {
  if (step.executorConfig?.type !== 'log') {
    return null;
  }

  const config = step.executorConfig.config as Record<string, unknown>;
  return getLogMessageFromConfig(config);
}

export function getLogMessageFromConfig(
  config?: Record<string, unknown>
): string | null {
  const message = config?.message;
  return typeof message === 'string' ? message : null;
}

export function formatLogStepOutput(content: string): string {
  return stripAnsi(content)
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .replace(/\n+$/, '');
}
