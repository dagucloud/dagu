// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Badge } from '@/components/ui/badge';
import { cn, parseLabelParts } from '@/lib/utils';
import { ChevronDown, X } from 'lucide-react';
import * as React from 'react';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

interface LabelComboboxProps {
  selectedLabels: string[];
  onLabelsChange: (labels: string[]) => void;
  availableLabels: string[];
  placeholder?: string;
  className?: string;
}

function canonicalizeLabel(label: string) {
  const { key, value } = parseLabelParts(label);
  const normalizedKey = key.trim().toLowerCase();
  if (!normalizedKey) return '';
  if (value === null) {
    return normalizedKey;
  }
  return `${normalizedKey}=${value.trim().toLowerCase()}`;
}

function LabelCombobox({
  selectedLabels,
  onLabelsChange,
  availableLabels,
  placeholder = 'Filter by labels...',
  className,
}: LabelComboboxProps) {
  const [inputValue, setInputValue] = React.useState('');
  const [isOpen, setIsOpen] = React.useState(false);
  const [highlightedIndex, setHighlightedIndex] = React.useState(-1);
  const containerRef = React.useRef<HTMLDivElement>(null);
  const inputRef = React.useRef<HTMLInputElement>(null);

  // Filter suggestions based on input value and sort alphabetically
  const filteredSuggestions = React.useMemo(() => {
    const selectedLower = new Set(selectedLabels.map(canonicalizeLabel));
    const available = availableLabels.filter(
      (label) => !selectedLower.has(canonicalizeLabel(label))
    );

    const sortAlphabetically = (a: string, b: string) =>
      a.toLowerCase().localeCompare(b.toLowerCase());

    if (!inputValue.trim()) {
      return available.sort(sortAlphabetically);
    }

    const searchLower = inputValue.toLowerCase().trim();
    return available
      .filter((label) => label.toLowerCase().includes(searchLower))
      .sort(sortAlphabetically);
  }, [inputValue, availableLabels, selectedLabels]);

  // Reset highlighted index when suggestions change
  React.useEffect(() => {
    setHighlightedIndex(-1);
  }, [filteredSuggestions]);

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

  const addLabel = (label: string) => {
    const normalized = canonicalizeLabel(label);
    if (!normalized) return;

    // Check for duplicates (case-insensitive)
    if (selectedLabels.some((t) => canonicalizeLabel(t) === normalized)) {
      return;
    }

    onLabelsChange([...selectedLabels, normalized]);
    setInputValue('');
    setIsOpen(false);
    inputRef.current?.focus();
  };

  const removeLabel = (labelToRemove: string) => {
    onLabelsChange(selectedLabels.filter((t) => t !== labelToRemove));
    inputRef.current?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case 'Enter':
        e.preventDefault();
        if (
          highlightedIndex >= 0 &&
          highlightedIndex < filteredSuggestions.length
        ) {
          const label = filteredSuggestions[highlightedIndex];
          if (label) addLabel(label);
        } else if (inputValue.trim()) {
          addLabel(inputValue);
        }
        break;

      case 'Backspace':
        if (!inputValue && selectedLabels.length > 0) {
          const lastLabel = selectedLabels[selectedLabels.length - 1];
          if (lastLabel) removeLabel(lastLabel);
        }
        break;

      case 'Escape':
        setIsOpen(false);
        setHighlightedIndex(-1);
        break;

      case 'ArrowDown':
        e.preventDefault();
        setIsOpen(true);
        setHighlightedIndex((prev) =>
          prev < filteredSuggestions.length - 1 ? prev + 1 : 0
        );
        break;

      case 'ArrowUp':
        e.preventDefault();
        setIsOpen(true);
        setHighlightedIndex((prev) =>
          prev > 0 ? prev - 1 : filteredSuggestions.length - 1
        );
        break;

      default:
        break;
    }
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setInputValue(e.target.value);
    setIsOpen(true);
  };

  const handleInputFocus = () => {
    setIsOpen(true);
  };

  const handleSuggestionClick = (label: string) => {
    addLabel(label);
  };

  const clearAll = () => {
    onLabelsChange([]);
    setInputValue('');
    inputRef.current?.focus();
  };

  return (
    <div ref={containerRef} className={cn('relative', className)}>
      <div
        className={cn(
          'flex min-h-9 flex-wrap items-center gap-1 rounded-md border border-input bg-card px-2 py-0.5 shadow-sm cursor-text',
          'focus-within:border-ring'
        )}
        onClick={() => inputRef.current?.focus()}
      >
        {selectedLabels.map((label) => {
          const { key, value } = parseLabelParts(label);
          return (
            <Badge
              key={label}
              variant="secondary"
              className="text-xs h-6 px-2 gap-1"
            >
              {value !== null ? (
                <>
                  <span className="font-medium">{key}</span>
                  <span className="opacity-60">=</span>
                  <span>{value}</span>
                </>
              ) : (
                key
              )}
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  e.preventDefault();
                  removeLabel(label);
                }}
                onMouseDown={(e) => {
                  e.stopPropagation();
                  e.preventDefault();
                }}
                className="ml-0.5 rounded-sm hover:bg-muted-foreground/20 hover:text-destructive focus:outline-none"
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          );
        })}
        <input
          ref={inputRef}
          type="text"
          value={inputValue}
          onChange={handleInputChange}
          onKeyDown={handleKeyDown}
          onFocus={handleInputFocus}
          placeholder={selectedLabels.length === 0 ? placeholder : ''}
          role="combobox"
          aria-label={placeholder}
          aria-expanded={isOpen}
          aria-haspopup="listbox"
          aria-autocomplete="list"
          aria-controls="label-suggestions"
          className="flex-1 min-w-[80px] h-6 bg-transparent border-none outline-none text-sm placeholder:text-muted-foreground"
        />
        <div className="flex items-center gap-1 ml-auto">
          {selectedLabels.length > 0 && (
            <I18nProps>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  clearAll();
                }}
                className="p-0.5 rounded hover:bg-muted text-muted-foreground hover:text-foreground"
                title="Clear all labels"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </I18nProps>
          )}
          <ChevronDown
            className={cn(
              'h-4 w-4 text-muted-foreground transition-transform',
              isOpen && 'rotate-180'
            )}
          />
        </div>
      </div>

      {isOpen && (filteredSuggestions.length > 0 || inputValue.trim()) && (
        <div
          id="label-suggestions"
          role="listbox"
          className="absolute z-50 mt-1 w-full max-h-[200px] overflow-y-auto rounded-md border border-border bg-popover shadow-md"
        >
          {inputValue.trim() &&
            !filteredSuggestions.some(
              (t) => t.toLowerCase() === inputValue.toLowerCase().trim()
            ) && (
              <div
                className={cn(
                  'px-3 py-1.5 text-sm cursor-pointer hover:bg-muted',
                  filteredSuggestions.length === 0 && highlightedIndex === -1
                    ? 'bg-accent text-accent-foreground'
                    : ''
                )}
                onClick={() => addLabel(inputValue)}
              >
                <I18nText
                  text={'Add "{label}"'}
                  values={{ label: inputValue.trim() }}
                />
              </div>
            )}
          {filteredSuggestions.map((label, index) => (
            <div
              key={label}
              className={cn(
                'px-3 py-1.5 text-sm cursor-pointer',
                index === highlightedIndex
                  ? 'bg-accent text-accent-foreground'
                  : 'hover:bg-muted'
              )}
              onClick={() => handleSuggestionClick(label)}
              onMouseEnter={() => setHighlightedIndex(index)}
            >
              {label}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export { LabelCombobox };
