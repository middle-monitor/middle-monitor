import { useRef, type InputHTMLAttributes } from 'react';
import { HiChevronUp, HiChevronDown } from 'react-icons/hi2';
import './NumberInput.css';

type NumberInputProps = InputHTMLAttributes<HTMLInputElement>;

// Sets a controlled input's value through the native setter and dispatches a
// real `input` event so React's onChange fires with a genuine event (the
// caller's handler still sees e.target.name / e.target.value). This keeps
// NumberInput a drop-in replacement for any <input type="number">.
function setNativeValue(el: HTMLInputElement, value: string) {
  const descriptor = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
  descriptor?.set?.call(el, value);
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

/**
 * NumberInput is a drop-in for <input type="number"> with the ugly native
 * spinner replaced by custom chevron steppers. Spread the same props you'd give
 * a native input (value, onChange, name, min, max, step, className, ...).
 */
export function NumberInput({ className = '', ...props }: NumberInputProps) {
  const ref = useRef<HTMLInputElement>(null);

  const step = (dir: 1 | -1) => {
    const el = ref.current;
    if (!el || el.disabled) return;
    const stepVal = props.step != null ? Number(props.step) : 1;
    const base = el.value === '' ? 0 : Number(el.value);
    if (Number.isNaN(base)) return;
    let next = base + dir * stepVal;
    if (props.min != null && next < Number(props.min)) next = Number(props.min);
    if (props.max != null && next > Number(props.max)) next = Number(props.max);
    setNativeValue(el, String(next));
    el.focus();
  };

  return (
    <div className="number-input">
      <input ref={ref} {...props} type="number" className={`${className} number-input-field`.trim()} />
      <div className="number-input-steppers" aria-hidden>
        <button type="button" tabIndex={-1} className="number-input-step" onClick={() => step(1)}>
          <HiChevronUp />
        </button>
        <button type="button" tabIndex={-1} className="number-input-step" onClick={() => step(-1)}>
          <HiChevronDown />
        </button>
      </div>
    </div>
  );
}
