import { useState } from 'react';
import {
  HiCheckCircle,
  HiXCircle,
  HiExclamationTriangle,
  HiOutlineCalendarDays,
} from 'react-icons/hi2';
import { HiX } from 'react-icons/hi';
import { useTranslation } from 'react-i18next';
import type { Service, ServiceResult } from '../../../api';
import { DateRangePicker } from '../../../components/DateRangePicker';

interface ServiceResultsModalProps {
  service: Service;
  results: ServiceResult[];
  loading: boolean;
  dateRange: { start: string; end: string };
  onDateRangeChange: (start: string, end: string) => void;
  onClose: () => void;
}

function parseDateRange(dateRange: { start: string; end: string }): { start: Date; end: Date } {
  return {
    start: new Date(dateRange.start),
    end: new Date(dateRange.end),
  };
}

function formatDateRangeLabel(range: { start: Date; end: Date }, locale: string): string {
  const opts: Intl.DateTimeFormatOptions = { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' };
  const s = range.start.toLocaleString(locale, opts);
  const e = range.end.toLocaleString(locale, opts);
  return `${s}  —  ${e}`;
}

export function ServiceResultsModal({
  service,
  results,
  loading,
  dateRange,
  onDateRangeChange,
  onClose,
}: ServiceResultsModalProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const [calendarOpen, setCalendarOpen] = useState(false);
  const dateRangeObj = parseDateRange(dateRange);

  const handleRangeChange = (range: { start: Date; end: Date }) => {
    onDateRangeChange(range.start.toISOString(), range.end.toISOString());
  };

  return (
    <div className='modal-overlay' onClick={onClose}>
      <div
        className='modal-content'
        style={{ maxWidth: '1000px', maxHeight: '85vh' }}
        onClick={(e) => e.stopPropagation()}>
        <div className='modal-header'>
          <h2 className='modal-title'>{t('metrics_view.results_modal.title', { name: service.name })}</h2>
          <button className='modal-close' onClick={onClose}>
            <HiX style={{ fontSize: '1.25rem' }} />
          </button>
        </div>

        <div
          style={{
            padding: '1rem 1.5rem',
            borderBottom: '1px solid var(--border-primary)',
            display: 'flex',
            gap: '1rem',
            alignItems: 'center',
            flexWrap: 'wrap',
          }}>
          <button
            type='button'
            className='date-range-trigger'
            onClick={() => setCalendarOpen(true)}
          >
            <HiOutlineCalendarDays />
            {formatDateRangeLabel(dateRangeObj, locale)}
          </button>
        </div>

        <DateRangePicker
          isOpen={calendarOpen}
          value={dateRangeObj}
          onChange={handleRangeChange}
          onClose={() => setCalendarOpen(false)}
          showTime
        />

        <div
          style={{
            flex: 1,
            overflowY: 'auto',
            padding: '1.5rem',
          }}>
          {loading ? (
            <div className='loading'>
              <div className='loading-spinner' />
              {t('metrics_view.results_modal.loading')}
            </div>
          ) : results.length === 0 ? (
            <div className='empty-state'>
              <div className='empty-state-title'>{t('metrics_view.results_modal.empty_title')}</div>
              <div className='empty-state-description'>
                {t('metrics_view.results_modal.empty_desc')}
              </div>
            </div>
          ) : (
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: '0.75rem',
              }}>
              {results.map((result) => (
                <div
                  key={result.id}
                  style={{
                    padding: '1rem',
                    background:
                      result.status === 'failure'
                        ? 'var(--status-error-bg)'
                        : result.status === 'warning'
                        ? 'var(--status-warning-bg)'
                        : 'var(--status-success-bg)',
                    border: `1px solid ${
                      result.status === 'failure'
                        ? 'var(--status-error-border)'
                        : result.status === 'warning'
                        ? 'var(--status-warning-border)'
                        : 'var(--status-success-border)'
                    }`,
                    borderRadius: '8px',
                  }}>
                  <div
                    style={{
                      display: 'flex',
                      gap: '1rem',
                      alignItems: 'center',
                      marginBottom: '0.5rem',
                      flexWrap: 'wrap',
                    }}>
                    <span
                      className={`status-badge ${
                        result.status === 'success'
                          ? 'status-healthy'
                          : result.status === 'warning'
                          ? 'status-degraded'
                          : 'status-down'
                      }`}>
                      {result.status === 'success' ? (
                        <>
                          <HiCheckCircle />
                          {t('metrics_view.status.success')}
                        </>
                      ) : result.status === 'warning' ? (
                        <>
                          <HiExclamationTriangle />
                          {t('metrics_view.status.warning')}
                        </>
                      ) : (
                        <>
                          <HiXCircle />
                          {t('metrics_view.status.failure')}
                        </>
                      )}
                    </span>
                    {result.latency && (
                      <span
                        style={{
                          color: 'var(--brand-primary)',
                          fontSize: '0.875rem',
                          fontWeight: 500,
                        }}>
                        {result.latency.toFixed(2)}ms
                      </span>
                    )}
                    <span
                      style={{
                        color: 'var(--text-tertiary)',
                        fontSize: '0.8125rem',
                        fontFamily: 'monospace',
                      }}>
                      {new Date(result.timestamp).toLocaleString(locale)}
                    </span>
                  </div>
                  {(() => {
                    let certExpirationDate: string | null = null;
                    if (service.type === 'certificate' && result.metadata) {
                      try {
                        const metadata = JSON.parse(result.metadata);
                        if (metadata.expires_at) {
                          certExpirationDate = new Date(metadata.expires_at).toLocaleDateString(
                            locale,
                            {
                              day: 'numeric',
                              month: 'long',
                              year: 'numeric',
                              hour: '2-digit',
                              minute: '2-digit',
                            }
                          );
                        }
                      } catch {
                        // Invalid JSON, ignore
                      }
                    }
                    return (
                      <>
                        {result.message && (
                          <div style={{ color: 'var(--text-secondary)', fontSize: '0.875rem' }}>
                            {result.message}
                          </div>
                        )}
                        {certExpirationDate && (
                          <div style={{ color: 'var(--text-secondary)', fontSize: '0.875rem', marginTop: result.message ? '0.25rem' : 0 }}>
                            {t('metrics_view.results_modal.expires_at', { date: certExpirationDate })}
                          </div>
                        )}
                      </>
                    );
                  })()}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
