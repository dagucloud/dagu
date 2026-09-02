import { Calendar } from 'lucide-react';
import React, { useRef, useState, useEffect } from 'react';
import { cn } from '../../lib/utils';
import { Input } from './input';
import { I18nProps } from '@/i18n/I18nProps';

interface DateRangePickerProps extends React.HTMLAttributes<HTMLDivElement> {
  fromDate: string | undefined;
  toDate: string | undefined;
  onFromDateChange: (date: string) => void;
  onToDateChange: (date: string) => void;
  fromLabel?: string;
  toLabel?: string;
  onEnterPress?: () => void;
}

// Custom date-time input component
interface CustomDateTimeInputProps {
  value: string | undefined;
  onChange: (value: string) => void;
  id?: string;
  className?: string;
  onEnterPress?: () => void;
}

function CustomDateTimeInput({
  value,
  onChange,
  id,
  className,
  onEnterPress,
}: CustomDateTimeInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const hiddenDateInputRef = useRef<HTMLInputElement>(null);
  const [displayValue, setDisplayValue] = useState('');
  const [cursorPosition, setCursorPosition] = useState(0);

  // Format date for display
  useEffect(() => {
    if (value) {
      // Convert from YYYY-MM-DDTHH:mm to YYYY-MM-DD HH:mm:ss
      const date = new Date(value);
      if (!isNaN(date.getTime())) {
        const year = date.getFullYear();
        const month = String(date.getMonth() + 1).padStart(2, '0');
        const day = String(date.getDate()).padStart(2, '0');
        const hours = String(date.getHours()).padStart(2, '0');
        const minutes = String(date.getMinutes()).padStart(2, '0');
        const seconds = String(date.getSeconds()).padStart(2, '0');
        setDisplayValue(
          `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
        );
      }
    } else {
      setDisplayValue('');
    }
  }, [value]);

  // Restore cursor position after value change
  useEffect(() => {
    if (inputRef.current && cursorPosition > 0) {
      inputRef.current.setSelectionRange(cursorPosition, cursorPosition);
    }
  }, [displayValue, cursorPosition]);

  const parseDisplayValue = (display: string): string => {
    // Convert from YYYY-MM-DD HH:mm:ss to YYYY-MM-DDTHH:mm
    const match = display.match(
      /(\d{4})-(\d{2})-(\d{2})\s+(\d{2}):(\d{2}):(\d{2})/
    );
    if (match) {
      const [, year, month, day, hour, minute] = match;
      return `${year}-${month}-${day}T${hour}:${minute}`;
    }
    return '';
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;
    setDisplayValue(newValue);
    setCursorPosition(e.target.selectionStart || 0);

    // Try to parse and update if valid
    const parsed = parseDisplayValue(newValue);
    if (parsed) {
      onChange(parsed);
    }
  };

  const adjustValue = (increment: number) => {
    const pos = inputRef.current?.selectionStart || 0;
    const dateStr = value || new Date().toISOString().slice(0, 16);
    const date = new Date(dateStr);

    if (isNaN(date.getTime())) {
      return;
    }

    // Determine which segment to adjust based on cursor position
    // Format: YYYY-MM-DD HH:mm:ss
    // Positions: 0-4 (year), 5-7 (month), 8-10 (day), 11-13 (hour), 14-16 (minute), 17-19 (second)

    if (pos <= 4) {
      date.setFullYear(date.getFullYear() + increment);
    } else if (pos <= 7) {
      date.setMonth(date.getMonth() + increment);
    } else if (pos <= 10) {
      date.setDate(date.getDate() + increment);
    } else if (pos <= 13) {
      date.setHours(date.getHours() + increment);
    } else if (pos <= 16) {
      date.setMinutes(date.getMinutes() + increment);
    } else {
      date.setSeconds(date.getSeconds() + increment);
    }

    // Format back to datetime-local string
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    const seconds = String(date.getSeconds()).padStart(2, '0');

    onChange(`${year}-${month}-${day}T${hours}:${minutes}:${seconds}`);
    setCursorPosition(pos);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      e.stopPropagation();
      onEnterPress?.();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      e.stopPropagation();
      adjustValue(1);
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      e.stopPropagation();
      adjustValue(-1);
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      // Allow normal cursor movement
      setTimeout(() => {
        setCursorPosition(inputRef.current?.selectionStart || 0);
      }, 0);
    }
  };

  const handleDatePickerChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;
    if (newValue) {
      // The native date picker gives us YYYY-MM-DDTHH:mm format
      onChange(newValue);
    }
  };

  const openDatePicker = () => {
    hiddenDateInputRef.current?.showPicker?.();
    // Fallback for browsers that don't support showPicker
    hiddenDateInputRef.current?.click();
  };

  return (
    <div className="relative flex items-center">
      <I18nProps><Input
        ref={inputRef}
        id={id}
        type="text"
        value={displayValue}
        onChange={handleInputChange}
        onKeyDown={handleKeyDown}
        onClick={() => setCursorPosition(inputRef.current?.selectionStart || 0)}
        placeholder="YYYY-MM-DD HH:mm:ss"
        className={cn(
          className,
          'w-44 font-mono text-foreground placeholder:text-muted-foreground/60 pt-1'
        )}
      /></I18nProps>
      <I18nProps><button
        type="button"
        onClick={openDatePicker}
        className="px-1 hover:bg-accent rounded-sm transition-colors"
        aria-label="Open date picker"
      >
        <Calendar className="h-4 w-4 text-muted-foreground" />
      </button></I18nProps>
      <input
        ref={hiddenDateInputRef}
        type="datetime-local"
        value={value || ''}
        onChange={handleDatePickerChange}
        className="sr-only"
        tabIndex={-1}
        aria-hidden="true"
      />
    </div>
  );
}

export function DateRangePicker({
  fromDate,
  toDate,
  onFromDateChange,
  onToDateChange,
  onEnterPress,
  className,
  ...props
}: DateRangePickerProps) {
  return (
    <div
      className={cn(
        'relative items-center flex rounded-md border border-input bg-card shadow-sm',
        className
      )}
      {...props}
    >
      <div className="flex flex-col sm:flex-row">
        <div>
          <CustomDateTimeInput
            id="fromDate"
            value={fromDate}
            onChange={onFromDateChange}
            onEnterPress={onEnterPress}
            className="border-0 shadow-none focus-visible:ring-0 text-sm py-0.5 h-9 bg-transparent"
          />
        </div>

        {/* Arrow only visible on sm screens and above */}
        <div className="hidden sm:flex items-center px-1 text-muted-foreground justify-center flex pl-3">
          →
        </div>

        <div>
          <CustomDateTimeInput
            id="toDate"
            value={toDate}
            onChange={onToDateChange}
            onEnterPress={onEnterPress}
            className="border-0 shadow-none focus-visible:ring-0 text-sm py-0.5 h-9 bg-transparent"
          />
        </div>
      </div>
    </div>
  );
}
