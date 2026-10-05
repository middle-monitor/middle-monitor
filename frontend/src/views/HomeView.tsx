import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  HiBolt,
  HiOutlineExclamationTriangle,
  HiOutlineSignal,
  HiOutlineServerStack,
  HiOutlinePlay,
  HiArrowRight,
  HiOutlineChartBar,
  HiOutlineCommandLine,
  HiOutlineShieldCheck,
  HiOutlineGlobeAlt,
  HiOutlineSquares2X2,
  HiOutlineCalendarDays,
  HiOutlineFire,
  HiOutlineEnvelope,
  HiOutlineLink,
} from 'react-icons/hi2';
import {
  SiGo,
  SiNodedotjs,
  SiPython,
  SiRust,
  SiSlack,
  SiJira,
  SiWhatsapp,
} from 'react-icons/si';
import type { IconType } from 'react-icons';
import { useAuth } from '../contexts/AuthContext';
import { SiteHeader } from '../components/SiteHeader';
import { isDemoMode } from '../demo/demoMode';
import { useDocumentMeta } from '../seo/useDocumentMeta';
import './HomeView.css';

export default function HomeView() {
  const { t } = useTranslation();
  useDocumentMeta('home');
  const { hasStoredSession, organization } = useAuth();
  // A demo session makes AuthContext report "authenticated"; the marketing page
  // must keep showing its signup CTAs instead of a dashboard shortcut.
  const hasAccount = hasStoredSession && !isDemoMode();
  const dashboardUrl = organization?.slug
    ? `/organizations/${organization.slug}`
    : '/login';
  const previewRef = useRef<HTMLElement>(null);
  const [previewVisible, setPreviewVisible] = useState(false);
  const [activeTab, setActiveTab] = useState(0);

  // Tab labels are translated, but we keep the technical id for the window title.
  const TABS = [
    {
      id: 'traces',
      label: t('home.platform.tab_traces'),
      color: 'var(--mm-brand-purple)',
    },
    {
      id: 'metrics',
      label: t('home.platform.tab_metrics'),
      color: 'var(--mm-brand-cyan)',
    },
    {
      id: 'errors',
      label: t('home.platform.tab_errors'),
      color: 'var(--mm-brand-pink)',
    },
    {
      id: 'logs',
      label: t('home.platform.tab_logs'),
      color: 'var(--mm-brand-amber)',
    },
  ];

  // SDK snippets are code samples — kept untranslated on purpose. Package names
  // and the global API mirror the SDK_REPOS table in DocumentationView.
  const SDK_LANGS = [
    {
      name: 'Go',
      icon: SiGo,
      lines: [
        '$ go get github.com/middle-monitor/sdk-go',
        'e.Use(middlemonitor.EchoMiddleware())',
      ],
    },
    {
      name: 'Node.js',
      icon: SiNodedotjs,
      lines: ['$ npm install @middle-monitor/sdk', 'reportError(error)'],
    },
    {
      name: 'Python',
      icon: SiPython,
      lines: ['$ pip install middle-monitor-sdk', 'report_error(e)'],
    },
    {
      name: 'Rust',
      icon: SiRust,
      lines: [
        '# Cargo.toml',
        '[dependencies]',
        'middle-monitor-sdk = { version = "0.1", features = ["axum"] }',
      ],
    },
  ] satisfies Array<{ name: string; icon: IconType; lines: string[] }>;

  const [sdkLangIndex, setSdkLangIndex] = useState(0);

  // Delivery channels the backend actually implements (see services/notification.go).
  const CHANNELS = [
    { name: 'Email', icon: HiOutlineEnvelope },
    { name: 'Slack', icon: SiSlack },
    { name: 'Jira', icon: SiJira },
    { name: 'WhatsApp', icon: SiWhatsapp },
    { name: 'Webhook', icon: HiOutlineLink },
  ] satisfies Array<{ name: string; icon: IconType }>;

  // Grouped by category (APM, error tracking, infra checks, logs, uptime) so
  // each answer stays honest and specific rather than a flat brand-name list.
  const FAQ_ITEMS = [1, 2, 3, 4, 5, 6, 7, 8].map((n) => ({
    q: t(`home.faq.q${n}`),
    a: t(`home.faq.a${n}`),
  }));

  useEffect(() => {
    const el = previewRef.current;
    if (!el) return;
    const obs = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) setPreviewVisible(true);
        });
      },
      { threshold: 0.1, rootMargin: '0px 0px -50px 0px' },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, []);

  return (
    <div className='mm-home'>
      {/* Dynamic Background */}
      <div className='mm-bg-grid' />
      <div className='mm-bg-glow mm-glow-purple' />
      <div className='mm-bg-glow mm-glow-cyan' />

      {/* Navigation */}
      <SiteHeader active='home' />

      {/* Hero Section */}
      <section className='mm-hero'>
        <div className='mm-container'>
          <div className='mm-hero-content'>
            <div className='mm-hero-badge'>
              <span className='mm-hero-badge-dot'></span>
              {t('home.hero.badge')}
            </div>
            <h1 className='mm-hero-title'>
              {t('home.hero.title_1')}{' '}
              <span className='mm-text-gradient'>{t('home.hero.title_2')}</span>
              <br />
              {t('home.hero.title_3')}
            </h1>
            <p className='mm-hero-subtitle'>{t('home.hero.subtitle')}</p>
            <div className='mm-hero-ctas'>
              {!hasAccount ? (
                <>
                  <Link
                    to='/demo'
                    className='mm-btn mm-btn-primary mm-btn-lg plausible-event-name=Demo+Open'>
                    <HiOutlinePlay className='mm-btn-icon-left' />{' '}
                    {t('home.hero.cta_demo')}
                  </Link>
                  <Link
                    to='/get-started'
                    className='mm-btn mm-btn-outline mm-btn-lg plausible-event-name=Signup+Start'>
                    {t('home.hero.cta_start')}
                  </Link>
                </>
              ) : (
                <Link
                  to={dashboardUrl}
                  className='mm-btn mm-btn-primary mm-btn-lg'>
                  {t('home.hero.cta_dashboard')}{' '}
                  <HiArrowRight className='mm-btn-icon-right' />
                </Link>
              )}
            </div>
          </div>

          {/* Hero Visual - Isometric Dashboard (d/dy inspired) */}
          <div className='mm-hero-visual'>
            <div className='mm-isometric-container'>
              <div className='mm-iso-plane mm-iso-layer-1'>
                <div className='mm-iso-card mm-iso-chart'>
                  <div className='mm-iso-chart-line'></div>
                </div>
                <div className='mm-iso-card mm-iso-stats'>
                  <div className='mm-iso-stat-item'>
                    <div
                      className='mm-iso-stat-bar'
                      style={{
                        width: '80%',
                        background: 'var(--mm-brand-purple)',
                      }}></div>
                  </div>
                  <div className='mm-iso-stat-item'>
                    <div
                      className='mm-iso-stat-bar'
                      style={{
                        width: '40%',
                        background: 'var(--mm-brand-pink)',
                      }}></div>
                  </div>
                  <div className='mm-iso-stat-item'>
                    <div
                      className='mm-iso-stat-bar'
                      style={{
                        width: '60%',
                        background: 'var(--mm-brand-cyan)',
                      }}></div>
                  </div>
                </div>
              </div>
              <div className='mm-iso-plane mm-iso-layer-2'>
                <div className='mm-iso-card mm-iso-alert'>
                  <HiOutlineExclamationTriangle className='mm-iso-alert-icon' />
                  <div className='mm-iso-alert-text'>
                    {t('home.hero.iso_alert')}
                  </div>
                </div>
                <div className='mm-iso-card mm-iso-logs'>
                  <div className='mm-iso-log-line'>
                    {t('home.hero.iso_log_info_auth')}
                  </div>
                  <div className='mm-iso-log-line mm-log-err'>
                    {t('home.hero.iso_log_err_db')}
                  </div>
                  <div className='mm-iso-log-line'>
                    {t('home.hero.iso_log_info_payment')}
                  </div>
                </div>
              </div>
              <div className='mm-iso-plane mm-iso-layer-3'>
                <div className='mm-iso-card mm-iso-trace'>
                  <div className='mm-iso-trace-node'>Gateway</div>
                  <div className='mm-iso-trace-node mm-iso-trace-child'>
                    Auth Service
                  </div>
                  <div className='mm-iso-trace-node mm-iso-trace-child mm-trace-err'>
                    Payment Service
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Trust Banner */}
      <section className='mm-trust'>
        <p className='mm-trust-text'>{t('home.trust.text')}</p>
        <div className='mm-trust-logos'>
          <span className='mm-trust-logo'>Go</span>
          <span className='mm-trust-logo'>Node.js</span>
          <span className='mm-trust-logo'>Python</span>
          <span className='mm-trust-logo'>Rust</span>
          <span className='mm-trust-logo'>Docker</span>
          <span className='mm-trust-logo'>Kubernetes</span>
        </div>
      </section>

      {/* Platform Section (Interactive Tabs) */}
      <section id='platform' className='mm-platform' ref={previewRef}>
        <div className='mm-container'>
          <div className='mm-section-header'>
            <h2 className='mm-section-title'>
              {t('home.platform.section_title_1')}{' '}
              <span className='mm-text-gradient-alt'>
                {t('home.platform.section_title_2')}
              </span>
            </h2>
            <p className='mm-section-subtitle'>
              {t('home.platform.section_subtitle')}
            </p>
          </div>

          <div
            className={`mm-platform-showcase ${previewVisible ? 'is-visible' : ''}`}>
            <div className='mm-platform-tabs'>
              {TABS.map((tab, idx) => (
                <button
                  key={tab.id}
                  className={`mm-platform-tab ${activeTab === idx ? 'active' : ''}`}
                  onClick={() => setActiveTab(idx)}
                  style={{ '--tab-color': tab.color } as React.CSSProperties}>
                  {tab.label}
                </button>
              ))}
            </div>
            <div className='mm-platform-window'>
              <div className='mm-window-header'>
                <div className='mm-window-dots'>
                  <span></span>
                  <span></span>
                  <span></span>
                </div>
                <div className='mm-window-title'>
                  middle-monitor / {TABS[activeTab].id}
                </div>
              </div>
              <div className='mm-window-body'>
                {/* Every tab shows the same failed checkout: the trace id is what
                    ties the four views together, so it stays visible above them. */}
                <div className='mm-showcase-corr'>
                  <span className='mm-corr-label'>
                    {t('home.platform.correlation_label')}
                  </span>
                  <code className='mm-corr-trace'>
                    trace_id a1b2c3d4e5f60789
                  </code>
                </div>
                {activeTab === 0 && (
                  <div className='mm-showcase-content mm-showcase-traces'>
                    <div className='mm-waterfall'>
                      <div className='mm-waterfall-row'>
                        <span className='mm-w-label'>frontend</span>
                        <div
                          className='mm-w-bar'
                          style={{
                            width: '100%',
                            left: '0%',
                            background: 'var(--mm-brand-purple)',
                          }}></div>
                      </div>
                      <div className='mm-waterfall-row'>
                        <span className='mm-w-label'>api-gateway</span>
                        <div
                          className='mm-w-bar'
                          style={{
                            width: '80%',
                            left: '10%',
                            background: 'var(--mm-brand-cyan)',
                          }}></div>
                      </div>
                      <div className='mm-waterfall-row'>
                        <span className='mm-w-label'>auth-service</span>
                        <div
                          className='mm-w-bar'
                          style={{
                            width: '20%',
                            left: '15%',
                            background: 'var(--mm-brand-pink)',
                          }}></div>
                      </div>
                      <div className='mm-waterfall-row'>
                        <span className='mm-w-label'>billing-service</span>
                        <div
                          className='mm-w-bar mm-w-err'
                          style={{
                            width: '40%',
                            left: '40%',
                            background: 'var(--status-error)',
                          }}></div>
                      </div>
                      <div className='mm-waterfall-row'>
                        <span className='mm-w-label'>postgres-db</span>
                        <div
                          className='mm-w-bar'
                          style={{
                            width: '15%',
                            left: '50%',
                            background: 'var(--mm-brand-amber)',
                          }}></div>
                      </div>
                    </div>
                  </div>
                )}
                {activeTab === 1 && (
                  <div className='mm-showcase-content mm-showcase-metrics'>
                    <div className='mm-metrics-grid'>
                      <div className='mm-metric-card'>
                        <div className='mm-metric-title'>
                          {t('home.platform.metrics_cpu')}
                        </div>
                        <div className='mm-metric-val'>42%</div>
                        <div className='mm-metric-chart cpu-chart'></div>
                      </div>
                      <div className='mm-metric-card'>
                        <div className='mm-metric-title'>
                          {t('home.platform.metrics_memory')}
                        </div>
                        <div className='mm-metric-val'>6.4 GB</div>
                        <div className='mm-metric-chart ram-chart'></div>
                      </div>
                      <div className='mm-metric-card'>
                        <div className='mm-metric-title'>
                          {t('home.platform.metrics_network')}
                        </div>
                        <div className='mm-metric-val'>1.2 Gbps</div>
                        <div className='mm-metric-chart net-chart'></div>
                      </div>
                    </div>
                    <p className='mm-corr-note'>
                      {t('home.platform.metrics_note')}
                    </p>
                  </div>
                )}
                {activeTab === 2 && (
                  <div className='mm-showcase-content mm-showcase-errors'>
                    <div className='mm-error-list'>
                      <div className='mm-error-item active'>
                        <span className='mm-error-badge'>Unhandled</span>
                        <div className='mm-error-desc'>
                          <strong>
                            TypeError: Cannot read property 'id' of undefined
                          </strong>
                          <span>
                            in PaymentProcessor.process
                            (src/billing/payment.ts:42)
                          </span>
                        </div>
                        <div className='mm-error-count'>
                          {t('home.platform.errors_count', { count: 12000 })}
                        </div>
                      </div>
                      <div className='mm-error-item'>
                        <span className='mm-error-badge db'>DBError</span>
                        <div className='mm-error-desc'>
                          <strong>
                            ConnectionTimeout: failed to connect to host
                          </strong>
                          <span>
                            in UserRepository.findByEmail (src/auth/user.go:102)
                          </span>
                        </div>
                        <div className='mm-error-count'>
                          {t('home.platform.errors_count', { count: 3400 })}
                        </div>
                      </div>
                      <div className='mm-error-item'>
                        <span className='mm-error-badge http'>HTTP 502</span>
                        <div className='mm-error-desc'>
                          <strong>
                            UpstreamError: stripe.com returned 502 Bad Gateway
                          </strong>
                          <span>
                            in billing.ChargeCard (internal/billing/stripe.go:88)
                          </span>
                        </div>
                        <div className='mm-error-count'>
                          {t('home.platform.errors_count', { count: 870 })}
                        </div>
                      </div>
                      <div className='mm-error-item'>
                        <span className='mm-error-badge panic'>Panic</span>
                        <div className='mm-error-desc'>
                          <strong>
                            runtime error: index out of range [3] with length 3
                          </strong>
                          <span>
                            in cart.ApplyDiscounts (internal/cart/pricing.go:214)
                          </span>
                        </div>
                        <div className='mm-error-count'>
                          {t('home.platform.errors_count', { count: 142 })}
                        </div>
                      </div>
                      <div className='mm-error-item'>
                        <span className='mm-error-badge web'>Browser</span>
                        <div className='mm-error-desc'>
                          <strong>
                            NetworkError: Failed to fetch /api/v1/checkout
                          </strong>
                          <span>in CheckoutButton (web/src/Checkout.tsx:57)</span>
                        </div>
                        <div className='mm-error-count'>
                          {t('home.platform.errors_count', { count: 61 })}
                        </div>
                      </div>
                    </div>
                  </div>
                )}
                {activeTab === 3 && (
                  <div className='mm-showcase-content mm-showcase-logs'>
                    <div className='mm-log-viewer'>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.123</span>{' '}
                        <span className='mm-log-level info'>INFO</span>{' '}
                        <span className='mm-log-msg'>
                          [api-gateway] Incoming request GET /api/v1/checkout
                        </span>
                      </div>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.145</span>{' '}
                        <span className='mm-log-level debug'>DEBUG</span>{' '}
                        <span className='mm-log-msg'>
                          [auth-service] Validating JWT token for user_8a9v
                        </span>
                      </div>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.201</span>{' '}
                        <span className='mm-log-level warn'>WARN</span>{' '}
                        <span className='mm-log-msg'>
                          [billing] Payment gateway latency high (450ms)
                        </span>
                      </div>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.450</span>{' '}
                        <span className='mm-log-level error'>ERROR</span>{' '}
                        <span className='mm-log-msg'>
                          [billing] Stripe connection refused. Order failed.
                        </span>
                      </div>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.452</span>{' '}
                        <span className='mm-log-level error'>ERROR</span>{' '}
                        <span className='mm-log-msg'>
                          [billing] Rolling back order 8421, refund queued
                        </span>
                      </div>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.455</span>{' '}
                        <span className='mm-log-level warn'>WARN</span>{' '}
                        <span className='mm-log-msg'>
                          [api-gateway] Circuit breaker open for billing-service
                        </span>
                      </div>
                      <div className='mm-log-line'>
                        <span className='mm-log-time'>14:02:41.461</span>{' '}
                        <span className='mm-log-level info'>INFO</span>{' '}
                        <span className='mm-log-msg'>
                          [api-gateway] POST /api/v1/checkout returned 502 in
                          338ms
                        </span>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Code / Integration Section (s style) */}
      <section className='mm-integration'>
        <div className='mm-container mm-integration-grid'>
          <div className='mm-integration-text'>
            <h2>
              {t('home.integration.title_1')}
              <br />
              {t('home.integration.title_2')}
            </h2>
            <p>{t('home.integration.subtitle')}</p>

            <ul className='mm-feature-list'>
              <li>
                <HiOutlineShieldCheck className='mm-feat-icon' />{' '}
                <span>{t('home.integration.feature_context')}</span>
              </li>
              <li>
                <HiOutlineChartBar className='mm-feat-icon' />{' '}
                <span>{t('home.integration.feature_metrics')}</span>
              </li>
              <li>
                <HiOutlineCommandLine className='mm-feat-icon' />{' '}
                <span>{t('home.integration.feature_config')}</span>
              </li>
            </ul>
          </div>

          <div className='mm-code-window'>
            <div className='mm-code-header'>
              <div className='mm-code-tabs'>
                {SDK_LANGS.map((lang, idx) => {
                  const LangIcon = lang.icon;
                  return (
                    <button
                      key={lang.name}
                      className={`mm-code-tab ${sdkLangIndex === idx ? 'active' : ''}`}
                      onClick={() => setSdkLangIndex(idx)}>
                      <LangIcon /> {lang.name}
                    </button>
                  );
                })}
              </div>
            </div>
            <div className='mm-code-body'>
              {SDK_LANGS[sdkLangIndex].lines.map((line, i) => (
                <div key={i} className='mm-code-line'>
                  {line.startsWith('$') ? (
                    <>
                      <span className='mm-prompt'>$</span>{' '}
                      <span className='mm-cmd'>{line.slice(2)}</span>
                    </>
                  ) : line.startsWith('#') ? (
                    <span className='mm-comment'>{line}</span>
                  ) : (
                    <span className='mm-code'>{line}</span>
                  )}
                </div>
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* Feature Bento Box */}
      <section id='features' className='mm-bento-section'>
        <div className='mm-container'>
          <div className='mm-bento-grid'>
            <div className='mm-bento-card mm-bento-large'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(139, 92, 246, 0.1)',
                  color: '#a78bfa',
                }}>
                <HiOutlineServerStack />
              </div>
              <h3>{t('home.bento.agent_title')}</h3>
              <p>{t('home.bento.agent_desc')}</p>
              <div className='mm-bento-visual mm-visual-pulse'>
                <div className='mm-pulse-ring'></div>
                <div className='mm-pulse-core'></div>
              </div>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(236, 72, 153, 0.1)',
                  color: '#f472b6',
                }}>
                <HiOutlineExclamationTriangle />
              </div>
              <h3>{t('home.bento.rca_title')}</h3>
              <p>{t('home.bento.rca_desc')}</p>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(6, 182, 212, 0.1)',
                  color: '#22d3ee',
                }}>
                <HiOutlineSignal />
              </div>
              <h3>{t('home.bento.alerts_title')}</h3>
              <p>{t('home.bento.alerts_desc')}</p>
              <div className='mm-channel-row'>
                {CHANNELS.map((channel) => {
                  const ChannelIcon = channel.icon;
                  return (
                    <span key={channel.name} className='mm-channel-chip'>
                      <ChannelIcon /> {channel.name}
                    </span>
                  );
                })}
              </div>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(34, 197, 94, 0.1)',
                  color: '#4ade80',
                }}>
                <HiOutlineGlobeAlt />
              </div>
              <h3>{t('home.bento.checks_title')}</h3>
              <p>{t('home.bento.checks_desc')}</p>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(139, 92, 246, 0.1)',
                  color: '#a78bfa',
                }}>
                <HiOutlineSquares2X2 />
              </div>
              <h3>{t('home.bento.dashboards_title')}</h3>
              <p>{t('home.bento.dashboards_desc')}</p>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(245, 158, 11, 0.1)',
                  color: '#fbbf24',
                }}>
                <HiOutlineCalendarDays />
              </div>
              <h3>{t('home.bento.maintenance_title')}</h3>
              <p>{t('home.bento.maintenance_desc')}</p>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(236, 72, 153, 0.1)',
                  color: '#f472b6',
                }}>
                <HiOutlineFire />
              </div>
              <h3>{t('home.bento.profiling_title')}</h3>
              <p>{t('home.bento.profiling_desc')}</p>
            </div>

            <div className='mm-bento-card'>
              <div
                className='mm-bento-icon'
                style={{
                  background: 'rgba(6, 182, 212, 0.1)',
                  color: '#22d3ee',
                }}>
                <HiOutlineCommandLine />
              </div>
              <h3>{t('home.bento.terraform_title')}</h3>
              <p>{t('home.bento.terraform_desc')}</p>
            </div>
          </div>
        </div>
      </section>

      {/* FAQ */}
      <section id='faq' className='mm-faq'>
        <div className='mm-container'>
          <div className='mm-section-header'>
            <h2 className='mm-section-title'>{t('home.faq.title')}</h2>
            <p className='mm-section-subtitle'>{t('home.faq.subtitle')}</p>
          </div>
          <div className='mm-faq-list'>
            {FAQ_ITEMS.map((item) => (
              <details key={item.q} className='mm-faq-item'>
                <summary>{item.q}</summary>
                <p>{item.a}</p>
              </details>
            ))}
          </div>
        </div>
      </section>

      {/* Footer CTA */}
      <section className='mm-cta'>
        <div className='mm-container'>
          <div className='mm-cta-inner'>
            <h2>{t('home.cta.title')}</h2>
            <p>{t('home.cta.subtitle')}</p>
            <div className='mm-cta-buttons'>
              {!hasAccount && (
                <>
                  <Link
                    to='/get-started'
                    className='mm-btn mm-btn-primary mm-btn-lg plausible-event-name=Signup+Start'>
                    {t('home.cta.btn_free')}
                  </Link>
                  <Link to='/login' className='mm-btn mm-btn-white mm-btn-lg'>
                    {t('home.cta.btn_signin')}
                  </Link>
                </>
              )}
              {hasAccount && (
                <Link
                  to={dashboardUrl}
                  className='mm-btn mm-btn-primary mm-btn-lg'>
                  {t('home.cta.btn_dashboard')}
                </Link>
              )}
            </div>
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className='mm-footer'>
        <div className='mm-container mm-footer-inner'>
          <div className='mm-footer-brand'>
            <div className='brand-logo-icon brand-logo-icon-sm mm-footer-logo-icon'>
              <HiBolt />
            </div>
            <span>Middle Monitor</span>
          </div>
          <div className='mm-footer-links'>
            {!hasAccount && (
              <Link to='/login'>{t('home.footer.links.signin')}</Link>
            )}
            <Link to='/get-started'>{t('home.footer.links.get_started')}</Link>
            <Link to='/docs'>{t('home.footer.links.docs')}</Link>
            <Link to='/pricing'>{t('home.footer.links.pricing')}</Link>
            <a href='#platform'>{t('home.footer.links.platform')}</a>
            <a href='#features'>{t('home.footer.links.features')}</a>
            <Link to='/contact'>{t('home.footer.links.contact')}</Link>
            <Link to='/status'>{t('public.nav_status')}</Link>
            <Link to='/alternatives'>{t('public.nav_alternatives')}</Link>
            <Link to='/legal'>{t('public.nav_legal')}</Link>
            <Link to='/privacy'>{t('public.nav_privacy')}</Link>
            <Link to='/terms'>{t('public.nav_terms')}</Link>
          </div>
          <p className='mm-footer-copy'>
            © {new Date().getFullYear()} Middle Monitor.{' '}
            {t('home.footer.rights')}
          </p>
        </div>
      </footer>
    </div>
  );
}
