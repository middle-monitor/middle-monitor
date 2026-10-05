import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  HiOutlineCheckCircle,
  HiOutlineExclamationTriangle,
  HiOutlineXCircle,
  HiOutlineQuestionMarkCircle,
  HiOutlineWrenchScrewdriver,
} from 'react-icons/hi2';
import type { IconType } from 'react-icons';
import { statusApi } from '../api';
import type { StatusDay, StatusIncident, StatusLevel, StatusPage } from '../api';
import { SiteHeader } from '../components/SiteHeader';
import { useDocumentMeta } from '../seo/useDocumentMeta';
import './StatusPageView.css';

const LEVEL_ICON: Record<StatusLevel, IconType> = {
  operational: HiOutlineCheckCircle,
  degraded: HiOutlineExclamationTriangle,
  outage: HiOutlineXCircle,
  unknown: HiOutlineQuestionMarkCircle,
};

// One entry per component per day. The API already merges consecutive flaps,
// but a component can still fail several times in a day; repeating "API was
// unavailable" eight times reads as noise, so a day rolls them into one line
// carrying the count and the span.
interface DayEntry {
  component: string;
  kind: StatusIncident['kind'];
  severity: string;
  ongoing: boolean;
  count: number;
  start: string;
  end?: string;
}

// Incident lines are grouped by the day they started, the way a status page
// reads: "what happened, and when".
function groupByDay(incidents: StatusIncident[], locale: string) {
  const days = new Map<string, Map<string, DayEntry>>();
  incidents.forEach((incident) => {
    const key = new Date(incident.started_at).toLocaleDateString(locale, {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
    });
    let bucket = days.get(key);
    if (!bucket) {
      bucket = new Map();
      days.set(key, bucket);
    }
    // Same component, same kind of failure: one line for the day.
    const id = `${incident.component}|${incident.kind}`;
    const entry = bucket.get(id);
    if (!entry) {
      bucket.set(id, {
        component: incident.component,
        kind: incident.kind,
        severity: incident.severity,
        ongoing: !incident.resolved_at,
        count: 1,
        start: incident.started_at,
        end: incident.resolved_at,
      });
      return;
    }
    entry.count += 1;
    // Incidents arrive newest first, so an older one extends the span backwards.
    if (incident.started_at < entry.start) entry.start = incident.started_at;
    if (!incident.resolved_at) entry.ongoing = true;
    else if (entry.end && incident.resolved_at > entry.end) entry.end = incident.resolved_at;
    if (incident.severity === 'critical') entry.severity = incident.severity;
  });
  return [...days.entries()].map(
    ([day, entries]) => [day, [...entries.values()]] as const
  );
}

// An amber bar is a short outage, not a slow spell: warnings leave a day green,
// so "degraded" here always means the check failed for a while.
function dayKind(day: StatusDay) {
  return day.status === 'degraded' ? 'partial' : day.status;
}

// Incidents the visitor would expect to see behind a bar: same component, and
// started on the day being hovered.
function relatedIncidents(
  incidents: StatusIncident[],
  component: string,
  date: string
) {
  return incidents.filter(
    (incident) =>
      incident.component === component && incident.started_at.slice(0, 10) === date
  );
}

export default function StatusPageView() {
  const { t, i18n } = useTranslation();
  useDocumentMeta('status');
  const [page, setPage] = useState<StatusPage | null>(null);
  const [failed, setFailed] = useState(false);
  const [hovered, setHovered] = useState<{
    component: string;
    date: string;
    above: boolean;
  } | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      statusApi
        .get()
        .then((res) => {
          if (!cancelled) {
            setPage(res.data);
            setFailed(false);
          }
        })
        .catch(() => {
          if (!cancelled) setFailed(true);
        });
    load();
    // The checks themselves run every 60s; refreshing on the same beat keeps an
    // open tab honest during an incident without polling for nothing.
    const timer = window.setInterval(load, 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  const locale = i18n.language === 'fr' ? 'fr-FR' : 'en-US';
  const time = (iso: string) =>
    new Date(iso).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' });
  // An outage that runs past midnight ends on the next day: showing the bare
  // time would read as ending before it started ("from 04:19 PM to 01:06 AM").
  const endTime = (startIso: string, endIso: string) => {
    const start = new Date(startIso);
    const end = new Date(endIso);
    if (start.toDateString() === end.toDateString()) return time(endIso);
    return end.toLocaleString(locale, {
      day: 'numeric',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  const dayLabel = (date: string) =>
    new Date(`${date}T12:00:00Z`).toLocaleDateString(locale, {
      day: 'numeric',
      month: 'long',
      year: 'numeric',
    });
  // Minutes are what the API reports; past an hour they stop being readable.
  const formatDuration = (minutes: number) =>
    minutes >= 60
      ? t('status.duration_hours', { hours: Math.floor(minutes / 60), minutes: minutes % 60 })
      : t('status.duration_minutes', { count: minutes });

  const BannerIcon = page ? LEVEL_ICON[page.status] : HiOutlineQuestionMarkCircle;

  return (
    <div className='status-page'>
      <SiteHeader active='status' />

      <main className='status-main'>
        {failed && (
          <div className='status-banner status-unknown'>
            <HiOutlineQuestionMarkCircle className='status-banner-icon' />
            <div>
              <h1>{t('status.unreachable_title')}</h1>
              <p>{t('status.unreachable_desc')}</p>
            </div>
          </div>
        )}

        {!failed && !page && <div className='status-loading'>{t('status.loading')}</div>}

        {page && (
          <>
            <div className={`status-banner status-${page.status}`}>
              <BannerIcon className='status-banner-icon' />
              <div>
                <h1>{t(`status.banner.${page.status}`)}</h1>
                <p>
                  {t('status.updated_at', { time: time(page.updated_at) })}
                </p>
              </div>
            </div>

            {page.maintenance.length > 0 && (
              <section className='status-section'>
                <h2>{t('status.maintenance_title')}</h2>
                <ul className='status-list'>
                  {page.maintenance.map((m) => (
                    <li key={`${m.name}-${m.starts_at}`} className='status-maintenance'>
                      <HiOutlineWrenchScrewdriver />
                      <div>
                        <strong>{m.name}</strong>
                        <span>
                          {new Date(m.starts_at).toLocaleString(locale)} &rarr;{' '}
                          {new Date(m.ends_at).toLocaleString(locale)}
                        </span>
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section className='status-section'>
              <div className='status-components'>
                {page.components.map((component) => {
                  const Icon = LEVEL_ICON[component.status];
                  return (
                    <article key={component.name} className='status-component'>
                      <header>
                        <span className='status-component-name'>
                          <Icon className={`status-icon status-${component.status}`} />
                          {component.name}
                        </span>
                      </header>
                      <div
                        className='status-bars'
                        role='img'
                        aria-label={t('status.uptime_label', {
                          uptime: component.uptime.toFixed(2),
                          days: page.window_days,
                        })}>
                        {component.days.map((day) => (
                          <span
                            key={day.date}
                            className={`status-bar status-${day.status}`}
                            onMouseEnter={(e) =>
                              setHovered({
                                component: component.name,
                                date: day.date,
                                // Above by default, below only when the bar sits too
                                // high for the card to fit over it.
                                above: e.currentTarget.getBoundingClientRect().top > 260,
                              })
                            }
                            onMouseLeave={() => setHovered(null)}>
                            {hovered?.component === component.name &&
                              hovered.date === day.date && (
                                <span
                                  className={`status-tooltip ${hovered.above ? 'is-above' : 'is-below'}`}
                                  role='tooltip'>
                                  <strong>{dayLabel(day.date)}</strong>
                                  <span className={`status-tooltip-state status-${day.status}`}>
                                    {t(`status.level.${dayKind(day)}`)}
                                    {day.downtime_minutes ? (
                                      <em>{formatDuration(day.downtime_minutes)}</em>
                                    ) : null}
                                  </span>
                                  {/* The chip already reads "Partial outage — 22 min";
                                      a sentence repeating it would only restate the
                                      bad news. A quiet day is the one that needs
                                      spelling out. */}
                                  {(day.status === 'operational' ||
                                    day.status === 'unknown') && (
                                    <span className='status-tooltip-line'>
                                      {t(`status.day.${day.status}`)}
                                    </span>
                                  )}
                                  {relatedIncidents(page.incidents, component.name, day.date).map(
                                    (incident) => (
                                      <span
                                        key={incident.started_at}
                                        className='status-tooltip-related'>
                                        {t(`status.incident.${incident.kind}`, {
                                          component: incident.component,
                                        })}
                                      </span>
                                    )
                                  )}
                                </span>
                              )}
                          </span>
                        ))}
                      </div>
                      <footer>
                        <span>{t('status.days_ago', { count: page.window_days })}</span>
                        <span className='status-footer-uptime'>
                          {t('status.uptime_label', {
                            uptime: component.uptime.toFixed(2),
                            days: page.window_days,
                          })}
                        </span>
                        <span>{t('status.today')}</span>
                      </footer>
                    </article>
                  );
                })}
              </div>
            </section>

            <section className='status-section'>
              <h2>{t('status.incidents_title', { days: page.window_days })}</h2>
              {page.incidents.length === 0 ? (
                <p className='status-empty'>{t('status.no_incidents')}</p>
              ) : (
                groupByDay(page.incidents, locale).map(([day, entries]) => (
                  <div key={day} className='status-incident-day'>
                    <h3>{day}</h3>
                    <ul className='status-list'>
                      {entries.map((entry) => (
                        <li key={`${entry.component}-${entry.kind}`}>
                          {/* The severity is a colour, not a second label: the
                              sentence below already says what happened. */}
                          <span
                            className={`status-dot status-severity-${entry.severity}`}
                            aria-hidden='true'
                          />
                          <div>
                            <strong>
                              {/* One wording per kind: whether it is still going is
                                  what the line underneath says ("Since 19:22"), and
                                  the dot's colour already carries the severity. */}
                              {t(`status.incident.${entry.kind}`, {
                                component: entry.component,
                              })}
                            </strong>
                            <span>
                              {entry.ongoing
                                ? t('status.incident_ongoing', { start: time(entry.start) })
                                : entry.count > 1
                                  ? t('status.incident_repeated', {
                                      count: entry.count,
                                      start: time(entry.start),
                                      end: endTime(entry.start, entry.end!),
                                    })
                                  : t('status.incident_resolved', {
                                      start: time(entry.start),
                                      end: endTime(entry.start, entry.end!),
                                    })}
                            </span>
                          </div>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))
              )}
            </section>
          </>
        )}
      </main>
    </div>
  );
}
