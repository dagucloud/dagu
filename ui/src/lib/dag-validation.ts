/**
 * DAG name validation utilities
 *
 * These rules must match the backend validation in internal/ir/validator.go
 */

// Maximum allowed length for a DAG name (must match ir.DAGNameMaxLen)
export const DAG_NAME_MAX_LEN = 40;

// Regex pattern for valid DAG names
export const DAG_NAME_PATTERN = /^[a-zA-Z0-9_.-]+$/;

// Pattern string for display
export const DAG_NAME_PATTERN_STRING = '^[a-zA-Z0-9_.-]+$';

// Error messages
export const DAG_NAME_ERROR_MESSAGES = {
  empty: 'DAG name cannot be empty',
  tooLong: `DAG name must be ${DAG_NAME_MAX_LEN} characters or less`,
  invalid: 'DAG name can only contain letters, numbers, underscores, dots, and hyphens',
  space: 'DAG name cannot contain spaces',
} as const;

/**
 * Validates a DAG name
 * @param name The DAG name to validate
 * @returns Object containing validation result and error message
 */
export function validateDAGName(name: string): { isValid: boolean; error?: string } {
  const trimmedName = name.trim();

  if (!trimmedName) {
    return { isValid: false, error: DAG_NAME_ERROR_MESSAGES.empty };
  }

  if (trimmedName.length > DAG_NAME_MAX_LEN) {
    return { isValid: false, error: DAG_NAME_ERROR_MESSAGES.tooLong };
  }

  if (!DAG_NAME_PATTERN.test(trimmedName)) {
    // Check for spaces specifically to provide better error message
    if (name.includes(' ')) {
      return { isValid: false, error: DAG_NAME_ERROR_MESSAGES.space };
    }
    return { isValid: false, error: DAG_NAME_ERROR_MESSAGES.invalid };
  }

  return { isValid: true };
}

/**
 * Quick check if a DAG name is valid
 * @param name The DAG name to check
 * @returns true if valid, false otherwise
 */
export function isValidDAGName(name: string): boolean {
  return validateDAGName(name).isValid;
}
