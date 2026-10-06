import { useState, useRef, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { HiChevronLeft, HiChevronRight } from 'react-icons/hi';
import { rangeForPreset } from '../../utils/dateRangePresets';
import './DateRangePicker.css';

interface DateRange {
  start: Date;
  end: Date;
  relative?: string;
}

interface Preset {
  id: string;
  getRange: () => DateRange;
}

interface DateRangePickerProps {
  value: DateRange | null;
  onChange: (range: DateRange) => void;
  onClose: () => void;
  isOpen: boolean;
  /** When true, show time inputs (HH:mm) for start and end */
  showTime?: boolean;
}

function getWeekdayLabels(locale: string): string[] {
  const formatter = new Intl.DateTimeFormat(locale, { weekday: 'short' });
  const monday = new Date(2024, 0, 1);
  return Array.from({ length: 7 }, (_, i) => {
    const d = new Date(monday);
    d.setDate(monday.getDate() + i);
    return formatter.format(d);
  });
}

function formatMonthYear(year: number, month: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { month: 'long', year: 'numeric' }).format(
    new Date(year, month, 1)
  );
}

// Every preset is relative: the provider recomputes its bounds from the id as
// time passes, so a picked "today" or "this week" never freezes.
const PRESETS: Preset[] = ['15m', '1h', '6h', '24h', 'today', 'yesterday', 'this_week', 'this_month', 'this_quarter', 'this_year', '7d', '30d', '12_months'].map((id) => ({
  id,
  getRange: () => ({ ...rangeForPreset(id)!, relative: id }),
}));

function startOfDay(d: Date): Date {
  const r = new Date(d);
  r.setHours(0, 0, 0, 0);
  return r;
}

function endOfDay(d: Date): Date {
  const r = new Date(d);
  r.setHours(23, 59, 59, 999);
  return r;
}

function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();
}

function isInRange(day: Date, start: Date | null, end: Date | null): boolean {
  if (!start || !end) return false;
  const d = startOfDay(day).getTime();
  const s = startOfDay(start).getTime();
  const e = startOfDay(end).getTime();
  return d >= s && d <= e;
}

function getDaysInMonth(year: number, month: number): number {
  return new Date(year, month + 1, 0).getDate();
}

function getCalendarDays(year: number, month: number): (Date | null)[] {
  const firstDay = new Date(year, month, 1);
  let startWeekday = firstDay.getDay(); // 0=Sun
  startWeekday = startWeekday === 0 ? 6 : startWeekday - 1; // Convert to Mon=0

  const daysInMonth = getDaysInMonth(year, month);
  const days: (Date | null)[] = [];

  // Previous month padding
  for (let i = 0; i < startWeekday; i++) {
    const prevDate = new Date(year, month, -startWeekday + i + 1);
    days.push(prevDate);
  }

  // Current month
  for (let i = 1; i <= daysInMonth; i++) {
    days.push(new Date(year, month, i));
  }

  // Next month padding (fill to 6 rows)
  const remaining = 42 - days.length;
  for (let i = 1; i <= remaining; i++) {
    days.push(new Date(year, month + 1, i));
  }

  return days;
}

function CalendarMonth({
  year,
  month,
  locale,
  selectionStart,
  selectionEnd,
  hoverDate,
  onDayClick,
  onDayHover,
  onPrev,
  onNext,
}: {
  year: number;
  month: number;
  locale: string;
  selectionStart: Date | null;
  selectionEnd: Date | null;
  hoverDate: Date | null;
  onDayClick: (d: Date) => void;
  onDayHover: (d: Date | null) => void;
  onPrev?: () => void;
  onNext?: () => void;
}) {
  const days = getCalendarDays(year, month);
  const today = startOfDay(new Date());
  const weekdayLabels = getWeekdayLabels(locale);

  const effectiveEnd = selectionEnd || hoverDate;

  return (
    <div className="drp-calendar">
      <div className="drp-calendar-header">
        {onPrev ? (
          <button className="drp-nav-btn" onClick={onPrev}><HiChevronLeft /></button>
        ) : <div className="drp-nav-spacer" />}
        <span className="drp-month-label">
          {formatMonthYear(year, month, locale)}
        </span>
        {onNext ? (
          <button className="drp-nav-btn" onClick={onNext}><HiChevronRight /></button>
        ) : <div className="drp-nav-spacer" />}
      </div>
      <div className="drp-weekdays">
        {weekdayLabels.map((d, i) => (
          <div key={i} className="drp-weekday">{d}</div>
        ))}
      </div>
      <div className="drp-days">
        {days.map((day, i) => {
          if (!day) return <div key={i} className="drp-day drp-day-empty" />;

          const isCurrentMonth = day.getMonth() === month;
          const isToday = isSameDay(day, today);
          const isStart = selectionStart && isSameDay(day, selectionStart);
          const isEnd = effectiveEnd && isSameDay(day, effectiveEnd);
          const inRange = selectionStart && effectiveEnd
            ? isInRange(day, selectionStart, effectiveEnd)
            : false;

          return (
            <button
              key={i}
              className={[
                'drp-day',
                !isCurrentMonth && 'drp-day-outside',
                isToday && 'drp-day-today',
                isStart && 'drp-day-start',
                isEnd && 'drp-day-end',
                inRange && !isStart && !isEnd && 'drp-day-in-range',
              ].filter(Boolean).join(' ')}
              onClick={() => onDayClick(day)}
              onMouseEnter={() => onDayHover(day)}
              onMouseLeave={() => onDayHover(null)}
            >
              {day.getDate()}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function formatTime(d: Date): string {
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

function parseTime(s: string): { h: number; m: number } {
  const [h, m] = s.split(':').map(Number);
  return { h: isNaN(h) ? 0 : Math.max(0, Math.min(23, h)), m: isNaN(m) ? 0 : Math.max(0, Math.min(59, m)) };
}

export default function DateRangePicker({ value, onChange, onClose, isOpen, showTime = false }: DateRangePickerProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const ref = useRef<HTMLDivElement>(null);

  const now = new Date();
  const [leftMonth, setLeftMonth] = useState(
    value ? value.start.getMonth() : now.getMonth() === 0 ? 11 : now.getMonth() - 1
  );
  const [leftYear, setLeftYear] = useState(
    value ? value.start.getFullYear() : now.getMonth() === 0 ? now.getFullYear() - 1 : now.getFullYear()
  );

  // Selection state (internal)
  const [selStart, setSelStart] = useState<Date | null>(value?.start || null);
  const [selEnd, setSelEnd] = useState<Date | null>(value?.end || null);
  // Relative id (e.g. '1h', '24h') when a preset is active, so the range keeps
  // auto-refreshing. Cleared as soon as the user makes a custom selection.
  const [selRelative, setSelRelative] = useState<string | undefined>(value?.relative);
  const [picking, setPicking] = useState<'start' | 'end'>('start');
  const [hoverDate, setHoverDate] = useState<Date | null>(null);
  const [startTime, setStartTime] = useState('00:00');
  const [endTime, setEndTime] = useState('23:59');

  // Right calendar is always leftMonth + 1
  const rightMonth = leftMonth === 11 ? 0 : leftMonth + 1;
  const rightYear = leftMonth === 11 ? leftYear + 1 : leftYear;

  // Two side-by-side months don't fit a phone, and stacking them pushed the
  // footer off screen. Below this width a single month carries both arrows.
  const [isNarrow, setIsNarrow] = useState(
    () => window.matchMedia('(max-width: 700px)').matches
  );
  useEffect(() => {
    const mq = window.matchMedia('(max-width: 700px)');
    const update = (e: MediaQueryListEvent) => setIsNarrow(e.matches);
    mq.addEventListener('change', update);
    return () => mq.removeEventListener('change', update);
  }, []);

  // Click outside to close
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose();
      }
    }
    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isOpen, onClose]);

  // Reset internal state when opened
  useEffect(() => {
    if (isOpen) {
      setSelStart(value?.start || null);
      setSelEnd(value?.end || null);
      setSelRelative(value?.relative);
      setPicking('start');
      if (value) {
        setStartTime(formatTime(value.start));
        setEndTime(formatTime(value.end));
        setLeftMonth(value.start.getMonth() === 0 ? 11 : value.start.getMonth() - 1);
        setLeftYear(value.start.getMonth() === 0 ? value.start.getFullYear() - 1 : value.start.getFullYear());
      } else {
        setStartTime('00:00');
        setEndTime('23:59');
        setLeftMonth(now.getMonth() === 0 ? 11 : now.getMonth() - 1);
        setLeftYear(now.getMonth() === 0 ? now.getFullYear() - 1 : now.getFullYear());
      }
    }
    // Seeds the picker from the current range when it opens. Depending on
    // value or now would reset the calendar under a user mid-selection.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen]);

  // Sync time inputs when selection changes (must be before early return)
  useEffect(() => {
    if (selStart) setStartTime(formatTime(selStart));
    if (selEnd) setEndTime(formatTime(selEnd));
  }, [selStart, selEnd]);

  if (!isOpen) return null;

  const handleDayClick = (day: Date) => {
    setSelRelative(undefined); // custom selection is no longer relative
    if (picking === 'start') {
      setSelStart(startOfDay(day));
      setSelEnd(null);
      setPicking('end');
    } else {
      if (selStart && day < selStart) {
        // Clicked before start → swap
        setSelEnd(endOfDay(selStart));
        setSelStart(startOfDay(day));
      } else {
        setSelEnd(endOfDay(day));
      }
      setPicking('start');
    }
  };

  const handlePreset = (preset: Preset) => {
    const range = preset.getRange();
    setSelStart(range.start);
    setSelEnd(range.end);
    setSelRelative(range.relative);
    setStartTime(formatTime(range.start));
    setEndTime(formatTime(range.end));
    setPicking('start');

    // Adjust calendar view
    const m = range.start.getMonth();
    setLeftMonth(m === 0 ? 11 : m - 1);
    setLeftYear(m === 0 ? range.start.getFullYear() - 1 : range.start.getFullYear());
  };

  const handleConfirm = () => {
    if (selStart && selEnd) {
      const start = new Date(selStart);
      const end = new Date(selEnd);
      if (showTime) {
        const { h: sh, m: sm } = parseTime(startTime);
        const { h: eh, m: em } = parseTime(endTime);
        start.setHours(sh, sm, 0, 0);
        end.setHours(eh, em, 59, 999);
      }
      onChange({ start, end, relative: selRelative });
      onClose();
    }
  };

  const navigateLeft = (delta: number) => {
    let newMonth = leftMonth + delta;
    let newYear = leftYear;
    if (newMonth < 0) { newMonth = 11; newYear--; }
    if (newMonth > 11) { newMonth = 0; newYear++; }
    setLeftMonth(newMonth);
    setLeftYear(newYear);
  };

  return (
    <div className="drp-overlay">
      <div className="drp-container" ref={ref}>
        {/* Presets */}
        <div className="drp-presets">
          {PRESETS.map((p) => (
            <button
              key={p.id}
              className="drp-preset-btn"
              onClick={() => handlePreset(p)}
            >
              {t(`date_range.presets.${p.id}`)}
            </button>
          ))}
        </div>

        {/* Calendars */}
        <div className="drp-calendars">
          {!isNarrow && (
            <CalendarMonth
              year={leftYear}
              month={leftMonth}
              locale={locale}
              selectionStart={selStart}
              selectionEnd={selEnd}
              hoverDate={picking === 'end' ? hoverDate : null}
              onDayClick={handleDayClick}
              onDayHover={setHoverDate}
              onPrev={() => navigateLeft(-1)}
            />
          )}
          <CalendarMonth
            year={rightYear}
            month={rightMonth}
            locale={locale}
            selectionStart={selStart}
            selectionEnd={selEnd}
            hoverDate={picking === 'end' ? hoverDate : null}
            onDayClick={handleDayClick}
            onDayHover={setHoverDate}
            onPrev={isNarrow ? () => navigateLeft(-1) : undefined}
            onNext={() => navigateLeft(1)}
          />
        </div>

        {/* Time inputs (when showTime) */}
        {showTime && selStart && selEnd && (
          <div className="drp-time-row">
            <label className="drp-time-label">
              <span>{t('date_range.time_from')}</span>
              <input
                type="time"
                className="drp-time-input"
                value={startTime}
                onChange={(e) => { setStartTime(e.target.value); setSelRelative(undefined); }}
              />
            </label>
            <label className="drp-time-label">
              <span>{t('date_range.time_to')}</span>
              <input
                type="time"
                className="drp-time-input"
                value={endTime}
                onChange={(e) => { setEndTime(e.target.value); setSelRelative(undefined); }}
              />
            </label>
          </div>
        )}

        {/* Footer */}
        <div className="drp-footer">
          <button className="drp-cancel-btn" onClick={onClose}>
            {t('common.cancel')}
          </button>
          <button
            className="drp-confirm-btn"
            onClick={handleConfirm}
            disabled={!selStart || !selEnd}
          >
            {t('date_range.select')}
          </button>
        </div>
      </div>
    </div>
  );
}
