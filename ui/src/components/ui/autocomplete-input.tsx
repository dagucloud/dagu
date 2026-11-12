// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import * as React from 'react';

interface AutocompleteInputProps {
  id?: string;
  value: string;
  onValueChange: (value: string) => void;
  suggestions: string[];
  placeholder?: string;
  'aria-label'?: string;
  className?: string;
  disabled?: boolean;
  onEnterPress?: () => void;
}

function AutocompleteInput({
  id,
  value,
  onValueChange,
  suggestions,
  placeholder,
  'aria-label': ariaLabel,
  className,
  disabled,
  onEnterPress,
}: AutocompleteInputProps) {
  const [isOpen, setIsOpen] = React.useState(false);
  // Tracks the suggestion rather than its index so live suggestion updates
  // that reorder or extend the list keep the keyboard selection.
  const [highlightedValue, setHighlightedValue] = React.useState<string | null>(
    null
  );
  const containerRef = React.useRef<HTMLDivElement>(null);
  const listboxId = React.useId();

  // Filter suggestions based on the input value and sort alphabetically
  const filteredSuggestions = React.useMemo(() => {
    const unique = [...new Set(suggestions)].filter((item) => item !== '');
    const sortAlphabetically = (a: string, b: string) =>
      a.toLowerCase().localeCompare(b.toLowerCase());

    if (!value.trim()) {
      return unique.sort(sortAlphabetically);
    }

    const searchLower = value.toLowerCase().trim();
    return unique
      .filter((item) => item.toLowerCase().includes(searchLower))
      .sort(sortAlphabetically);
  }, [value, suggestions]);

  const highlightedIndex =
    highlightedValue === null
      ? -1
      : filteredSuggestions.indexOf(highlightedValue);

  // Handle click outside to close dropdown
  React.useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const selectSuggestion = (suggestion: string) => {
    onValueChange(suggestion);
    setIsOpen(false);
    setHighlightedValue(null);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case 'Enter':
        e.preventDefault();
        if (isOpen && highlightedValue !== null && highlightedIndex >= 0) {
          selectSuggestion(highlightedValue);
        } else {
          setIsOpen(false);
          onEnterPress?.();
        }
        break;

      case 'Escape':
        setIsOpen(false);
        setHighlightedValue(null);
        break;

      case 'ArrowDown':
        if (filteredSuggestions.length === 0) {
          break;
        }
        e.preventDefault();
        setIsOpen(true);
        setHighlightedValue(
          filteredSuggestions[
            highlightedIndex < filteredSuggestions.length - 1
              ? highlightedIndex + 1
              : 0
          ] ?? null
        );
        break;

      case 'ArrowUp':
        if (filteredSuggestions.length === 0) {
          break;
        }
        e.preventDefault();
        setIsOpen(true);
        setHighlightedValue(
          filteredSuggestions[
            highlightedIndex > 0
              ? highlightedIndex - 1
              : filteredSuggestions.length - 1
          ] ?? null
        );
        break;

      default:
        break;
    }
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    onValueChange(e.target.value);
    setIsOpen(true);
    setHighlightedValue(null);
  };

  const handleInputFocus = () => {
    setIsOpen(true);
  };

  return (
    <div ref={containerRef} className="relative">
      <Input
        id={id}
        type="text"
        value={value}
        onChange={handleInputChange}
        onKeyDown={handleKeyDown}
        onFocus={handleInputFocus}
        placeholder={placeholder}
        disabled={disabled}
        role="combobox"
        aria-label={ariaLabel ?? placeholder}
        aria-expanded={isOpen && filteredSuggestions.length > 0}
        aria-haspopup="listbox"
        aria-autocomplete="list"
        aria-controls={
          isOpen && filteredSuggestions.length > 0 ? listboxId : undefined
        }
        aria-activedescendant={
          isOpen &&
          filteredSuggestions.length > 0 &&
          highlightedIndex >= 0 &&
          highlightedIndex < filteredSuggestions.length
            ? `${listboxId}-option-${highlightedIndex}`
            : undefined
        }
        autoComplete="off"
        className={className}
      />

      {isOpen && filteredSuggestions.length > 0 && (
        <div
          id={listboxId}
          role="listbox"
          className="absolute z-50 mt-1 w-full max-h-[200px] overflow-y-auto rounded-md border border-border bg-popover shadow-md"
        >
          {filteredSuggestions.map((suggestion, index) => (
            <div
              key={suggestion}
              id={`${listboxId}-option-${index}`}
              role="option"
              aria-selected={index === highlightedIndex}
              className={cn(
                'px-3 py-1.5 text-sm cursor-pointer',
                index === highlightedIndex
                  ? 'bg-accent text-accent-foreground'
                  : 'hover:bg-muted'
              )}
              onClick={() => selectSuggestion(suggestion)}
              // Keep the input's focus so selecting does not blur then
              // re-open the dropdown through onFocus.
              onMouseDown={(e) => e.preventDefault()}
              onMouseEnter={() => setHighlightedValue(suggestion)}
            >
              {suggestion}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export { AutocompleteInput };
