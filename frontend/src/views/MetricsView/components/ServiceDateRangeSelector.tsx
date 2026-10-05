import { HiClock } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import './ServiceDateRangeSelector.css';

interface ServiceDateRangeSelectorProps {
  dateRange: string;
  customDates: { start: string; end: string } | null;
  onDateRangeChange: (range: string) => void;
  onCustomDatesChange: (start: string, end: string) => void;
}

export function ServiceDateRangeSelector({
  dateRange,
  customDates,
  onDateRangeChange,
  onCustomDatesChange,
}: ServiceDateRangeSelectorProps) {
  const { t } = useTranslation();

  return (
    <div className="service-date-range-selector">
      <div className="service-date-range-row">
        <HiClock style={{ fontSize: '0.875rem', color: 'var(--text-tertiary)' }} />
        <select
          className="service-date-range-select"
          value={dateRange}
          onChange={(e) => onDateRangeChange(e.target.value)}
          onClick={(e) => e.stopPropagation()}>
          <option value="1h">1h</option>
          <option value="6h">6h</option>
          <option value="24h">24h</option>
          <option value="7d">7j</option>
          <option value="30d">30j</option>
          <option value="custom">{t('metrics_view.date_range_selector.custom')}</option>
          <option value="all">{t('metrics_view.date_range_selector.all')}</option>
        </select>
      </div>
      {dateRange === 'custom' && (
        <div className="service-date-range-custom">
          <div className="service-date-range-custom-label">
            {t('metrics_view.date_range_selector.custom_range')}
          </div>
          <div className="service-date-range-custom-grid">
            <div>
              <label className="service-date-range-custom-label">
                {t('metrics_view.date_range_selector.start')}
              </label>
              <input
                type="datetime-local"
                className="service-date-range-custom-input"
                value={customDates?.start || ''}
                onChange={(e) =>
                  onCustomDatesChange(e.target.value, customDates?.end || '')
                }
              />
            </div>
            <div>
              <label className="service-date-range-custom-label">
                {t('metrics_view.date_range_selector.end')}
              </label>
              <input
                type="datetime-local"
                className="service-date-range-custom-input"
                value={customDates?.end || ''}
                onChange={(e) =>
                  onCustomDatesChange(customDates?.start || '', e.target.value)
                }
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
