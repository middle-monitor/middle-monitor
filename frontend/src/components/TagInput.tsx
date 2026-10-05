import { useRef, useState, type KeyboardEvent } from 'react';
import { HiOutlineXMark } from 'react-icons/hi2';
import './TagInput.css';

interface TagInputProps {
  /** Separator-joined string (kept in this format so it stays a drop-in
   *  replacement for a plain comma-separated text input — no backend change). */
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  /** Fired when the field loses focus, after any pending text is committed. */
  onBlur?: (value: string) => void;
  invalid?: boolean;
  separator?: string;
  id?: string;
}

/**
 * TagInput turns a separated string into editable chips: the user types a value
 * and presses Enter (or the separator) to add it; chips are removable. No manual
 * separators needed. The value in/out stays a separator-joined string.
 */
export function TagInput({
  value,
  onChange,
  placeholder,
  onBlur,
  invalid,
  separator = ',',
  id,
}: TagInputProps) {
  const tags = value.split(separator).map((s) => s.trim()).filter(Boolean);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const commit = (raw: string): string => {
    const next = [...tags];
    for (const a of raw.split(separator).map((s) => s.trim()).filter(Boolean)) {
      if (!next.includes(a)) next.push(a);
    }
    const joined = next.join(separator);
    if (joined !== value) onChange(joined);
    return joined;
  };

  const removeAt = (i: number) => onChange(tags.filter((_, idx) => idx !== i).join(separator));

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === separator) {
      if (draft.trim()) {
        e.preventDefault();
        commit(draft);
        setDraft('');
      }
    } else if (e.key === 'Backspace' && !draft && tags.length) {
      e.preventDefault();
      removeAt(tags.length - 1);
    }
  };

  const handleBlur = () => {
    let final = value;
    if (draft.trim()) {
      final = commit(draft);
      setDraft('');
    }
    onBlur?.(final);
  };

  return (
    <div
      className={`tag-input${invalid ? ' tag-input-invalid' : ''}`}
      onClick={() => inputRef.current?.focus()}
    >
      {tags.map((tag, i) => (
        <span className="tag-input-chip" key={`${tag}-${i}`}>
          {tag}
          <button
            type="button"
            className="tag-input-chip-remove"
            onClick={(e) => {
              e.stopPropagation();
              removeAt(i);
            }}
            aria-label={`remove ${tag}`}
          >
            <HiOutlineXMark />
          </button>
        </span>
      ))}
      <input
        id={id}
        ref={inputRef}
        className="tag-input-field"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={handleBlur}
        placeholder={tags.length === 0 ? placeholder : ''}
      />
    </div>
  );
}
