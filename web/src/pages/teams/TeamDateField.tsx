import React, { useEffect, useRef, useState } from 'react';
import { CalendarDays } from 'lucide-react';
import { DayPicker } from 'react-day-picker';
import { enUS, zhCN } from 'react-day-picker/locale';
import 'react-day-picker/style.css';
import type { Locale } from '@/types/api';

function parseIsoDate(iso: string): Date {
  const [year, month, day] = iso.split('-').map(Number);
  return new Date(year, month - 1, day);
}

function formatIsoDate(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function formatDisplay(iso: string): string {
  if (!iso) return '';
  return iso.replaceAll('-', '/');
}

export const TeamDateField: React.FC<{
  value: string;
  onChange: (value: string) => void;
  label: string;
  min?: string;
  max?: string;
  locale: Locale;
  invalid?: boolean;
  align?: 'start' | 'end';
}> = ({ value, onChange, label, min, max, locale, invalid, align = 'end' }) => {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const selected = value ? parseIsoDate(value) : undefined;

  useEffect(() => {
    if (!open) return undefined;
    const onPointer = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="team-date-field" ref={rootRef}>
      <button
        type="button"
        className="team-date-trigger"
        aria-label={label}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-invalid={invalid || undefined}
        onClick={() => setOpen((next) => !next)}
      >
        <span>{value ? formatDisplay(value) : label}</span>
        <CalendarDays size={16} aria-hidden="true" />
      </button>
      {open && (
        <div className={`team-date-popover is-${align}`} role="dialog" aria-label={label}>
          <DayPicker
            mode="single"
            locale={locale === 'en-US' ? enUS : zhCN}
            defaultMonth={selected || (max ? parseIsoDate(max) : new Date())}
            selected={selected}
            onSelect={(date) => {
              if (!date) return;
              onChange(formatIsoDate(date));
              setOpen(false);
            }}
            disabled={[
              ...(min ? [{ before: parseIsoDate(min) }] : []),
              ...(max ? [{ after: parseIsoDate(max) }] : []),
            ]}
          />
        </div>
      )}
    </div>
  );
};
