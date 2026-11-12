import { Check, ChevronDown, LoaderCircle } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Portal } from '../overlays/Portal';
import styles from './controls.module.css';

export type SelectOption<T extends string> = {
  disabled?: boolean;
  label: string;
  /** A short mark after the label in the list, as «Пример» of the demo saga. */
  mark?: string;
  value: T;
};

export interface SelectProps<T extends string> {
  disabled?: boolean;
  id?: string;
  label: string;
  labelHidden?: boolean;
  loading?: boolean;
  name?: string;
  onValueChange: (value: T) => void;
  options: readonly SelectOption<T>[];
  value: T;
}

export function Select<T extends string>({
  disabled = false,
  id,
  label,
  labelHidden = false,
  loading = false,
  name,
  onValueChange,
  options,
  value,
}: SelectProps<T>) {
  const generatedId = useId();
  const triggerId = id ?? generatedId;
  const labelId = `${triggerId}-label`;
  const listboxId = `${triggerId}-listbox`;
  const valueId = `${triggerId}-value`;
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLUListElement>(null);
  const optionRefs = useRef<Array<HTMLLIElement | null>>([]);
  const selectedIndex = options.findIndex((option) => option.value === value);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(selectedIndex);
  const blocked = disabled || loading;

  useLayoutEffect(() => {
    if (!open) return;
    const trigger = triggerRef.current;
    const menu = menuRef.current;
    if (!trigger || !menu) return;
    const place = () => {
      const rect = trigger.getBoundingClientRect();
      const viewport = document.documentElement;
      const width = Math.min(Math.max(rect.width, 180), Math.max(0, viewport.clientWidth - 16));
      menu.style.width = `${width}px`;
      const height = menu.scrollHeight + menu.offsetHeight - menu.clientHeight;
      const below = Math.max(0, viewport.clientHeight - Math.max(8, rect.bottom + 6) - 8);
      const above = Math.max(0, Math.min(rect.top - 6, viewport.clientHeight - 8) - 8);
      const upwards = height > below && above > below;
      Object.assign(menu.style, {
        left: `${Math.max(8, Math.min(rect.left, viewport.clientWidth - width - 8))}px`,
        top: `${upwards ? Math.min(rect.top - 6, viewport.clientHeight - 8) - Math.min(height, above) : Math.max(8, Math.min(rect.bottom + 6, viewport.clientHeight - 8))}px`,
        maxHeight: `${upwards ? above : below}px`,
      });
    };
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open, options]);

  useEffect(() => {
    if (!open) return;
    function dismiss(event: PointerEvent) {
      if (event.target instanceof Node && !triggerRef.current?.contains(event.target) && !menuRef.current?.contains(event.target)) setOpen(false);
    }
    document.addEventListener('pointerdown', dismiss);
    return () => document.removeEventListener('pointerdown', dismiss);
  }, [open]);

  useEffect(() => {
    if (open && activeIndex >= 0) optionRefs.current[activeIndex]?.focus();
  }, [activeIndex, open]);

  const firstEnabled = () => options.findIndex((option) => !option.disabled);
  const lastEnabled = () => options.map((option) => !option.disabled).lastIndexOf(true);
  const move = (from: number, step: -1 | 1) => {
    for (let offset = 1; offset <= options.length; offset += 1) {
      const index = (from + step * offset + options.length) % options.length;
      if (!options[index]?.disabled) return index;
    }
    return -1;
  };
  const show = (index = selectedIndex >= 0 && !options[selectedIndex]?.disabled ? selectedIndex : firstEnabled()) => {
    if (blocked || index < 0) return;
    setActiveIndex(index);
    setOpen(true);
  };
  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };
  const choose = (index: number) => {
    const option = options[index];
    if (!option || option.disabled || blocked) return;
    onValueChange(option.value);
    close();
  };

  return (
    <div className={styles.control}>
      <label className={labelHidden ? styles.labelHidden : styles.label} htmlFor={triggerId} id={labelId}>{label}</label>
      <div className={styles.selectRoot}>
        <button
          aria-busy={loading || undefined}
          aria-controls={open ? listboxId : undefined}
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-labelledby={`${labelId} ${valueId}`}
          className={styles.select}
          disabled={blocked}
          id={triggerId}
          onClick={() => (open ? close() : show())}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
              event.preventDefault();
              show(event.key === 'ArrowDown' ? firstEnabled() : lastEnabled());
            } else if (event.key === 'Home' || event.key === 'End') {
              event.preventDefault();
              show(event.key === 'Home' ? firstEnabled() : lastEnabled());
            } else if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              show();
            } else if (event.key === 'Escape' && open) {
              event.preventDefault();
              close();
            }
          }}
          ref={triggerRef}
          type="button"
        >
          <span id={valueId}>{options[selectedIndex]?.label ?? value}</span>
          {loading
            ? <LoaderCircle aria-hidden="true" className={styles.spinner} size={16} />
            : <ChevronDown aria-hidden="true" color="var(--ck-muted-foreground)" size={16} />}
        </button>
        {open ? (
          <Portal>
          <ul aria-labelledby={labelId} className={styles.selectMenu} id={listboxId} ref={menuRef} role="listbox">
            {options.map((option, index) => (
              <li
                aria-disabled={option.disabled || undefined}
                aria-selected={option.value === value}
                className={styles.selectOption}
                data-active={index === activeIndex || undefined}
                key={option.value}
                onClick={() => choose(index)}
                onKeyDown={(event) => {
                  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                    event.preventDefault();
                    setActiveIndex(move(index, event.key === 'ArrowDown' ? 1 : -1));
                  } else if (event.key === 'Home' || event.key === 'End') {
                    event.preventDefault();
                    setActiveIndex(event.key === 'Home' ? firstEnabled() : lastEnabled());
                  } else if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    choose(index);
                  } else if (event.key === 'Escape') {
                    event.preventDefault();
                    event.stopPropagation();
                    close();
                  } else if (event.key === 'Tab') {
                    close();
                  }
                }}
                ref={(node) => { optionRefs.current[index] = node; }}
                role="option"
                tabIndex={index === activeIndex && !option.disabled ? 0 : -1}
              >
                <span>{option.label}{option.mark ? <>{' '}<span className={styles.selectMark}>{option.mark}</span></> : null}</span>
                {option.value === value ? <Check aria-hidden="true" size={14} /> : null}
              </li>
            ))}
          </ul>
          </Portal>
        ) : null}
      </div>
      <input name={name} readOnly type="hidden" value={value} />
    </div>
  );
}
