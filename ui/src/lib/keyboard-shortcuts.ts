/**
 * Utility functions for managing keyboard shortcuts and preventing conflicts
 * with text input areas.
 */

/**
 * Determines if keyboard shortcuts should be ignored based on the currently focused element.
 * This prevents shortcuts from triggering when users interact with inputs, selects, textareas,
 * contenteditable elements, or code editors.
 * 
 * @returns true if shortcuts should be ignored (user is editing text), false otherwise
 */
export function shouldIgnoreKeyboardShortcuts(): boolean {
  const activeElement = document.activeElement;
  
  if (!activeElement) {
    return false;
  }

  // Check for standard form input elements
  const tagName = activeElement.tagName.toLowerCase();
  if (tagName === 'input' || tagName === 'textarea' || tagName === 'select') {
    return true;
  }

  // Check for contenteditable elements
  if (activeElement.hasAttribute('contenteditable')) {
    const contentEditable = activeElement.getAttribute('contenteditable');
    if (contentEditable === 'true' || contentEditable === '') {
      return true;
    }
  }

  // Check for Monaco editor or other code editors
  // Monaco editor creates elements with specific classes and attributes
  if (
    activeElement.closest('[class*="monaco"]') ||
    activeElement.closest('[class*="editor"]') ||
    activeElement.closest('[data-mode-id]') ||
    activeElement.closest('.view-line') ||
    activeElement.closest('.monaco-editor') ||
    activeElement.closest('.monaco-scrollable-element') ||
    activeElement.closest('.lines-content') ||
    activeElement.closest('.view-zones') ||
    activeElement.matches('[data-mprt]') ||
    activeElement.closest('.decorationsOverviewRuler')
  ) {
    return true;
  }

  // Check if the element has a role that indicates text input
  const role = activeElement.getAttribute('role');
  if (role === 'textbox' || role === 'searchbox') {
    return true;
  }

  // Check for elements with input-related classes (common in UI libraries)
  const className = activeElement.className;
  if (
    className.includes('input') ||
    className.includes('textarea') ||
    className.includes('search') ||
    className.includes('editor')
  ) {
    return true;
  }

  return false;
}
