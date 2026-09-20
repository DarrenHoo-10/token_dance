import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties } from 'react';
import { ChevronDown, Check } from 'lucide-react';
import type { SelectOption } from './Select';

export function FilterSelect({ label, value, options, disabled, onChange }: {
  label: string; value: string; options: SelectOption[]; disabled?: boolean; onChange: (value: string) => void;
}) {
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({});
  const selected = Math.max(0, options.findIndex(option => option.value === value));
  const choose = (index: number) => { if (options[index]) onChange(options[index].value); setOpen(false); };
  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      if (!root.current) return;
      const rect = root.current.getBoundingClientRect();
      const bounds = root.current.closest('.analytics-dialog-scroll')?.getBoundingClientRect() ?? { left: 0, right: window.innerWidth };
      const width = Math.min(280, bounds.right - bounds.left - 24);
      if (width <= 0) return;
      const left = Math.max(bounds.left + 12, Math.min(rect.left, bounds.right - 12 - width)) - rect.left;
      setMenuStyle({ left, right: 'auto', width, maxWidth: width, minWidth: 0 });
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [open]);
  useEffect(() => {
    if (open) root.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView?.({ block: 'nearest' });
  }, [open, active]);
  return <div className="filter-select" ref={root} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}>
    <button type="button" role="combobox" aria-label={label} aria-expanded={open} aria-haspopup="listbox" aria-controls={open ? id : undefined} aria-activedescendant={open ? `${id}-${active}` : undefined} disabled={disabled}
      onClick={() => { setActive(selected); setOpen(!open); }} onKeyDown={event => {
        if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); setOpen(false); return; }
        if (event.key === 'Tab') { setOpen(false); return; }
        if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); if (open) choose(active); else { setActive(selected); setOpen(true); } return; }
        if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
          event.preventDefault();
          setActive(event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : !open ? selected : Math.max(0, Math.min(options.length - 1, active + (event.key === 'ArrowDown' ? 1 : -1))));
          setOpen(true);
        } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey) {
          const match = options.findIndex(option => option.label.toLocaleLowerCase().startsWith(event.key.toLocaleLowerCase()));
          if (match >= 0) { setActive(match); setOpen(true); }
        }
      }}><span>{options[selected]?.label}</span><ChevronDown size={13} aria-hidden="true" /></button>
    {open && !disabled && <ul id={id} role="listbox" aria-label={label} className="filter-options" style={menuStyle}>
      {options.map((option, index) => <li id={`${id}-${index}`} key={option.value} data-index={index} role="option" aria-selected={value === option.value} className={index === active ? 'is-active' : ''} onPointerMove={() => setActive(index)} onMouseDown={event => event.preventDefault()} onClick={() => choose(index)}>
        <span>{option.label}</span>{value === option.value && <Check size={13} aria-hidden="true" />}
      </li>)}
    </ul>}
  </div>;
}
