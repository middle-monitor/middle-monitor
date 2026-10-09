import { useState, useEffect, useRef, type MouseEvent } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import { useTheme } from '../contexts/ThemeContext';
import { CodeBlock } from '../components/CodeBlock';
import { SiteHeader } from '../components/SiteHeader';
import { useDocumentMeta } from '../seo/useDocumentMeta';
import SECTIONS from '../seo/docs-sections.json';
import { PUBLIC_API_URL as API_URL } from '../api';
import {
  HiOutlineSun,
  HiOutlineMoon,
  HiOutlineDocumentText,
  HiOutlineUserPlus,
  HiOutlineServerStack,
  HiOutlineCube,
  HiOutlineCpuChip,
  HiOutlineExclamationTriangle,
  HiOutlineChartBar,
  HiOutlineScale,
  HiOutlineFire,
  HiOutlineSignal,
  HiOutlineSquares2X2,
  HiOutlineLink,
  HiOutlineGlobeAlt,
  HiOutlineRectangleGroup,
  HiOutlineArrowDownTray,
  HiOutlineBellAlert,
  HiOutlineShieldCheck,
  HiChevronRight,
  HiMagnifyingGlass,
  HiXMark,
  HiBars3,
  HiCheck,
  HiOutlineTrash,
} from 'react-icons/hi2';
import './DocumentationView.css';

type Lang = 'go' | 'node' | 'python' | 'rust';
type CheckType = 'http' | 'ping' | 'sql' | 'certificate' | 'snmp';

const GITHUB_ORG = 'https://github.com/middle-monitor';

const SDK_REPOS = [
  { label: 'Go', pkg: 'github.com/middle-monitor/sdk-go', url: `${GITHUB_ORG}/sdk-go` },
  { label: 'Node.js', pkg: '@middle-monitor/sdk', url: `${GITHUB_ORG}/sdk-typescript` },
  { label: 'Python', pkg: 'middle-monitor-sdk', url: `${GITHUB_ORG}/sdk-python` },
  { label: 'Rust', pkg: 'middle-monitor-sdk', url: `${GITHUB_ORG}/sdk-rust` },
  { label: 'Web (browser)', pkg: '@middle-monitor/web', url: `${GITHUB_ORG}/sdk-web` },
  {
    label: 'Terraform provider',
    pkg: 'middle-monitor/middmonitor',
    url: `${GITHUB_ORG}/terraform-provider-middmonitor`,
  },
];

const CATEGORIES = [
  {
    id: 'demarrage',
    labelKey: 'docs.sidebar_demarrage',
    descKey: 'docs.sidebar_demarrage_desc',
    icon: HiOutlineDocumentText,
    items: [
      {
        id: 'intro',
        labelKey: 'docs.sidebar_intro',
        icon: HiOutlineDocumentText,
      },
      {
        id: 'register',
        labelKey: 'docs.sidebar_register',
        icon: HiOutlineUserPlus,
      },
      {
        id: 'two-factor',
        labelKey: 'docs.sidebar_2fa',
        icon: HiOutlineShieldCheck,
      },
      {
        id: 'deletion',
        labelKey: 'docs.sidebar_deletion',
        icon: HiOutlineTrash,
      },
      {
        id: 'self-hosting',
        labelKey: 'docs.sidebar_self_hosting',
        icon: HiOutlineServerStack,
      },
    ],
  },
  {
    id: 'infrastructure',
    labelKey: 'docs.sidebar_infrastructure',
    descKey: 'docs.sidebar_infrastructure_desc',
    icon: HiOutlineServerStack,
    items: [
      {
        id: 'host-groups',
        labelKey: 'docs.sidebar_host_groups',
        icon: HiOutlineRectangleGroup,
      },
      { id: 'host', labelKey: 'docs.sidebar_host', icon: HiOutlineServerStack },
      { id: 'agent', labelKey: 'docs.sidebar_agent', icon: HiOutlineCpuChip },
      {
        id: 'metrics',
        labelKey: 'docs.sidebar_metrics',
        icon: HiOutlineChartBar,
      },
      {
        id: 'scraping',
        labelKey: 'docs.sidebar_scraping',
        icon: HiOutlineArrowDownTray,
      },
      {
        id: 'custom-metrics',
        labelKey: 'docs.sidebar_custom_metrics',
        icon: HiOutlineChartBar,
      },
      { id: 'limits', labelKey: 'docs.sidebar_limits', icon: HiOutlineScale },
    ],
  },
  {
    id: 'monitoring',
    labelKey: 'docs.sidebar_monitoring',
    descKey: 'docs.sidebar_monitoring_desc',
    icon: HiOutlineSignal,
    items: [
      {
        id: 'services',
        labelKey: 'docs.sidebar_services',
        icon: HiOutlineCube,
      },
      { id: 'checks', labelKey: 'docs.sidebar_checks', icon: HiOutlineSignal },
    ],
  },
  {
    id: 'applications',
    labelKey: 'docs.sidebar_applications',
    descKey: 'docs.sidebar_applications_desc',
    icon: HiOutlineCube,
    items: [
      { id: 'sdk', labelKey: 'docs.sidebar_sdk', icon: HiOutlineSignal },
      {
        id: 'sdk-web',
        labelKey: 'docs.sidebar_sdk_web',
        icon: HiOutlineGlobeAlt,
      },
      {
        id: 'errors',
        labelKey: 'docs.sidebar_errors',
        icon: HiOutlineExclamationTriangle,
      },
      {
        id: 'correlation-links',
        labelKey: 'docs.sidebar_correlation_links',
        icon: HiOutlineLink,
      },
      {
        id: 'traces',
        labelKey: 'docs.sidebar_traces',
        icon: HiOutlineSquares2X2,
      },
      {
        id: 'profiling',
        labelKey: 'docs.sidebar_profiling',
        icon: HiOutlineFire,
      },
    ],
  },
  {
    id: 'alerting',
    labelKey: 'docs.sidebar_alerting',
    descKey: 'docs.sidebar_alerting_desc',
    icon: HiOutlineBellAlert,
    items: [
      {
        id: 'channels',
        labelKey: 'docs.sidebar_channels',
        icon: HiOutlineBellAlert,
      },
      {
        id: 'incidents',
        labelKey: 'docs.sidebar_incidents',
        icon: HiOutlineExclamationTriangle,
      },
    ],
  },
  {
    id: 'api',
    labelKey: 'docs.sidebar_api',
    descKey: 'docs.sidebar_api_desc',
    icon: HiOutlineSignal,
    items: [
      {
        id: 'api-auth',
        labelKey: 'docs.sidebar_api_auth',
        icon: HiOutlineDocumentText,
      },
      {
        id: 'api-endpoints',
        labelKey: 'docs.sidebar_api_endpoints',
        icon: HiOutlineDocumentText,
      },
    ],
  },
  {
    id: 'terraform',
    labelKey: 'docs.sidebar_terraform',
    descKey: 'docs.sidebar_terraform_desc',
    icon: HiOutlineSquares2X2,
    items: [
      {
        id: 'terraform-setup',
        labelKey: 'docs.sidebar_terraform_setup',
        icon: HiOutlineDocumentText,
      },
      {
        id: 'terraform-resources',
        labelKey: 'docs.sidebar_terraform_resources',
        icon: HiOutlineCube,
      },
    ],
  },
] as const;

export default function DocumentationView() {
  const { t, i18n } = useTranslation();
  useDocumentMeta('docs');
  const { toggleTheme, isDark } = useTheme();

  const [lang, setLang] = useState<Lang>('go');
  const [checkType, setCheckType] = useState<CheckType>('http');
  const [activeId, setActiveId] = useState<string>('');
  const [expandedCats, setExpandedCats] = useState<Set<string>>(
    () => new Set(CATEGORIES.map((c) => c.id)),
  );
  const [searchOpen, setSearchOpen] = useState(false);
  const [modalSearch, setModalSearch] = useState('');
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [selectedResult, setSelectedResult] = useState(0);

  const modalInputRef = useRef<HTMLInputElement>(null);

  const openSearch = () => {
    setModalSearch('');
    setSelectedResult(0);
    setSearchOpen(true);
  };

  const closeSearch = () => {
    setSearchOpen(false);
    setModalSearch('');
  };

  const mq = modalSearch.trim().toLowerCase();
  const searchResults =
    mq.length >= 1
      ? SECTIONS.filter((s) => {
          return (
            s.title.toLowerCase().includes(mq) ||
            s.category.toLowerCase().includes(mq) ||
            s.excerpt.toLowerCase().includes(mq) ||
            s.keywords.some((k) => k.includes(mq))
          );
        })
      : SECTIONS;

  const navigateTo = (id: string) => {
    closeSearch();
    setMobileNavOpen(false);
    requestAnimationFrame(() => {
      document
        .getElementById(id)
        ?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
  };

  // The browser's native anchor jump fires before React has rendered the
  // sections, so a deep link (docs#sdk-web from a README) would land at the top.
  useEffect(() => {
    const id = decodeURIComponent(window.location.hash.slice(1));
    if (!id) return;
    requestAnimationFrame(() => {
      document.getElementById(id)?.scrollIntoView({ block: 'start' });
    });
  }, []);

  useEffect(() => {
    if (searchOpen) {
      setTimeout(() => modalInputRef.current?.focus(), 50);
    }
  }, [searchOpen]);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        searchOpen ? closeSearch() : openSearch();
        return;
      }
      if (!searchOpen) return;
      if (e.key === 'Escape') {
        closeSearch();
        return;
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedResult((i) => Math.min(i + 1, searchResults.length - 1));
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedResult((i) => Math.max(i - 1, 0));
      }
      if (e.key === 'Enter' && searchResults[selectedResult]) {
        navigateTo(searchResults[selectedResult].id);
      }
    };
    window.addEventListener('keydown', handleKey);
    return () => window.removeEventListener('keydown', handleKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchOpen, searchResults, selectedResult]);

  useEffect(() => {
    setSelectedResult(0);
  }, [modalSearch]);

  useEffect(() => {
    document.body.style.overflow = searchOpen || mobileNavOpen ? 'hidden' : '';
    return () => {
      document.body.style.overflow = '';
    };
  }, [searchOpen, mobileNavOpen]);

  const toggleCat = (id: string) => {
    setExpandedCats((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  useEffect(() => {
    const observerOptions = {
      root: null,
      rootMargin: '-20% 0px -60% 0px',
      threshold: 0,
    };
    const observerCallback: IntersectionObserverCallback = (entries) => {
      entries.forEach((entry) => {
        if (entry.isIntersecting) setActiveId(entry.target.id);
      });
    };
    const observer = new IntersectionObserver(
      observerCallback,
      observerOptions,
    );
    const sections = document.querySelectorAll(
      '.doc-subsection, .doc-section[id]',
    );
    sections.forEach((section) => observer.observe(section));
    return () => observer.disconnect();
  }, []);

  const ShareAnchor = () => {
    const [copied, setCopied] = useState(false);
    const handleClick = (e: MouseEvent<HTMLButtonElement>) => {
      const section = e.currentTarget.closest<HTMLElement>('[id]');
      if (!section) return;
      const url = `${window.location.origin}${window.location.pathname}#${section.id}`;
      window.location.hash = section.id;
      navigator.clipboard?.writeText(url).catch(() => {});
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    };
    return (
      <button
        type='button'
        className='doc-anchor-btn'
        onClick={handleClick}
        aria-label='Copy link to this section'
        title={copied ? 'Link copied!' : 'Copy link to this section'}>
        {copied ? <HiCheck /> : <HiOutlineLink />}
      </button>
    );
  };

  const LangTabs = () => (
    <div className='doc-lang-tabs'>
      {(['go', 'node', 'python', 'rust'] as Lang[]).map((l) => (
        <button
          key={l}
          className={`doc-lang-tab ${lang === l ? 'active' : ''}`}
          onClick={() => setLang(l)}>
          {l === 'node'
            ? 'Node.js / TS'
            : l.charAt(0).toUpperCase() + l.slice(1)}
        </button>
      ))}
    </div>
  );

  const checkTypeLabels: Record<CheckType, string> = {
    http: 'HTTP',
    ping: 'Ping',
    sql: 'SQL',
    certificate: 'SSL Certificate',
    snmp: 'SNMP',
  };

  const CheckTypeTabs = () => (
    <div className='doc-lang-tabs'>
      {(['http', 'ping', 'sql', 'certificate', 'snmp'] as CheckType[]).map(
        (c) => (
          <button
            key={c}
            className={`doc-lang-tab ${checkType === c ? 'active' : ''}`}
            onClick={() => setCheckType(c)}>
            {checkTypeLabels[c]}
          </button>
        ),
      )}
    </div>
  );

  const SidebarContent = ({ onNavigate }: { onNavigate?: () => void }) => (
    <>
      {CATEGORIES.map((cat) => {
        const isExpanded = expandedCats.has(cat.id);
        return (
          <div key={cat.id} className='doc-sidebar-section'>
            <button
              className='doc-sidebar-category-btn'
              onClick={() => toggleCat(cat.id)}
              aria-expanded={isExpanded}>
              <span>{t(cat.labelKey)}</span>
              <HiChevronRight
                className={`doc-sidebar-chevron ${isExpanded ? 'open' : ''}`}
              />
            </button>

            {isExpanded && (
              <ul className='doc-sidebar-list'>
                {cat.items.map((item) => (
                  <li key={item.id}>
                    <a
                      href={`#${item.id}`}
                      className={`doc-sidebar-link ${activeId === item.id ? 'active' : ''}`}
                      onClick={() => onNavigate?.()}>
                      <item.icon className='doc-sidebar-icon' />
                      {t(item.labelKey)}
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </div>
        );
      })}
    </>
  );

  return (
    <div className='doc-view' key={i18n.language}>
      {/* ─── Search modal (command palette) ─── */}
      {searchOpen && (
        <div className='doc-modal-overlay' onClick={closeSearch}>
          <div
            className='doc-modal'
            onClick={(e) => e.stopPropagation()}
            role='dialog'
            aria-modal='true'>
            <div className='doc-modal-search-row'>
              <HiMagnifyingGlass className='doc-modal-search-icon' />
              <input
                ref={modalInputRef}
                type='text'
                className='doc-modal-input'
                placeholder='Search documentation...'
                value={modalSearch}
                onChange={(e) => setModalSearch(e.target.value)}
                aria-label='Search documentation'
              />
              <button
                className='doc-modal-close-btn'
                onClick={closeSearch}
                aria-label='Close search'>
                <HiXMark />
              </button>
            </div>

            <div className='doc-modal-results'>
              {searchResults.length === 0 ? (
                <div className='doc-modal-empty'>
                  No results for "{modalSearch}"
                </div>
              ) : (
                <>
                  {!mq && (
                    <div className='doc-modal-hint'>
                      All sections — start typing to search
                    </div>
                  )}
                  {searchResults.map((s, i) => (
                    <button
                      key={s.id}
                      className={`doc-modal-result ${i === selectedResult ? 'selected' : ''}`}
                      onClick={() => navigateTo(s.id)}
                      onMouseEnter={() => setSelectedResult(i)}>
                      <div className='doc-modal-result-meta'>{s.category}</div>
                      <div className='doc-modal-result-title'>{s.title}</div>
                      <div className='doc-modal-result-excerpt'>
                        {s.excerpt}
                      </div>
                    </button>
                  ))}
                </>
              )}
            </div>

            <div className='doc-modal-footer'>
              <span>
                <kbd>↑</kbd>
                <kbd>↓</kbd> navigate
              </span>
              <span>
                <kbd>↵</kbd> open
              </span>
              <span>
                <kbd>Esc</kbd> close
              </span>
            </div>
          </div>
        </div>
      )}

      {/* ─── Mobile nav drawer ─── */}
      {mobileNavOpen && (
        <div
          className='doc-drawer-overlay'
          onClick={() => setMobileNavOpen(false)}>
          <div className='doc-drawer' onClick={(e) => e.stopPropagation()}>
            <div className='doc-drawer-header'>
              <span className='doc-drawer-title'>Documentation</span>
              <button
                className='doc-drawer-close'
                onClick={() => setMobileNavOpen(false)}
                aria-label='Close menu'>
                <HiXMark />
              </button>
            </div>
            <div className='doc-drawer-body'>
              <SidebarContent onNavigate={() => setMobileNavOpen(false)} />
            </div>
          </div>
        </div>
      )}

      {/* ─── Header ─── */}
      <SiteHeader
        active='docs'
        leadingActions={
          <>
            <button
              className='doc-search-trigger'
              onClick={openSearch}
              aria-label='Search documentation'>
              <HiMagnifyingGlass className='doc-search-trigger-icon' />
              <span className='doc-search-trigger-text'>Search docs...</span>
              <kbd className='doc-search-trigger-kbd'>⌘K</kbd>
            </button>

            <button
              className='doc-theme-toggle'
              onClick={toggleTheme}
              aria-label={
                isDark ? 'Switch to light mode' : 'Switch to dark mode'
              }>
              {isDark ? <HiOutlineSun /> : <HiOutlineMoon />}
            </button>
          </>
        }
        trailingActions={
          <button
            className='doc-mobile-menu-btn'
            onClick={() => setMobileNavOpen(true)}
            aria-label='Open navigation'>
            <HiBars3 />
          </button>
        }
      />

      {/* ─── Main layout ─── */}
      <div className='doc-layout'>
        <aside className='doc-sidebar'>
          <SidebarContent />
        </aside>

        <main className='doc-content'>
          <div className='doc-hero'>
            <h1>{t('docs.hero_title')}</h1>
            <p>{t('docs.hero_subtitle')}</p>
          </div>

          {/* GETTING STARTED */}
          <section id='demarrage' className='doc-section'>
            <h2>
              {t('docs.nav_demarrage')}
              <ShareAnchor />
            </h2>
            <p>{t('docs.sidebar_demarrage_desc')}</p>

            <div id='intro' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_intro')}
                <ShareAnchor />
              </h3>
              <p>{t('docs.intro_pillars')}</p>
              <div className='doc-feature-grid'>
                <div className='doc-feature-card'>
                  <HiOutlineServerStack className='doc-feature-card-icon' />
                  <strong>Infrastructure Monitoring</strong>
                  <p>
                    CPU, RAM, Disk, Network on every machine via our lightweight
                    binary agent. Zero configuration.
                  </p>
                </div>
                <div className='doc-feature-card'>
                  <HiOutlineSignal className='doc-feature-card-icon' />
                  <strong>Uptime & Checks</strong>
                  <p>
                    HTTP, SQL, Ping, SSL certificate, and SNMP checks run
                    periodically from the Middle Monitor backend — no agent on
                    the target.
                  </p>
                </div>
                <div className='doc-feature-card'>
                  <HiOutlineSquares2X2 className='doc-feature-card-icon' />
                  <strong>Distributed Tracing</strong>
                  <p>
                    Follow requests across microservices. Auto-instrument your
                    HTTP layer in one line.
                  </p>
                </div>
                <div className='doc-feature-card'>
                  <HiOutlineExclamationTriangle className='doc-feature-card-icon' />
                  <strong>Error Tracking</strong>
                  <p>
                    Automatic panic/exception capture with full stacktraces,
                    grouped by fingerprint.
                  </p>
                </div>
                <div className='doc-feature-card'>
                  <HiOutlineFire className='doc-feature-card-icon' />
                  <strong>Continuous Profiling</strong>
                  <p>
                    Heap and CPU flame graphs directly from your production
                    services.
                  </p>
                </div>
                <div className='doc-feature-card'>
                  <HiOutlineBellAlert className='doc-feature-card-icon' />
                  <strong>Alerting & AI-powered RCA</strong>
                  <p>
                    Rule-based alerting, plus AI-powered root cause analysis
                    that correlates infrastructure and application signals to
                    explain incidents.
                  </p>
                </div>
              </div>
              <div className='doc-callout'>
                Some features are part of the <strong>Pro plan</strong>:
                Continuous Profiling, Alert Rules, Notification Channels, and
                Maintenance Windows. They are visible in the app with a{' '}
                <strong>Pro</strong> badge; see the{' '}
                <Link to='/pricing'>Pricing</Link> page for plan details and
                limits.
              </div>
              <p style={{ marginTop: '1rem' }}>
                Every account can enable{' '}
                <strong>two-factor authentication</strong> (TOTP), and an admin
                can enforce it organization-wide — see{' '}
                <strong>Two-Factor Authentication</strong> below.
              </p>
            </div>

            <div id='register' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_register')}
                <ShareAnchor />
              </h3>
              <p>{t('docs.register_create')}</p>
              <p>{t('docs.register_sidebar')}</p>
            </div>

            <div id='two-factor' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_2fa')}
                <ShareAnchor />
              </h3>
              <p>
                Middle Monitor supports{' '}
                <strong>TOTP-based two-factor authentication</strong> (Google
                Authenticator, 1Password, Authy, and any compatible app). 2FA is
                managed per user from
                <strong> Account</strong>, and can be enforced for a whole
                organization by an admin.
              </p>
              <p>
                <strong>Enrolling</strong>
              </p>
              <ol style={{ paddingLeft: '1.2rem', lineHeight: 1.7 }}>
                <li>
                  Open <strong>Account</strong> and start the two-factor setup.
                </li>
                <li>
                  Scan the QR code (or paste the secret) into your authenticator
                  app.
                </li>
                <li>
                  Enter the 6-digit code to confirm. You then get a set of{' '}
                  <strong>recovery codes</strong>.
                </li>
              </ol>
              <div className='doc-callout'>
                Store your recovery codes somewhere safe — they are shown{' '}
                <strong>only once</strong> and are the only way to sign in if
                you lose your authenticator device. Each code works a single
                time.
              </div>
              <p>
                <strong>Org-wide enforcement</strong>
              </p>
              <p>
                An admin can require 2FA for every member from{' '}
                <strong>Settings → Security</strong>. Once enabled, members who
                have not enrolled are taken to the enrollment screen on their
                next sign-in and cannot access the dashboard until 2FA is set
                up. While enforcement is active a member cannot disable their
                own 2FA.
              </p>
              <p>
                At login, after your password you are prompted for the 6-digit
                code (or a recovery code).
              </p>
            </div>

            <div id='deletion' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_deletion')}
                <ShareAnchor />
              </h3>
              <p>
                Both deletions are immediate and cannot be undone. There is no
                grace period and no export step, so export what you need first.
              </p>
              <p>
                <strong>Deleting an organization</strong>
              </p>
              <p>
                An admin deletes the active organization from{' '}
                <strong>Settings → Danger zone</strong>, typing its slug to
                confirm. Every check, host, alert rule, channel, token and
                stored trace, log, metric and error goes with it, and its
                subscription is cancelled. Members who belong to another
                organization keep their account and move to it; members who
                belong to no other organization lose their account.
              </p>
              <p>
                <strong>Deleting your account</strong>
              </p>
              <p>
                From <strong>Account → Delete account</strong>, confirmed with
                your password. Organizations where you are the only member are
                deleted with your account. If you are the last admin of an
                organization that has other members, the deletion is refused:
                make someone else admin, or delete the organization, first.
              </p>
              <div className='doc-callout'>
                Agents and SDKs that still send data to a deleted organization
                are rejected: their install and service tokens are deleted with
                it.
              </div>
            </div>

            <div id='self-hosting' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_self_hosting')}
                <ShareAnchor />
              </h3>
              <p>
                Middle Monitor is open source: the platform lives at{' '}
                <a
                  href={`${GITHUB_ORG}/middle-monitor`}
                  target='_blank'
                  rel='noopener noreferrer'>
                  github.com/middle-monitor/middle-monitor
                </a>{' '}
                under the FSL-1.1-ALv2 license (free to use, modify and
                self-host; each release becomes Apache 2.0 after two years). The
                agent and the SDKs work the same against your own instance.
              </p>
              <p>
                <strong>Run it</strong> on any machine with Docker Compose and
                about 3 GB of RAM:
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Terminal</div>
                <CodeBlock
                  language='bash'
                  code={`git clone https://github.com/middle-monitor/middle-monitor.git
cd middle-monitor/deploy/self-hosted
cp .env.example .env   # set JWT_SECRET, DB_PASSWORD and SEED_ADMIN_*
docker compose up -d --build`}
                />
              </div>
              <p>
                Everything is served on <code>http://localhost:8000</code>{' '}
                (<code>HTTP_PORT</code> changes it) behind a single Caddy entry
                point: ingestion paths go to the receiver, the rest of{' '}
                <code>/api</code> to the API, everything else to the UI. Put
                your own TLS proxy in front and set <code>PUBLIC_URL</code> to
                the address users and agents reach.
              </p>
              <p>
                <strong>First sign-in</strong>
              </p>
              <p>
                Sign in with the <code>SEED_ADMIN_EMAIL</code> /{' '}
                <code>SEED_ADMIN_PASSWORD</code> account, created already
                verified. Signups need an email confirmation, so configure{' '}
                <code>SMTP_*</code> before inviting the rest of the team.
              </p>
              <div className='doc-callout'>
                A self-hosted instance sells no plans: billing stays off unless{' '}
                <code>BILLING_ENABLED=true</code>, every organization is
                unlimited, and there is no trial. Retention defaults to 30 days;
                a platform admin (<code>PLATFORM_ADMIN_EMAILS</code>) can set
                quotas or retention per organization.
              </div>
              <p>
                <strong>Agents and SDKs</strong> point at your instance instead
                of <code>{API_URL}</code>: the install script served by your
                instance already writes its own URL into the agent
                configuration.
              </p>
              <p>
                <strong>Updating</strong>: <code>git pull</code> then{' '}
                <code>docker compose up -d --build</code>. Database migrations
                run when the API starts. Every setting is listed in{' '}
                <code>deploy/self-hosted/.env.example</code>.
              </p>
            </div>
          </section>

          <hr className='doc-divider' />

          {/* INFRASTRUCTURE */}
          <section id='infrastructure' className='doc-section'>
            <h2>
              {t('docs.sidebar_infrastructure')}
              <ShareAnchor />
            </h2>
            <p>{t('docs.sidebar_infrastructure_desc')}</p>

            <div id='host-groups' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_host_groups')}
                <ShareAnchor />
              </h3>
              <p>
                A <strong>host group</strong> gathers the hosts that share the
                same logical role — for example, all the machines running one
                application. Alert correlation is scoped to a host{' '}
                <strong>and its group</strong>: when an error or check failure
                happens, Middle Monitor only looks for related signals (failing
                services, other apps erroring){' '}
                <strong>within that group</strong>, never across your whole
                environment. This keeps the &quot;Context / Correlation&quot;
                panel relevant instead of noisy.
              </p>
              <p>
                <strong>Defaults</strong>
              </p>
              <ul style={{ paddingLeft: '1.2rem', lineHeight: 1.7 }}>
                <li>
                  Every organization has one <strong>Default</strong> group. New
                  hosts join it automatically.
                </li>
                <li>
                  Manage groups from the <strong>Hosts</strong> page: create a
                  group, then pick it in the
                  <strong> Group</strong> column on each host to reassign it.
                </li>
                <li>
                  Deleting a group moves its hosts back to the Default group —
                  no host is ever left without one.
                </li>
              </ul>
              <div className='doc-callout'>
                Correlation links (on the <strong>Errors</strong> page) can
                point an app to a <strong>host group</strong>, a single host, or
                a service. Linking to a group is the recommended way to tie an
                app to all the machines it runs on, so co-occurring failures
                across the group surface together.
              </div>
            </div>
            <div id='host' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_host')}
                <ShareAnchor />
              </h3>
              <p>
                A <strong>Host</strong> is the top-level container representing
                a physical or virtual machine. Services, checks, and agent
                metrics attach to it. You don't need to create one upfront — the
                agent registers its own host automatically on first startup,
                matched by hostname within your organization. Create one
                manually first only if you want to set its display name or host
                group before installing the agent, or if you're adding a check
                for something with no agent of its own (e.g. a managed
                database).
              </p>
            </div>

            <div id='agent' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_agent')}
                <ShareAnchor />
              </h3>
              <p>
                The Middle Monitor agent is a single static binary. It runs as a
                background service and ships CPU, RAM, Disk, and Network metrics
                every 60 seconds by default (configurable via{' '}
                <code>interval</code> in <code>config.yaml</code>). No runtime
                dependencies required.
              </p>
              <div className='doc-callout'>
                The install script automatically detects your OS (Linux x86_64 /
                arm64, macOS), registers the agent as a systemd service, and
                starts it. Your host must have outbound HTTPS access to{' '}
                <code>{API_URL}</code>.
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Terminal (Linux / macOS)</div>
                <CodeBlock
                  language='bash'
                  code={`# Download and run the installation script with your host token
curl -fsSL ${API_URL}/api/v1/agents/download/install \\
  | MIDDLE_MONITOR_INSTALL_TOKEN=your_host_token bash`}
                />
              </div>
              <p
                style={{
                  marginTop: '1rem',
                  color: 'var(--doc-muted)',
                  fontSize: '0.875rem',
                }}>
                The install token is generated from{' '}
                <strong>Settings → API Keys → Agent install tokens</strong>. It
                is scoped to your organization, not to a single host.
              </p>
              <p style={{ marginTop: '1rem' }}>
                The install script writes its config to{' '}
                <code>/etc/middle-monitor/config.yaml</code>. Every field is
                optional — edit it and restart the agent service (systemd on
                Linux, launchd on macOS) to apply what's collected.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`api:
  url: ${API_URL}
  api_key: your_host_token   # same value as MIDDLE_MONITOR_INSTALL_TOKEN

host:
  name: web-prod-01          # defaults to the OS hostname
  service: api                # logical app/stack label, defaults to "default"

metrics:
  cpu: true
  ram: true
  disk: true
  network: true

interval: 60                  # seconds between metric pushes`}
                />
              </div>
            </div>

            <div id='metrics' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_metrics')}
                <ShareAnchor />
              </h3>
              <p>
                Once the agent is running, metrics appear on your host page
                within a minute or two (first collection cycle). Each metric is
                stored as a dedicated agent service on the host (
                <code>agent_cpu</code>, <code>agent_ram</code>,{' '}
                <code>agent_disk</code>, <code>agent_network</code>). The
                following metrics are collected automatically:
              </p>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Metric</th>
                    <th>Unit</th>
                    <th>Description</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>cpu</td>
                    <td>%</td>
                    <td>Total CPU utilization across all cores</td>
                  </tr>
                  <tr>
                    <td>ram</td>
                    <td>%</td>
                    <td>
                      Memory utilization (total size reported as metadata)
                    </td>
                  </tr>
                  <tr>
                    <td>disk</td>
                    <td>%</td>
                    <td>
                      Root filesystem utilization (total size reported as
                      metadata)
                    </td>
                  </tr>
                  <tr>
                    <td>network_speed_in / network_speed_out</td>
                    <td>bytes/s</td>
                    <td>
                      Inbound / outbound throughput since the previous cycle
                    </td>
                  </tr>
                  <tr>
                    <td>network_bytes_in / network_bytes_out</td>
                    <td>bytes</td>
                    <td>
                      Cumulative traffic counters (also exposed as metadata
                      totals)
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <div id='scraping' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_scraping')}
                <ShareAnchor />
              </h3>
              <p>
                Beyond its own system metrics, the agent can{' '}
                <strong>pull</strong> any endpoint serving the Prometheus text
                exposition format — node_exporter, an instrumented application,
                a sidecar. The series are shipped to Middle Monitor with their
                labels intact and land in the{' '}
                <a href='#custom-metrics'>custom metrics</a> explorer.
              </p>
              <div className='doc-callout'>
                Scraping is <strong>off by default</strong>. An agent you
                upgrade without touching its config behaves exactly as before.
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`scrape:
  enabled: true
  interval: 15               # default seconds between scrapes
  timeout: 10                # default seconds before a target is given up on
  targets:
    - name: node             # added to every series as scrape_target
      url: http://localhost:9100/metrics
      labels:
        env: prod            # added to every series from this target
    - name: my-app
      url: http://localhost:8080/metrics
      interval: 30           # overrides the default for this target only
      timeout: 5`}
                />
              </div>
              <p>Restart the agent service to apply.</p>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Field</th>
                    <th>Default</th>
                    <th>Description</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>enabled</td>
                    <td>false</td>
                    <td>Turns pulling on. Nothing is scraped while false.</td>
                  </tr>
                  <tr>
                    <td>interval / timeout</td>
                    <td>15s / 10s</td>
                    <td>
                      Defaults for every target, overridable per target. Each
                      target runs on its own loop, so a slow endpoint never
                      delays the others.
                    </td>
                  </tr>
                  <tr>
                    <td>targets[].url</td>
                    <td>required</td>
                    <td>Full URL of the exposition endpoint.</td>
                  </tr>
                  <tr>
                    <td>targets[].labels</td>
                    <td>none</td>
                    <td>
                      Added to every series from this target. They win over a
                      label of the same name coming from the endpoint — they are
                      what you configured deliberately.
                    </td>
                  </tr>
                </tbody>
              </table>
              <p>
                <strong>What is kept.</strong> Counters, gauges and untyped
                metrics are all stored with their raw value; rates are computed
                at query time, so nothing is lost to a delta taken too early.
                Histogram and summary series arrive as their individual buckets
                and quantiles. A malformed line is skipped rather than failing
                the whole scrape, and <code>NaN</code> is dropped instead of
                being stored as a zero that would read as a real measurement.
              </p>

              <h4>Service discovery: Nomad and DNS SRV</h4>
              <p>
                On a scheduler, the address of an instance changes every time it
                is rescheduled, so a static target list goes stale. The agent
                can derive its targets from Nomad's service registry, from DNS
                SRV records, or both.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`scrape:
  enabled: true
  discovery_interval: 30     # seconds between refreshes

  nomad:
    address: http://localhost:4646
    namespace: ''            # empty uses Nomad's default namespace
    token: ''                # ACL token, if your cluster requires one
    tag: middle-monitor      # only services carrying this tag; empty means all
    metrics_path: /metrics
    scheme: http

  srv:
    - name: _metrics._tcp.service.consul
      metrics_path: /metrics
      scheme: http
      labels:
        source: consul`}
                />
              </div>
              <p>
                Targets are refreshed every{' '}
                <code>discovery_interval</code> and{' '}
                <strong>reconciled</strong>: an instance that appears gets its
                own scrape loop, one that disappears has its loop stopped — the
                agent never keeps hammering an address Nomad has already moved.
                Static <code>targets</code> and discovered ones coexist.
              </p>
              <div className='doc-callout'>
                When a discovery source is unreachable for a cycle, the current
                targets are <strong>kept</strong> rather than dropped. Acting on
                a partial list would tear down every scrape of a source that is
                merely blipping, and a gap in the data is indistinguishable from
                a real outage.
              </div>
              <p>
                Discovered targets carry where they come from, so a series can
                be traced back to what the cluster is running:
              </p>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Label</th>
                    <th>Source</th>
                    <th>Value</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>nomad_service</td>
                    <td>Nomad</td>
                    <td>Registered service name</td>
                  </tr>
                  <tr>
                    <td>nomad_job / nomad_alloc</td>
                    <td>Nomad</td>
                    <td>Job and allocation running the instance</td>
                  </tr>
                  <tr>
                    <td>nomad_namespace</td>
                    <td>Nomad</td>
                    <td>Namespace of the allocation</td>
                  </tr>
                  <tr>
                    <td>srv_record</td>
                    <td>DNS SRV</td>
                    <td>Record the target was resolved from</td>
                  </tr>
                  <tr>
                    <td>instance</td>
                    <td>both</td>
                    <td>host:port of the instance</td>
                  </tr>
                </tbody>
              </table>

              <h4>Filtering series before they leave the host</h4>
              <p>
                A single <code>node_exporter</code> serves more than a thousand
                series per host, where a typical setup keeps a few dozen.
                Filtering happens <strong>on the agent</strong>: what is dropped
                never crosses the network, so it costs nothing to transport and
                nothing to store.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`targets:
  - name: node
    url: http://localhost:9100/metrics
    keep_metrics:              # whitelist: when non-empty, the rest is dropped
      - '^node_(cpu|memory|disk_io|vmstat)_.*'
      - '^node_(boot_time_seconds|load(1|5|15))$'
    drop_metrics:              # applied to what keep_metrics left
      - '^(go_|prometheus_|promhttp_).*'
    keep_if_labels:            # keep a metric only for a label value
      - metric: '^node_filesystem_.*'
        label: fstype
        matches: '^ext4$'
    drop_labels: [id, uuid]    # remove a dimension, keep the series`}
                />
              </div>
              <p>
                The evaluation order is part of the contract:{' '}
                <code>keep_metrics</code>, then <code>drop_metrics</code>, then{' '}
                <code>keep_if_labels</code>, then <code>drop_labels</code>. A
                metric a <code>keep_if_labels</code> rule does not name is left
                alone; one it names whose label is absent is dropped, since the
                rule states which dimension is wanted. An invalid expression is
                a <strong>configuration error</strong>, reported by{' '}
                <code>--config-check</code> with the field that carries it.
              </p>

              <h4>Protected targets and probers</h4>
              <p>
                Nomad and several system exporters require a credential by
                default, and the prober family (blackbox, script, dns) is asked{' '}
                <em>what</em> to probe in the URL.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`targets:
  - name: nomad
    url: https://127.0.0.1:4646/v1/metrics
    params: { format: prometheus }
    bearer_token_file: /etc/middle-monitor/nomad.token   # re-read at every scrape
    # bearer_token: '\${NOMAD_TOKEN}'                     # or from the unit environment
    # basic_auth: { username: ops, password_file: /etc/middle-monitor/pw }
    headers: { X-Scope-OrgID: demo }
    tls_config:
      insecure_skip_verify: true    # internal self-signed certificate
      # ca_file / cert_file / key_file for a private CA or mTLS

  - name: dns_query
    url: http://127.0.0.1:15353/query
    params: { module: [custom_dns1, cloudflare_dns1] }
    params_matrix: { query_name: [gmail.com, google.com] }
    # 2 x 2 = 4 targets, each with its own loop and its module/query_name labels`}
                />
              </div>
              <p>
                A scalar parameter is fixed; a list expands into one target per
                value, crossed with the other lists, and the values used are
                attached as labels so the results stay distinguishable.
                Credential files are re-read at every scrape, so a rotated token
                needs no restart, and <code>${'{'}VAR{'}'}</code> is expanded from
                the process environment in the credential fields only.
              </p>

              <h4>Fragments, validation and reload</h4>
              <p>
                When each deployment role installs its own exporter, it wants to
                drop the matching scrape configuration next to it rather than
                edit a file the next role will overwrite.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`scrape:
  include: /etc/middle-monitor/scrape.d/*.yaml   # merged, deduplicated by name
  labels:                                        # applied to every target
    cluster: prod`}
                />
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Shell</div>
                <CodeBlock
                  language='bash'
                  code={`# Validate before restarting anything (exit code != 0 when broken)
middle-monitor-agent --config-check --config /etc/middle-monitor/config.yaml

# Apply a change without losing the running scrape cycles
systemctl reload middle-monitor-agent    # ExecReload=/bin/kill -HUP $MAINPID

middle-monitor-agent --version`}
                />
              </div>
              <p>
                The main file wins on a duplicate name, so a fragment cannot
                silently replace a target written by hand, and a pattern
                matching nothing is not an error. A reload that fails validation
                is refused and the agent keeps running its previous
                configuration.
              </p>

              <h4>Discovery beyond a single Nomad block</h4>
              <p>
                Each piece of software decides where it serves its metrics, so
                the <code>nomad</code> block is a <strong>list</strong>: one
                entry per family of services, each with its own filter, path and
                port. Configurations written with a single block keep working
                unchanged.
              </p>
              <div className='doc-callout'>
                Two different things are called <em>nomad</em> in this file.{' '}
                <code>targets[].name: nomad</code> above is a static target that
                scrapes Nomad's own metrics endpoint; <code>scrape.nomad[]</code>{' '}
                here queries the Nomad API to ask <em>which services are
                registered</em> and derives targets from the answer. They have
                separate settings: the first is configured like any other target,
                the second carries the credentials and TLS of the API connection.
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`nomad:
  - address: https://10.0.1.10:4646         # the Nomad API, not a target
    token_file: /etc/middle-monitor/nomad.token
    region: eu-west                          # default: the region that answers
    tls_config:                              # presented to the API itself
      insecure_skip_verify: true             # internal self-signed certificate
      # ca_file: /etc/ssl/internal-ca.pem
    service: '^(web|worker|cache-exporter)$' # regex on the service name
    labels:                                  # only this block's targets
      project: platform
    tag_labels:                              # tag "fqdn:app.example.com" -> label fqdn
      - prefix: 'fqdn:'
        label: fqdn
  - address: http://localhost:4646
    service: '^object-store$'
    metrics_path: /v2/metrics/cluster        # software not serving /metrics
  - address: http://localhost:4646
    service: '^edge-router$'
    port: 8081                               # replaces the registered port

# Any endpoint answering the Prometheus http_sd format:
#   [{"targets": ["10.0.1.5:9100"], "labels": {"job": "node"}}]
http_sd:
  - url: http://127.0.0.1:8500/targets.json`}
                />
              </div>
              <p>
                <code>tls_config</code> and the ACL token are the client settings
                of the API connection, so a cluster served behind a private CA or
                a self-signed certificate is reachable without opening a plaintext
                listener next to it. <code>labels</code> tags the series of one
                block, which <code>scrape.labels</code> at the root cannot do: it
                applies to every target of the agent. <code>region</code> is
                optional and defaults to the region of the server contacted.
              </p>
              <p>
                <code>http_sd</code> is the escape hatch for orchestrators the
                agent does not know: you produce the list, the agent reads it.
                The reserved labels <code>__metrics_path__</code> and{' '}
                <code>__scheme__</code> are honoured and are not kept as series
                dimensions, which is how one endpoint can describe services that
                expose metrics in different places.
              </p>

              <h4>Targets served by the platform</h4>
              <p>
                Adding a target does not have to mean touching the machine: the
                platform can serve a scrape fragment for a host, which the agent
                fetches for itself with its install token.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`remote_config:
  enabled: true
  interval: 300   # seconds between refreshes`}
                />
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Setting the fragment</div>
                <CodeBlock
                  language='bash'
                  code={`curl -X PUT "${API_URL}/api/v1/organizations/acme/hosts/42/agent-config" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"scrape_config": "- name: redis\\n  url: http://localhost:9121/metrics\\n"}'`}
                />
              </div>
              <p>
                The fragment has the same shape as the ones in{' '}
                <code>scrape.d</code>, and the API parses it the way the agent
                will before storing it, so a broken one is refused there rather
                than discovered on the machine. It is{' '}
                <strong>off by default</strong>: upgrading a binary must never
                make an agent start taking instructions from the network. A
                target configured locally wins over a served one of the same
                name, and an unreachable platform leaves the agent scraping what
                it already has.
              </p>

              <h4>Keeping your Prometheus during the migration</h4>
              <p>
                The agent can also <strong>serve</strong> what it holds in the
                same exposition format — its own system metrics and everything
                it scraped. Your existing Prometheus keeps scraping while you
                move collection over, so switching to Middle Monitor is not a
                one-way door.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`expose:
  enabled: true
  listen: 127.0.0.1:9099     # bound to localhost unless you widen it
  path: /metrics
  staleness: 300             # seconds; a series not refreshed for this long is dropped`}
                />
              </div>
              <p>
                Point a scrape job at <code>127.0.0.1:9099/metrics</code> and
                the agent answers like any exporter. Each sample carries the
                time the agent <em>read</em> it, not the time it is served, so a
                value pulled fifty seconds ago is not presented as a current
                measurement. Past the staleness window a series disappears from
                the output rather than repeating its last value forever, which
                would make a dead target read as healthy.
              </p>
              <div className='doc-callout'>
                Exposing is <strong>off by default</strong> and binds to
                localhost: turning it on makes the agent listen on a port, which
                an upgrade must never do on its own. Widen{' '}
                <code>listen</code> to <code>0.0.0.0:9099</code> only if your
                Prometheus runs on another machine, and firewall it accordingly.
              </div>
              <p>
                The endpoint keeps serving even when Middle Monitor is
                unreachable, so a network incident on our side does not take
                your Prometheus down with it.
              </p>
            </div>

            <div id='custom-metrics' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_custom_metrics')}
                <ShareAnchor />
              </h3>
              <p>
                Any metric with arbitrary labels — scraped by the agent, or
                pushed straight to the receiver as OTLP — is queryable from{' '}
                <strong>Infrastructure → Metrics</strong>, under the system
                charts. Pick a metric name, narrow it with label filters, and
                break it down by a label.
              </p>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Control</th>
                    <th>What it does</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Metric</td>
                    <td>
                      The series name, e.g. <code>http_requests_total</code>.
                      Only names actually seen in the selected time range and
                      scope are listed.
                    </td>
                  </tr>
                  <tr>
                    <td>Filters</td>
                    <td>
                      One or more label equality constraints. Keys and values
                      are suggested from your own data.
                    </td>
                  </tr>
                  <tr>
                    <td>Group by</td>
                    <td>
                      Splits the result into one line per value of a label, for
                      example one line per <code>route</code>.
                    </td>
                  </tr>
                  <tr>
                    <td>Aggregation</td>
                    <td>
                      avg, min, max, sum, count, p50, p75, p90, p95 or p99 over
                      each point of the chart, or <code>rate</code> for a
                      counter (see below).
                    </td>
                  </tr>
                  <tr>
                    <td>Scope</td>
                    <td>
                      The whole organization, one host group, or a single host.
                    </td>
                  </tr>
                </tbody>
              </table>
              <p>
                <strong>Correlating with a host or a host group.</strong> The
                scope selector answers &quot;what was this application doing
                while that machine spiked?&quot;. It is resolved at query time
                against the current group membership, so moving a host between
                groups re-reads the history correctly instead of leaving it
                stamped with a group it no longer belongs to.
              </p>
              <div className='doc-callout'>
                A group by that produces more than <strong>8 series</strong>{' '}
                keeps the first eight: past that a chart stops being readable
                and the palette has no ninth colour. Narrow it with a filter.
              </div>
              <p>
                <strong>Combining queries.</strong> Add up to five queries,
                named <code>$A</code> to <code>$E</code>, each with its own
                metric, filters, group by and aggregation. Without an
                expression they are drawn side by side, each line prefixed with
                its name. Type an expression and press Enter to draw its result
                instead, for example an error ratio in percent:
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Expression</div>
                <CodeBlock language='text' code={`$A / $B * 100`} />
              </div>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Syntax</th>
                    <th>Meaning</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>
                      <code>$A</code> to <code>$E</code>
                    </td>
                    <td>The result of a query.</td>
                  </tr>
                  <tr>
                    <td>
                      <code>+ - * /</code>, parentheses, numbers
                    </td>
                    <td>
                      Arithmetic, with the usual precedence: <code>$A / $B * 100</code>{' '}
                      is a percentage.
                    </td>
                  </tr>
                  <tr>
                    <td>
                      <code>abs(...)</code>
                    </td>
                    <td>Absolute value of every point.</td>
                  </tr>
                  <tr>
                    <td>
                      <code>sum(...)</code>
                    </td>
                    <td>
                      Adds every series into one line, for example{' '}
                      <code>sum($A)</code> for a total across routes.
                    </td>
                  </tr>
                </tbody>
              </table>
              <p>
                <strong>How series are paired.</strong> An operation between two
                queries pairs the series that carry the same labels: with both
                grouped by <code>route</code>, the errors of{' '}
                <code>/checkout</code> are divided by the traffic of{' '}
                <code>/checkout</code> only. A series is applied to every series
                opposite only when its own labels are carried by all of them,
                such as an ungrouped total. A route with no partner on the other
                side is left out of the result; when no series at all can be
                paired, the query is refused rather than drawn empty.
              </p>
              <p>
                <strong>Gaps, not zeros.</strong> A point missing on either side,
                or a division by zero, leaves a gap in the line. Nothing is
                filled in, so a gap always means &quot;no measurement&quot;.
              </p>
              <p>
                <strong>Counters and rate.</strong> A counter such as{' '}
                <code>http_requests_total</code> only goes up, so its raw value
                is rarely what you want to chart. The <code>rate</code>{' '}
                aggregation turns it into a per-second increase. It is computed
                for every individual series first and only then added up per
                group, so a process restart, which resets its counter to zero, is
                read as a restart and not as a negative drop. OTLP delta counters
                are read as increments, cumulative ones (the default of every
                Middle Monitor SDK, and of Prometheus) as running totals.
              </p>
              <div className='doc-callout'>
                <code>rate</code> works on at most 200 series at once, fewer
                with a short step over a long range. Beyond that the query is
                refused with a message asking for a filter, rather than drawing a
                total that silently misses part of the series. The last point of
                a <code>rate</code> chart covers an unfinished interval and reads
                lower than the others.
              </div>
              <p>
                <strong>On a dashboard.</strong> A chart widget holds the same
                queries and expression as the explorer, so a breakdown or a ratio
                you built there becomes a permanent panel.
              </p>
              <p>
                <strong>Alerting.</strong> An alert rule can watch a custom
                metric instead of a built-in signal, with the same aggregation,
                window, warning and critical thresholds. Set{' '}
                <code>custom_metric</code> and optionally{' '}
                <code>custom_labels</code> when creating the rule through the{' '}
                <a href='#api'>API</a>:
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  POST /api/v1/organizations/:org/alert-rules
                </div>
                <CodeBlock
                  language='json'
                  code={`{
  "name": "Checkout error rate",
  "type": "threshold",
  "target_type": "any",
  "metric": "custom",
  "custom_metric": "http_requests_total",
  "custom_labels": [{ "key": "route", "value": "/checkout" }],
  "aggregation": "p95",
  "operator": "gt",
  "warning_threshold": 100,
  "critical_threshold": 500,
  "duration": 300,
  "enabled": true,
  "channels": [1]
}`}
                />
              </div>
              <p>
                When <code>custom_metric</code> is set the rule reads the series
                store and the built-in <code>metric</code> field is ignored,
                though it stays required by the schema. Leaving{' '}
                <code>custom_labels</code> empty watches the metric across every
                label combination.
              </p>
              <div className='doc-callout'>
                Custom metric rules are created through the API only for now —
                the dashboard form and the Terraform provider cover built-in
                signals. The rule itself, once created, appears and fires like
                any other.
              </div>
            </div>

            <div id='limits' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_limits')}
                <ShareAnchor />
              </h3>
              <p>
                Metrics are metered in <strong>points per minute</strong>.
                Every host your plan includes brings{' '}
                <strong>250 points per minute</strong>, shared by the whole
                organization: a single busy host can use the budget the others
                leave unused.
              </p>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Plan</th>
                    <th>Hosts</th>
                    <th>Metric points per minute</th>
                    <th>Retention</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>Free</td>
                    <td>1</td>
                    <td>250</td>
                    <td>7 days</td>
                  </tr>
                  <tr>
                    <td>Pro</td>
                    <td>10</td>
                    <td>2,500</td>
                    <td>30 days</td>
                  </tr>
                  <tr>
                    <td>Custom</td>
                    <td>Purchased hosts</td>
                    <td>Purchased hosts × 250</td>
                    <td>Chosen at purchase</td>
                  </tr>
                </tbody>
              </table>
              <p>
                <strong>What counts as a point.</strong> One value of one
                series received as an OTLP metric: what the agent scrapes from
                Prometheus exporters, its own system metrics, and the metrics
                your SDKs export. A histogram point counts twice, as it is
                stored as <code>_count</code> and <code>_sum</code>. Checks,
                logs, traces, errors and profiles are not counted.
              </p>
              <p>
                <strong>Going over.</strong> Within a minute, the points past
                the limit are rejected and the others are stored. The request
                is still answered <code>200</code>, with the standard OTLP{' '}
                <code>partial_success</code> field carrying the number of
                rejected points and this message, which the agent and the SDKs
                write to their logs:
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Agent log</div>
                <CodeBlock
                  language='text'
                  code={`1240 points rejected: this organization is over its ingestion limit of 2500 points per minute (250 per host of its plan). Drop unused series with drop_metrics, scrape less often, or upgrade the plan. See https://middlemonitor.io/docs#limits`}
                />
              </div>
              <p>
                Nothing is retried, since the same points would be refused again,
                and the next minute starts with a full budget.{' '}
                <strong>Settings → Resource usage</strong> shows the busiest
                minute of the last hour against the limit, and how many points
                were rejected over the last 24 hours.
              </p>
              <p>
                <strong>Seeing where it goes.</strong> Each host page has a{' '}
                <strong>Metric ingestion</strong> table: points per minute per
                scrape target, its series, its interval and its share of the
                limit, with the change that would save the most and the YAML to
                paste in the agent config.
              </p>
              <p>
                <strong>The agent adapts.</strong> Once the limit is enforced,
                an up-to-date agent learns its organization&apos;s usage every
                minute and, when it is over, lengthens the interval of its
                heaviest targets (up to 8 times) until its share fits, instead of
                letting points be rejected. Light targets and system metrics keep
                their rate, and the intervals come back once usage drops under
                70% of the limit. Every change is written to the agent log with
                the cost of each target.
              </p>
              <p>
                <strong>Staying under it.</strong> A scrape costs its number of
                series times its number of scrapes per minute: an exporter with
                3,000 series scraped every 15 seconds uses 12,000 points per
                minute on its own. Histogram buckets are usually most of it.
                Drop what you do not chart, or scrape less often:
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  /etc/middle-monitor/config.yaml
                </div>
                <CodeBlock
                  language='yaml'
                  code={`scrape:
  targets:
    - name: dns_exporter
      url: http://localhost:15353/metrics
      interval: 60           # 4 times fewer points than every 15s
      drop_metrics:
        - ".*_bucket"        # histogram buckets, often most of the series`}
                />
              </div>
            </div>
          </section>

          <hr className='doc-divider' />

          {/* MONITORING */}
          <section id='monitoring' className='doc-section'>
            <h2>
              {t('docs.sidebar_monitoring')}
              <ShareAnchor />
            </h2>
            <p>{t('docs.sidebar_monitoring_desc')}</p>

            <div id='services' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_services')}
                <ShareAnchor />
              </h3>
              <p>
                A <strong>Service</strong> represents an endpoint, database, or
                certificate to monitor. Attach it to a host and pick a check
                type below (HTTP, Ping, SQL, SSL Certificate, or SNMP) — the
                backend then polls it periodically, no agent required on the
                target machine.
              </p>
            </div>

            <div id='checks' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_checks')}
                <ShareAnchor />
              </h3>
              <p>
                Checks are executed periodically by the Middle Monitor backend
                workers — no agent is required on the target. Each check type
                has its own set of configurable parameters:
              </p>
              <table
                className='doc-api-table'
                style={{ marginBottom: '1.5rem' }}>
                <thead>
                  <tr>
                    <th>Type</th>
                    <th>Protocol</th>
                    <th>What it verifies</th>
                    <th>Key parameters</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>HTTP / HTTPS</td>
                    <td>TCP + TLS</td>
                    <td>Status code, response time, optional keyword match</td>
                    <td>URL, keyword, latency threshold, auth</td>
                  </tr>
                  <tr>
                    <td>Ping</td>
                    <td>ICMP</td>
                    <td>Network reachability and round-trip latency</td>
                    <td>Host / IP, latency threshold</td>
                  </tr>
                  <tr>
                    <td>SQL</td>
                    <td>TCP</td>
                    <td>
                      Connection check plus built-in diagnostics (locks,
                      replication lag, connection saturation) — no user-provided
                      query
                    </td>
                    <td>Host, port, credentials, database name</td>
                  </tr>
                  <tr>
                    <td>SSL Certificate</td>
                    <td>TLS</td>
                    <td>Certificate validity and expiry date</td>
                    <td>Domain, days-before-expiry threshold</td>
                  </tr>
                  <tr>
                    <td>SNMP</td>
                    <td>UDP 161</td>
                    <td>
                      OID value reachability and optional numeric threshold
                    </td>
                    <td>Host / IP, community string, OID</td>
                  </tr>
                </tbody>
              </table>

              <div id='check-http' className='doc-check-detail'>
                <h4>HTTP / HTTPS check</h4>
                <p>
                  Sends a periodic HTTP(S) request to a URL and inspects the
                  response. It verifies the status code (a specific code or any
                  2xx by default), measures the response time against your
                  Warning / Critical thresholds (in ms), and can optionally
                  assert that the body
                  <strong> contains a keyword</strong> or{' '}
                  <strong>matches a JSON path</strong>. Supports Bearer and
                  Basic authentication when the endpoint is protected.
                </p>
              </div>

              <div id='check-ping' className='doc-check-detail'>
                <h4>Ping (ICMP) check</h4>
                <p>
                  Sends ICMP echo requests to a host or IP and measures the
                  round-trip latency. It alerts when the host becomes
                  unreachable or when latency exceeds your Warning / Critical
                  thresholds (in ms). Useful for raw network reachability where
                  there is no HTTP endpoint.
                </p>
              </div>

              <div id='check-sql' className='doc-check-detail'>
                <h4>SQL check</h4>
                <p>
                  Opens a connection to your database (host, port, database
                  name, optional credentials) and runs a lightweight query. It
                  reports the connection latency (in ms) against the Critical
                  threshold; Warnings are raised automatically from internal
                  heuristics (connection saturation, blocking locks, table
                  bloat, replication lag).
                </p>
              </div>

              <div id='check-certificate' className='doc-check-detail'>
                <h4>SSL certificate check</h4>
                <p>
                  Connects over TLS to a domain and inspects its certificate. It
                  alerts on the number of days remaining before expiry — the
                  comparison is <strong>inverted</strong>: fewer remaining days
                  is worse, so the Warning threshold (e.g. 30 days) is higher
                  than the Critical one (e.g. 7 days).
                </p>
              </div>

              <div id='check-snmp' className='doc-check-detail'>
                <h4>SNMP check</h4>
                <p>
                  Queries a numeric OID over SNMP (UDP 161) on a device, using
                  an optional community string. It verifies that the device
                  responds and, when a threshold is set, alerts when the
                  returned numeric value exceeds it. Left without a threshold,
                  it only checks that the device is reachable.
                </p>
              </div>

              <p>
                Each check supports an optional <strong>Warning</strong> and{' '}
                <strong>Critical</strong> threshold, set when you create or edit
                the service. What the threshold compares against depends on the
                check type:
              </p>
              <table
                className='doc-api-table'
                style={{ marginBottom: '1.5rem' }}>
                <thead>
                  <tr>
                    <th>Type</th>
                    <th>Metric evaluated</th>
                    <th>Unit</th>
                    <th>Default warning</th>
                    <th>Default critical</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>HTTP / HTTPS</td>
                    <td>Response time</td>
                    <td>ms</td>
                    <td>1000</td>
                    <td>3000</td>
                  </tr>
                  <tr>
                    <td>Ping</td>
                    <td>Round-trip latency</td>
                    <td>ms</td>
                    <td>100</td>
                    <td>300</td>
                  </tr>
                  <tr>
                    <td>Agent CPU</td>
                    <td>CPU usage</td>
                    <td>%</td>
                    <td>80</td>
                    <td>95</td>
                  </tr>
                  <tr>
                    <td>Agent RAM</td>
                    <td>RAM usage</td>
                    <td>%</td>
                    <td>80</td>
                    <td>95</td>
                  </tr>
                  <tr>
                    <td>Agent Disk</td>
                    <td>Disk usage</td>
                    <td>%</td>
                    <td>80</td>
                    <td>95</td>
                  </tr>
                  <tr>
                    <td>Agent Network</td>
                    <td>Latency</td>
                    <td>ms</td>
                    <td>100</td>
                    <td>300</td>
                  </tr>
                  <tr>
                    <td>SNMP</td>
                    <td>Numeric OID value</td>
                    <td>OID-native</td>
                    <td>—</td>
                    <td>—</td>
                  </tr>
                  <tr>
                    <td>SSL Certificate</td>
                    <td>Days until expiry</td>
                    <td>days</td>
                    <td>30</td>
                    <td>7</td>
                  </tr>
                  <tr>
                    <td>SQL</td>
                    <td>Connection latency</td>
                    <td>ms</td>
                    <td>auto</td>
                    <td>1000</td>
                  </tr>
                </tbody>
              </table>
              <div className='doc-callout'>
                <ul style={{ margin: 0, paddingLeft: '1.2rem' }}>
                  <li>
                    <strong>Applied when left empty.</strong> A threshold you do
                    not set at creation falls back to the default above — on
                    every path: the app form (which also pre-fills the values so
                    you can tune them), the REST API, the SDKs and Terraform.
                  </li>
                  <li>
                    <strong>Warning must be lower than Critical</strong> for
                    every check except SSL Certificate, where the comparison is
                    inverted — fewer remaining days is worse, so Warning (30) is
                    higher than Critical (7).
                  </li>
                  <li>
                    <strong>SQL</strong> uses only the Critical threshold
                    (connection latency in ms). Warnings are raised
                    automatically from internal heuristics: connection
                    saturation, blocking locks, table bloat, replication lag.
                  </li>
                  <li>
                    <strong>SNMP</strong> is the only check with no default (the
                    value depends on the OID/device) and compares the raw
                    numeric value returned, not latency. An SNMP check left
                    without a threshold only verifies that the device responds.
                  </li>
                </ul>
              </div>
            </div>
          </section>

          <hr className='doc-divider' />

          {/* APPLICATIONS */}
          <section id='applications' className='doc-section'>
            <h2>
              {t('docs.sidebar_applications')}
              <ShareAnchor />
            </h2>
            <p>{t('docs.sidebar_applications_desc')}</p>

            <div id='sdk' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_sdk')}
                <ShareAnchor />
              </h3>
              <p>
                Middle Monitor SDKs are built on top of{' '}
                <strong>OpenTelemetry (OTLP)</strong> and add Middle
                Monitor-specific features (error grouping, profiling, RCA
                correlation). Create an <em>Error Service</em> from the{' '}
                <strong>Applications (APM) → Errors</strong> page to get its
                token — the same token authenticates errors, traces, and
                profiles sent for that service.
              </p>
              <div className='doc-callout'>
                The <strong>Node.js</strong> SDK (
                <code>@middle-monitor/sdk</code>) is written in TypeScript and
                ships its own type definitions — there is no separate TypeScript
                package. Use it from plain JavaScript or TypeScript on any
                Node.js runtime.
              </div>
              <LangTabs />
              <div className='doc-code-block'>
                <div className='doc-code-header'>SDK initialization</div>
                {lang === 'go' && (
                  <CodeBlock
                    language='go'
                    code={`import "github.com/middle-monitor/sdk-go"

func main() {
    middlemonitor.InitWithConfig(
        "${API_URL}",
        "api-backend",
        "your_service_token",
    )
}`}
                  />
                )}
                {lang === 'node' && (
                  <CodeBlock
                    language='tsx'
                    code={`import { initWithConfig } from '@middle-monitor/sdk';

initWithConfig(
  '${API_URL}',
  'api-backend',
  'your_service_token',
);`}
                  />
                )}
                {lang === 'python' && (
                  <CodeBlock
                    language='python'
                    code={`from middlemonitor import init_with_config

init_with_config(
    api_url="${API_URL}",
    service="api-backend",
    token="your_service_token",
)`}
                  />
                )}
                {lang === 'rust' && (
                  <CodeBlock
                    language='rust'
                    code={`use middle_monitor_sdk::init_with_config;

init_with_config(
    "${API_URL}".to_string(),
    "api-backend".to_string(),
    Some("your_service_token".to_string()),
);`}
                  />
                )}
              </div>
              <div className='doc-callout'>
                The token can also be provided via the{' '}
                <code>MIDDLE_MONITOR_TOKEN</code> environment variable.
              </div>
              <p style={{ marginTop: '1rem' }}>
                <strong>Environment variable reference.</strong> All four SDKs
                read the same variables when initialized without an explicit
                config (<code>InitSimple</code> / <code>initSimple</code> /{' '}
                <code>init_simple</code> / <code>init_simple</code>), each
                falling back to the matching OpenTelemetry standard variable if
                unset.
              </p>
              <table
                className='doc-param-table'
                style={{ marginBottom: '1rem' }}>
                <thead>
                  <tr>
                    <th>Variable</th>
                    <th>Falls back to</th>
                    <th>Default</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>MIDDLE_MONITOR_API_URL</td>
                    <td>OTEL_EXPORTER_OTLP_ENDPOINT</td>
                    <td>{API_URL}</td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_SERVICE</td>
                    <td>OTEL_SERVICE_NAME</td>
                    <td>unknown</td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_TOKEN</td>
                    <td>
                      OTEL_EXPORTER_OTLP_HEADERS (parses{' '}
                      <code>Authorization=Bearer ...</code>)
                    </td>
                    <td>—</td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_PROTOCOL</td>
                    <td>OTEL_EXPORTER_OTLP_PROTOCOL</td>
                    <td>http</td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_TRACES_SAMPLING</td>
                    <td>—</td>
                    <td>
                      1 (100%); range -1 to 1, see <code>Sampling</code> config
                    </td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_LOGS_LEVELS</td>
                    <td>—</td>
                    <td>
                      comma-separated, e.g. <code>WARN,ERROR,FATAL,PANIC</code>
                    </td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_LOGS_MIN_HTTP_STATUS</td>
                    <td>—</td>
                    <td>integer, e.g. 400</td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_HOSTNAME</td>
                    <td>—</td>
                    <td>
                      OS hostname; set it in a container, where the OS hostname
                      is the container ID
                    </td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_CLIENT_IP</td>
                    <td>—</td>
                    <td>
                      <code>anonymized</code>; also <code>full</code> or{' '}
                      <code>off</code>, see{' '}
                      <a href='#client-ip'>Caller address</a>
                    </td>
                  </tr>
                  <tr>
                    <td>MIDDLE_MONITOR_PPROF_URL</td>
                    <td>—</td>
                    <td>
                      — <strong>Go SDK only</strong>, only to scrape an external
                      pprof server, see{' '}
                      <a href='#profiling'>Continuous Profiling</a>
                    </td>
                  </tr>
                </tbody>
              </table>
              <p style={{ marginTop: '1rem' }}>
                <strong>Direct OTLP export (no SDK).</strong> The ingestion
                endpoint also accepts standard OpenTelemetry OTLP/HTTP: point
                any OTLP exporter or Collector at{' '}
                <code>{API_URL}/v1/traces</code>, <code>{API_URL}/v1/logs</code>
                , and <code>{API_URL}/v1/metrics</code> with the header{' '}
                <code>Authorization: Bearer &lt;service token&gt;</code>. Traces
                show up in <strong>Traces</strong>, logs are searchable in{' '}
                <strong>Logs</strong>, and metrics feed the{' '}
                <strong>Metrics Explorer</strong>.
              </p>
              <p style={{ marginTop: '1rem' }}>
                <strong>Logs and errors are linked to their trace.</strong> When
                a log or an error is emitted while a span is active, the SDK
                attaches that span&apos;s <code>trace_id</code>, so a line in{' '}
                <strong>Logs</strong> and an entry in <strong>Errors</strong>{' '}
                both point back to the request that produced them. This needs an
                active span, so install the HTTP middleware for your framework —
                the linkage then happens on its own, with nothing to pass by
                hand. Outside a request, logs and errors carry no trace id at
                all rather than an empty one.
              </p>
              <p style={{ marginTop: '1rem' }}>
                <strong>Failed requests are logged for you.</strong> Every HTTP
                middleware (Echo, Gin, net/http, Express, Flask, axum) writes one
                line per failed request to <strong>Logs</strong> —{' '}
                <code>GET /api/orders 500: pq: duplicate key</code> — carrying
                the method, route, status, duration and trace id. Successful
                traffic stays out (that volume is what <strong>Traces</strong>{' '}
                counts) and so do health probes; widen it per level or per route
                with <code>MIDDLE_MONITOR_LOGS_LEVELS</code> or{' '}
                <code>Sampling.Logs.AlwaysCaptureRoutes</code>. Together with{' '}
                <code>MIDDLE_MONITOR_HOSTNAME</code>, this is what lets a CPU or
                memory spike on a host be lined up with the traffic of the
                services running on it.
              </p>
              <p id='client-ip' style={{ marginTop: '1rem' }}>
                <strong>Caller address on failed requests.</strong> Those request
                log lines carry a <code>client.ip</code> attribute, which is what
                tells a wall of 404s on <code>/wp-login.php</code> apart from a
                real user hitting a broken page. It is read from{' '}
                <code>CF-Connecting-IP</code>, <code>True-Client-IP</code>,{' '}
                <code>X-Forwarded-For</code> or <code>X-Real-IP</code> before
                falling back to the socket, so a service behind a reverse proxy
                records the caller and not the proxy. An IP address is personal
                data, so the default is <code>anonymized</code>: only the network
                is kept (<code>203.0.113.42</code> becomes{' '}
                <code>203.0.113.0</code>, an IPv6 address is cut to its /48).
                Set <code>MIDDLE_MONITOR_CLIENT_IP=full</code> to record the
                whole address — that needs a legal basis of its own and a mention
                in your privacy policy — or <code>off</code> to record none.
              </p>
              <p style={{ marginTop: '1rem' }}>
                <strong>Source code.</strong> The platform itself is open
                source (
                <a
                  href={`${GITHUB_ORG}/middle-monitor`}
                  target='_blank'
                  rel='noopener noreferrer'>
                  middle-monitor/middle-monitor
                </a>
                ), and so are every SDK and the Terraform provider:
              </p>
              <table className='doc-param-table'>
                <thead>
                  <tr>
                    <th>Package</th>
                    <th>Install name</th>
                    <th>Repository</th>
                  </tr>
                </thead>
                <tbody>
                  {SDK_REPOS.map((repo) => (
                    <tr key={repo.url}>
                      <td>{repo.label}</td>
                      <td>
                        <code>{repo.pkg}</code>
                      </td>
                      <td>
                        <a
                          href={repo.url}
                          target='_blank'
                          rel='noopener noreferrer'>
                          {repo.url.replace('https://github.com/', '')}
                        </a>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div id='sdk-web' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_sdk_web')}
                <ShareAnchor />
              </h3>
              <p>
                <code>@middle-monitor/web</code> reports frontend errors and
                traces <code>fetch</code> / <code>XMLHttpRequest</code> calls
                from the browser. It is framework-agnostic: React, Vue, Angular,
                Svelte and plain JavaScript all use the same setup, because the
                SDK hooks into the browser rather than into a framework.
              </p>
              <div className='doc-callout'>
                This is a separate package from{' '}
                <code>@middle-monitor/sdk</code>: the Node.js SDK pulls{' '}
                <code>@opentelemetry/sdk-node</code> and cannot be bundled for a
                browser. Use the Node SDK on your server, this one in the page.
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Installation</div>
                <CodeBlock language='bash' code={'npm install @middle-monitor/web'} />
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Initialization</div>
                <CodeBlock
                  language='tsx'
                  code={`import { init } from '@middle-monitor/web';

init({
  apiUrl: '${API_URL}',
  service: 'shop-web',
  token: import.meta.env.VITE_MIDDLE_MONITOR_TOKEN,
});`}
                />
              </div>
              <p>
                Call it once, as early as possible — before your app mounts, so
                a crash during the first render is still caught. From there
                uncaught errors and unhandled promise rejections land in{' '}
                <strong>Errors</strong>, and outgoing requests appear in{' '}
                <strong>Traces</strong>.
              </p>
              <div className='doc-callout'>
                <strong>Use a service token, never an org API key.</strong> A
                browser bundle is public: whatever token you ship is readable by
                every visitor, so it must be one that can only ingest. An{' '}
                <code>mm_</code> org API key would give any visitor read and
                write access to your whole organization.
              </div>
              <p>
                <strong>Reporting an error yourself.</strong> Every framework
                catches render errors before they reach <code>window</code>, so
                the global handler never sees them. Wire{' '}
                <code>captureError</code> into the framework's own hook to close
                that gap.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  React error boundary / Vue / Angular / Svelte
                </div>
                <CodeBlock
                  language='tsx'
                  code={`import { captureError } from '@middle-monitor/web';

// React
componentDidCatch(error, info) {
  captureError(error, { file: info.componentStack.trim().split('\\n')[0] });
}

// Vue
app.config.errorHandler = (err, instance, info) => captureError(err, { file: info });

// Angular — implements ErrorHandler
handleError(error: unknown) { captureError(error); }

// SvelteKit — src/hooks.client.ts
export function handleError({ error }) { captureError(error); }`}
                />
              </div>
              <p>
                <strong>Linking frontend traces to your backend.</strong> The
                SDK only adds the W3C <code>traceparent</code> header to
                same-origin requests by default, because a cross-origin request
                carrying an unexpected header fails the target's CORS preflight.
                If your API is on another origin, list it and allow the header
                server-side with{' '}
                <code>Access-Control-Allow-Headers: traceparent</code>:
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Cross-origin propagation</div>
                <CodeBlock
                  language='tsx'
                  code={`init({
  apiUrl: '${API_URL}',
  service: 'shop-web',
  token: import.meta.env.VITE_MIDDLE_MONITOR_TOKEN,
  propagateTraceHeaderCorsUrls: [/^https:\\/\\/api\\.myshop\\.com/],
});`}
                />
              </div>
              <p>
                Once it works, a browser span and the server span it triggered
                belong to the same trace: a failed request in the UI leads
                straight to the handler that failed.
              </p>
              <table className='doc-param-table'>
                <thead>
                  <tr>
                    <th>Option</th>
                    <th>Default</th>
                    <th>Description</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>
                      <code>service</code>
                    </td>
                    <td>required</td>
                    <td>Service name shown in the dashboard.</td>
                  </tr>
                  <tr>
                    <td>
                      <code>apiUrl</code>
                    </td>
                    <td>
                      <code>{API_URL}</code>
                    </td>
                    <td>Ingestion URL.</td>
                  </tr>
                  <tr>
                    <td>
                      <code>token</code>
                    </td>
                    <td>none</td>
                    <td>Service token. Reports land nowhere without it.</td>
                  </tr>
                  <tr>
                    <td>
                      <code>tracesSampleRate</code>
                    </td>
                    <td>0.1</td>
                    <td>
                      Share of fetch/XHR traces kept, 0 to 1. Errors are never
                      sampled out.
                    </td>
                  </tr>
                  <tr>
                    <td>
                      <code>propagateTraceHeaderCorsUrls</code>
                    </td>
                    <td>[]</td>
                    <td>
                      Cross-origin URLs allowed to receive{' '}
                      <code>traceparent</code>.
                    </td>
                  </tr>
                  <tr>
                    <td>
                      <code>ignoreUrls</code>
                    </td>
                    <td>[]</td>
                    <td>
                      URLs never traced. The Middle Monitor endpoint is always
                      ignored.
                    </td>
                  </tr>
                  <tr>
                    <td>
                      <code>captureGlobalErrors</code>
                    </td>
                    <td>true</td>
                    <td>
                      Install <code>window</code> error and rejection handlers.
                    </td>
                  </tr>
                  <tr>
                    <td>
                      <code>timeout</code>
                    </td>
                    <td>5000</td>
                    <td>Span export timeout in milliseconds.</td>
                  </tr>
                </tbody>
              </table>
              <p>
                <strong>Sampling.</strong> Unlike the server SDKs, the browser
                cannot keep a trace <em>because</em> it failed: the sampling
                decision is made when a request starts, and its status only
                exists once it ends. Set the rate to <code>1</code> on a
                low-traffic frontend if you want every request. Reported errors
                always reach the Errors view, whatever the rate.
              </p>
            </div>

            <div id='errors' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_errors')}
                <ShareAnchor />
              </h3>
              <p>
                Wire the global capture once at startup to report{' '}
                <strong>unhandled panics / exceptions</strong>, and manually
                report caught errors where you handle them. Reported errors
                appear in the Middle Monitor UI for faster triage.
              </p>
              <LangTabs />
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  Global capture + manual reporting + HTTP 5xx capture
                </div>
                {lang === 'go' && (
                  <CodeBlock
                    language='go'
                    code={`// 1. Global panic capture — add once at the top of main()
defer middlemonitor.CapturePanicGlobal()

// 2. Manually report a caught error
func HandleCheckout(ctx context.Context, order Order) error {
    if err := chargeCard(ctx, order); err != nil {
        middlemonitor.ReportError(err)
        return err
    }
    return nil
}

// 3. Automatic HTTP 5xx capture — the Echo/Gin/net-http middleware
// (see Traces) also reports server errors to the Errors view`}
                  />
                )}
                {lang === 'node' && (
                  <CodeBlock
                    language='tsx'
                    code={`import { capturePanicGlobal, reportError, shutdown } from '@middle-monitor/sdk';
import { expressMiddleware } from '@middle-monitor/sdk/expressMiddleware';

// 1. Global capture of uncaught exceptions — add once at startup
process.on('uncaughtException', (err) => capturePanicGlobal(err));

// 2. Manually report a caught error
async function handleCheckout(order: Order): Promise<void> {
    try {
        await chargeCard(order);
    } catch (err) {
        reportError(err as Error);
        throw err;
    }
}

// 3. Automatic HTTP 5xx capture + request traces (Express middleware)
app.use(expressMiddleware());

// 4. Scripts, cron jobs, lambdas: flush before exiting
await shutdown();`}
                  />
                )}
                {lang === 'python' && (
                  <CodeBlock
                    language='python'
                    code={`import sys
from middlemonitor import capture_panic_global, report_error
from middlemonitor.flask_middleware import instrument_flask

# 1. Global capture of unhandled exceptions — add once at startup
def handle_uncaught(exc_type, exc, tb):
    capture_panic_global()
    sys.__excepthook__(exc_type, exc, tb)

sys.excepthook = handle_uncaught

# 2. Manually report a caught error
def handle_checkout(request, order):
    try:
        charge_card(order)
    except Exception as e:
        report_error(e)
        raise

# 3. Automatic HTTP 5xx capture + request traces (Flask middleware)
instrument_flask(app)`}
                  />
                )}
                {lang === 'rust' && (
                  <CodeBlock
                    language='rust'
                    code={`use middle_monitor_sdk::{capture_panic_global, report_error};

// 1. Global panic hook — add once at startup
std::panic::set_hook(Box::new(|info| capture_panic_global(info)));

// 2. Manually report a caught error
async fn handle_checkout(order: &Order) -> Result<Receipt, String> {
    match charge_card(order) {
        Ok(receipt) => Ok(receipt),
        Err(e) => {
            report_error(&e).await.ok();
            Err(e)
        }
    }
}

// 3. Automatic HTTP 5xx capture + request traces (axum middleware,
// feature "axum")
// let app = app.layer(from_fn(middle_monitor_sdk::axum_middleware::middleware));`}
                  />
                )}
              </div>
              <p>{t('docs.errors_stacktrace')}</p>
            </div>

            <div id='correlation-links' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_correlation_links')}
                <ShareAnchor />
              </h3>
              <p>
                A correlation link connects one of your applications (a{' '}
                <strong>service</strong>, e.g. <code>api</code>) to the
                monitored <strong>hosts and services</strong> it depends on (its
                database, cache, an upstream API, the machine it runs on), or
                directly to <strong>another application</strong> it calls (e.g.{' '}
                <code>checkout</code> → <code>payment</code>). Once linked, the
                <strong> Error details</strong> panel shows what was happening
                around the resource at the moment of the error — so a raw
                stacktrace becomes{' '}
                <em>
                  "this 500 happened while the DB host's disk was 95% full"
                </em>{' '}
                or <em>"the upstream payment service had already failed"</em>.
              </p>
              <p>
                <strong>Where to configure</strong>
              </p>
              <p>
                Open the <strong>Errors</strong> tab. It lists every app that
                reports errors as a table — status, name, whether it's
                registered as an Error Service, error count and last-seen for
                the selected date range. Click an app's row to open its page.
              </p>
              <p>
                That app's page shows its error groups (name, message, event
                count, the same occurrence sparkline as before) and, below that,
                a <strong>Correlation links</strong> card scoped to just this
                app — no picker, since you're already looking at it:
              </p>
              <ul style={{ paddingLeft: '1.2rem', lineHeight: 1.7 }}>
                <li>
                  <strong>Suggested from traces</strong> — loads automatically.
                  Middle Monitor infers the hosts and services this app talks to
                  from your distributed traces and proposes them; click{' '}
                  <strong>Accept</strong> to link, or <strong>Ignore</strong> to
                  dismiss. If nothing shows up, the card tells you why: no
                  tracing pipeline configured, or tracing is set up but this app
                  has no recent spans yet.
                </li>
                <li>The app's existing links, listed below the suggestions.</li>
              </ul>
              <p>
                To add a link by hand — useful for dependencies traces can't
                see, like a database with no instrumentation, or a direct call
                to another of your apps — click <strong>Add correlation</strong>{' '}
                at the top of the page. It opens a small popup: pick a target
                type (host, host group, service, or <strong>application</strong>
                ) and a target, then <strong>Add</strong>. The app is already
                fixed to whichever one you're on. Application targets are
                identified by their service tag, so you can link to any app that
                has reported errors — whether or not it was registered via{' '}
                <strong>Add Application</strong>.
              </p>
              <p>
                <strong>What you get back</strong>: the{' '}
                <strong>Error details</strong> panel then cross-checks every
                error against those links — a linked dependency that errored{' '}
                <em>just before</em> yours is flagged as the likely upstream
                cause (labeled <em>Dependency</em>, with how long before it
                fired), apps that depend on yours show up as downstream impact,
                and the panel also tells you whether the error is brand new (a
                likely regression) or a chronic one it has seen for days, with
                an overall confidence score on the diagnosis.
              </p>
              <div className='doc-callout'>
                Links are directional (<strong>app → resource</strong>) and only
                enrich error context — they do not create alerts on their own.
                The more accurately your apps are linked, the more useful the
                automatic root-cause context becomes. These signals also feed
                the <strong>AI root cause analysis</strong> on the error detail
                page: an LLM turns them into a ranked, human-readable
                explanation of the incident.
              </div>
            </div>

            <div id='traces' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_traces')}
                <ShareAnchor />
              </h3>
              <p>
                Distributed traces let you follow a request's full lifecycle
                across services and databases. The SDKs auto-instrument your
                HTTP layer via middleware: Echo, Gin and net/http in Go,
                Express in Node, Flask in Python, axum in Rust. For non-HTTP
                work (queue consumers, background jobs, DB calls), use the
                standard <strong>OpenTelemetry API</strong> to create manual
                spans — the SDK is built on top of it.
              </p>
              <LangTabs />
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  HTTP middleware + manual spans (OpenTelemetry)
                </div>
                {lang === 'go' && (
                  <CodeBlock
                    language='go'
                    code={`// Automatic HTTP tracing middleware (Echo)
e := echo.New()
e.Use(middlemonitor.EchoMiddleware())

// Gin is also supported
// r := gin.New()
// r.Use(middlemonitor.GinMiddleware())

// Plain net/http (ServeMux, gorilla/mux, chi, ...) too
// http.ListenAndServe(":8080", middlemonitor.HTTPMiddleware(mux))

// Manual span for non-HTTP work — standard OpenTelemetry API
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/codes"
)

func ProcessOrder(ctx context.Context, order Order) error {
    tracer := otel.Tracer("my-service")
    ctx, span := tracer.Start(ctx, "order.process")
    defer span.End()

    span.SetAttributes(
        attribute.String("order.id",     order.ID),
        attribute.Float64("order.amount", order.Amount),
    )

    if err := db.SaveOrder(ctx, order); err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
        return err
    }
    return nil
}`}
                  />
                )}
                {lang === 'node' && (
                  <CodeBlock
                    language='tsx'
                    code={`// Automatic HTTP tracing middleware (Express)
import { expressMiddleware } from '@middle-monitor/sdk/expressMiddleware';

app.use(expressMiddleware());

// Manual span for non-HTTP work — standard OpenTelemetry API
import { trace, SpanStatusCode } from '@opentelemetry/api';

async function processOrder(order: Order): Promise<void> {
    const tracer = trace.getTracer('my-service');
    const span = tracer.startSpan('order.process');
    span.setAttributes({ 'order.id': order.id, 'order.amount': order.amount });

    try {
        await db.saveOrder(order);
    } catch (err) {
        span.recordException(err as Error);
        span.setStatus({ code: SpanStatusCode.ERROR });
        throw err;
    } finally {
        span.end();
    }
}`}
                  />
                )}
                {lang === 'python' && (
                  <CodeBlock
                    language='python'
                    code={`# Automatic HTTP tracing middleware (Flask)
from middlemonitor.flask_middleware import instrument_flask

instrument_flask(app)

# Manual span for non-HTTP work — standard OpenTelemetry API
from opentelemetry import trace
from opentelemetry.trace import StatusCode

def process_order(order):
    tracer = trace.get_tracer('my-service')
    with tracer.start_as_current_span('order.process') as span:
        span.set_attribute('order.id',     order.id)
        span.set_attribute('order.amount', order.amount)
        try:
            db.save_order(order)
        except Exception as e:
            span.record_exception(e)
            span.set_status(StatusCode.ERROR, str(e))
            raise`}
                  />
                )}
                {lang === 'rust' && (
                  <CodeBlock
                    language='rust'
                    code={`// Automatic HTTP tracing middleware (axum)
// Cargo.toml: middle-monitor-sdk = { version = "0.1", features = ["axum"] }
use axum::middleware::from_fn;

let app = app.layer(from_fn(middle_monitor_sdk::axum_middleware::middleware));

// Manual span for non-HTTP work — standard OpenTelemetry API
use opentelemetry::{global, KeyValue};
use opentelemetry::trace::{Status, TraceContextExt, Tracer};

fn process_order(order: &Order) -> Result<(), String> {
    let tracer = global::tracer("my-service");
    tracer.in_span("order.process", |cx| {
        let span = cx.span();
        span.set_attribute(KeyValue::new("order.id", order.id.clone()));
        span.set_attribute(KeyValue::new("order.amount", order.amount));

        match db::save_order(order) {
            Ok(()) => Ok(()),
            Err(e) => {
                span.set_status(Status::error(e.clone()));
                Err(e)
            }
        }
    })
}`}
                  />
                )}
              </div>
            </div>

            <div id='profiling' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_profiling')}
                <ShareAnchor />
              </h3>
              <p>
                Continuous profiling captures <strong>Heap</strong> (memory) and{' '}
                <strong>CPU</strong> profiles from production services and
                displays them as interactive flame graphs in Middle Monitor. The
                Go SDK profiles the running process directly — no pprof HTTP
                server to start and secure.
              </p>
              <div className='doc-callout'>
                <strong>Go only, for now.</strong> Continuous profiling (
                <code>CaptureHeapProfile</code> / <code>CaptureCPUProfile</code>
                ) is currently implemented in the Go SDK only — the Node.js,
                Python, and Rust SDKs don't expose an equivalent yet, since none
                of them ship a built-in profiler comparable to Go's{' '}
                <code>runtime/pprof</code>.
              </div>
              <div className='doc-callout'>
                Profiling is not automatic — you add the snippet below to your
                own service to schedule the captures (heap on a ticker, CPU on
                demand). Middle Monitor only stores and renders the profiles
                your app sends.
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  {t('docs.profiling_example')}
                </div>
                <CodeBlock
                  language='go'
                  code={`func main() {
    // 1. Periodic heap snapshot — every 10 minutes
    go func() {
        ticker := time.NewTicker(10 * time.Minute)
        defer ticker.Stop()
        for range ticker.C {
            ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
            if c := middlemonitor.GetGlobalClient(); c != nil {
                c.CaptureHeapProfile(ctx)
            }
            cancel()
        }
    }()

    // 2. One-shot CPU profile after a load spike
    middlemonitor.GetGlobalClient().CaptureCPUProfile(context.Background(), 30*time.Second)
}`}
                />
              </div>
              <p>
                Both calls profile the current process. Set{' '}
                <code>Config.PprofURL</code> when initializing the client, or
                the <code>MIDDLE_MONITOR_PPROF_URL</code> environment variable,
                only to profile a <em>different</em> process through its pprof
                HTTP endpoint (e.g. a sidecar).
              </p>
              <p style={{ marginTop: '1.25rem' }}>
                <strong>Reading the profiles</strong>
              </p>
              <ul style={{ paddingLeft: '1.2rem', lineHeight: 1.7 }}>
                <li>
                  <strong>CPU Profile:</strong> identifies functions that
                  intensively use the processor. Ideal for spotting slow loops
                  or blocking requests.
                </li>
                <li>
                  <strong>Heap (Memory):</strong> maps RAM allocations. Useful
                  for tracking memory leaks that weaken stability.
                </li>
                <li>
                  <strong>Flame Graph:</strong> visually represents call
                  hierarchies. Read them by focusing on the widest bars, which
                  consume proportionally more resources.
                </li>
              </ul>
            </div>
          </section>

          <hr className='doc-divider' />

          {/* ALERTING */}
          <section id='alerting' className='doc-section'>
            <h2>
              {t('docs.sidebar_alerting')}
              <ShareAnchor />
            </h2>
            <p>{t('docs.sidebar_alerting_desc')}</p>

            <div id='channels' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_channels')}
                <ShareAnchor />
              </h3>
              <p>{t('docs.channels_desc')}</p>
              <div className='doc-integration-grid'>
                <div className='doc-integration-card'>
                  <div className='doc-integration-header'>
                    <strong>Email</strong>
                  </div>
                  <p>{t('docs.channels_email')}</p>
                  <p style={{ fontSize: '0.85rem', color: 'var(--doc-muted)' }}>
                    Configure your organization's SMTP server once under{' '}
                    <strong>Settings → Email / SMTP</strong>; email alerts are
                    then delivered through it. Without it, email alerts are not
                    sent.
                  </p>
                </div>
                <div className='doc-integration-card'>
                  <div className='doc-integration-header'>
                    <strong>Slack</strong>
                  </div>
                  <p>{t('docs.channels_slack')}</p>
                  <p style={{ fontSize: '0.85rem', color: 'var(--doc-muted)' }}>
                    Create an <em>Incoming Webhook</em> in your Slack workspace
                    and paste the URL in the channel configuration.
                  </p>
                  <a
                    href='https://api.slack.com/messaging/webhooks'
                    target='_blank'
                    rel='noopener noreferrer'
                    className='doc-integration-link'>
                    Slack Webhooks Documentation →
                  </a>
                </div>
                <div className='doc-integration-card'>
                  <div className='doc-integration-header'>
                    <strong>Jira Service Management (JSM)</strong>
                  </div>
                  <p>{t('docs.channels_jsm')}</p>
                  <p style={{ fontSize: '0.85rem', color: 'var(--doc-muted)' }}>
                    Generate an API key from your JSM/Opsgenie integration (type{' '}
                    <em>API</em>) and paste it into <code>api_key</code>. Alerts
                    are created via <code>jsm/ops/integration/v2/alerts</code>{' '}
                    and auto-closed on resolution.
                  </p>
                  <a
                    href='https://support.atlassian.com/jira-service-management-cloud/docs/set-up-an-api-integration/'
                    target='_blank'
                    rel='noopener noreferrer'
                    className='doc-integration-link'>
                    JSM Integrations Documentation →
                  </a>
                </div>
                <div className='doc-integration-card'>
                  <div className='doc-integration-header'>
                    <strong>WhatsApp</strong>
                  </div>
                  <p>{t('docs.channels_whatsapp')}</p>
                  <p style={{ fontSize: '0.85rem', color: 'var(--doc-muted)' }}>
                    Uses the Meta WhatsApp Business Cloud API. Enter your{' '}
                    <code>phone_number_id</code> and permanent access{' '}
                    <code>token</code>.
                  </p>
                  <a
                    href='https://developers.facebook.com/docs/whatsapp/cloud-api/get-started'
                    target='_blank'
                    rel='noopener noreferrer'
                    className='doc-integration-link'>
                    WhatsApp Cloud API Documentation →
                  </a>
                </div>
                <div className='doc-integration-card'>
                  <div className='doc-integration-header'>
                    <strong>Generic Webhook</strong>
                  </div>
                  <p>{t('docs.channels_webhook')}</p>
                  <p style={{ fontSize: '0.85rem', color: 'var(--doc-muted)' }}>
                    Middle Monitor sends a JSON <code>POST</code>. By default the
                    body is the Slack-compatible shape — <code>text</code>{' '}
                    (title) and <code>attachments</code> (body coloured by
                    severity) — which works with Discord, Teams and any HTTP
                    receiver. Set <code>format: structured</code> for the typed
                    payload below.
                  </p>
                </div>
              </div>
              <p style={{ marginTop: '1rem' }}>{t('docs.channels_toggle')}</p>

              <h4>A typed payload for automated receivers</h4>
              <p>
                The Slack shape encodes the severity in a colour and the
                resolution in a string prefix, and carries no incident id: a
                machine consuming it has to parse English. A webhook channel
                configured with <code>format: structured</code> receives the
                event itself, so a receiver can trigger a remediation and report
                back.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Channel configuration</div>
                <CodeBlock
                  language='json'
                  code={`{
  "webhook_url": "https://ops.example.com/hooks/middlemonitor",
  "secret": "the HMAC key your receiver verifies with",
  "format": "structured",
  "headers": { "Authorization": "Bearer gateway-token" },

  "group_by": ["host_id"],
  "group_wait": 30,
  "repeat_interval": 14400
}`}
                />
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>POST body</div>
                <CodeBlock
                  language='json'
                  code={`{
  "version": "1",
  "event": "incident.opened",
  "event_id": "evt_8f2c1a9b4d7e",
  "dedup_key": "mm-rule-5",
  "occurred_at": "2026-06-13T11:55:00Z",
  "title": "[critical] API latency critical",
  "message": "latency p95 = 2310 above threshold",
  "organization": { "slug": "acme" },
  "incident": {
    "id": 801,
    "title": "API latency critical on api-health",
    "status": "open",
    "previous_status": "",
    "severity": "critical",
    "started_at": "2026-06-13T11:55:00Z",
    "acknowledged_at": null,
    "resolved_at": null,
    "url": "https://middlemonitor.io/organizations/acme/incidents/801"
  },
  "alert_rule": {
    "id": 5, "name": "API latency critical", "metric": "latency",
    "aggregation": "p95", "operator": "gt",
    "threshold": 1500, "observed_value": 2310, "duration": 300
  },
  "host": { "id": 42, "name": "web-prod-01", "group": "frontends", "service": "api" },
  "service": { "id": 1337, "name": "api-health", "type": "http" },
  "labels": { "project": "demo" }
}`}
                />
              </div>
              <p>
                The JSON Schema of this payload is published at{' '}
                <a href='/schemas/webhook-payload.json'>
                  /schemas/webhook-payload.json
                </a>
                .
              </p>

              <h4>Events, signature and retries</h4>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Event</th>
                    <th>When it is sent</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>
                      <code>incident.opened</code>
                    </td>
                    <td>A rule breached its threshold and an incident opened</td>
                  </tr>
                  <tr>
                    <td>
                      <code>incident.escalated</code>
                    </td>
                    <td>An open warning incident now breaches the critical threshold</td>
                  </tr>
                  <tr>
                    <td>
                      <code>incident.acknowledged</code>
                    </td>
                    <td>Someone, or something, took the incident</td>
                  </tr>
                  <tr>
                    <td>
                      <code>incident.resolved</code>
                    </td>
                    <td>The condition cleared, or the incident was resolved by hand</td>
                  </tr>
                  <tr>
                    <td>
                      <code>incident.reopened</code>
                    </td>
                    <td>A resolved incident was moved back to open</td>
                  </tr>
                </tbody>
              </table>
              <p>
                Every request carries <code>X-Middmonitor-Timestamp</code> (unix
                seconds) and <code>X-Middmonitor-Signature-256</code>, which is{' '}
                <code>sha256=</code> followed by the hex HMAC-SHA256, keyed with
                the channel secret, of the bytes{' '}
                <code>&lt;timestamp&gt;.&lt;raw body&gt;</code>. Rejecting a
                request whose timestamp is outside your tolerance window is what
                makes a captured POST unusable later. The original{' '}
                <code>X-Middmonitor-Signature</code> header, the HMAC of the raw
                body alone, is still sent for receivers written against it.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Verifying a delivery</div>
                <CodeBlock
                  language='python'
                  code={`import hmac, hashlib, time

def verify(headers, raw_body, secret, tolerance=300):
    stamp = headers["X-Middmonitor-Timestamp"]
    if abs(time.time() - int(stamp)) > tolerance:
        return False                     # replayed capture
    expected = hmac.new(
        secret.encode(),
        f"{stamp}.".encode() + raw_body,
        hashlib.sha256,
    ).hexdigest()
    return hmac.compare_digest(
        "sha256=" + expected, headers["X-Middmonitor-Signature-256"]
    )`}
                />
              </div>
              <p>
                A delivery that fails on a transport error, a <code>5xx</code>{' '}
                or a <code>429</code> is retried with a growing backoff over
                roughly fifteen minutes; a <code>4xx</code> is not, since
                repeating it would only repeat the same mistake. Every attempt
                is recorded: read the recent deliveries of a channel with{' '}
                <code>
                  GET /notification-channels/&#123;id&#125;/deliveries
                </code>{' '}
                and re-send one with{' '}
                <code>
                  POST
                  /notification-channels/&#123;id&#125;/deliveries/&#123;deliveryID&#125;/replay
                </code>
                .
              </p>
              <p>
                A channel can also ask for a periodic{' '}
                <code>heartbeat</code> event, with{' '}
                <code>"heartbeat_interval": 300</code> in its configuration.
                It answers the most unpleasant failure of any alerting system:
                silence meaning "nothing is wrong" and silence meaning "the
                notification path is dead" look exactly alike. A receiver that
                stops seeing heartbeats knows which one it is in.
              </p>
              <p>
                When a host falls, its services fall with it.{' '}
                <code>group_by</code> and <code>group_wait</code> accumulate a
                burst for a few seconds and deliver it as one message carrying a{' '}
                <code>grouped</code> block, instead of twenty POSTs.{' '}
                <code>repeat_interval</code> stops a flapping rule from
                re-notifying — a resolution is never suppressed, so a receiver
                always learns when to close what it opened.
              </p>
            </div>

            <div id='incidents' className='doc-subsection'>
              <h3>
                {t('docs.sidebar_incidents')}
                <ShareAnchor />
              </h3>
              <p>{t('docs.incidents_desc')}</p>
              <ul>
                <li>
                  <strong>Trigger:</strong> {t('docs.incidents_trigger')}
                </li>
                <li>
                  <strong>Correlation:</strong>{' '}
                  {t('docs.incidents_correlation')}
                </li>
                <li>
                  <strong>Resolution:</strong> {t('docs.incidents_resolution')}
                </li>
              </ul>

              <h4>Acknowledging from an automated system</h4>
              <p>
                A remediation that reacts to a webhook needs to report what it
                did. <code>acknowledged_by</code> is a user id, so a machine
                says who it is with <code>actor</code> instead, and can leave a{' '}
                <code>note</code>.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  PUT /incidents/&#123;id&#125;/status
                </div>
                <CodeBlock
                  language='json'
                  code={`{
  "status": "acknowledged",
  "note": "auto remediation #4711 triggered",
  "actor": "automation"
}`}
                />
              </div>
              <div className='doc-callout'>
                <code>incident.acknowledged</code> and{' '}
                <code>incident.reopened</code> are delivered to{' '}
                <strong>webhook channels only</strong>. They exist so an
                automated receiver can follow the whole lifecycle; a human
                already sees an acknowledgement in the dashboard. Openings,
                escalations and resolutions still reach every channel.
              </div>
              <p>
                Acknowledging is <strong>idempotent</strong>: replaying it
                answers <code>200</code> and keeps the first acknowledgement
                time, which is what a retrying system needs. The answer carries
                the <code>dedup_key</code>, and{' '}
                <code>PUT /incidents/dedup/&#123;key&#125;/status</code> accepts
                that key in place of the numeric id, so a webhook receiver has
                nothing to remember between two messages.
              </p>
            </div>
          </section>

          <hr className='doc-divider' />

          {/* API REFERENCE */}
          <section id='api' className='doc-section'>
            <h2>
              REST API Reference
              <ShareAnchor />
            </h2>
            <p>
              The Middle Monitor REST API gives you programmatic access to every
              resource in the platform. All endpoints are versioned under{' '}
              <code>/api/v1</code> and return JSON. Organization-scoped
              resources are further nested under{' '}
              <code>/api/v1/organizations/&#123;slug&#125;/</code> where{' '}
              <code>slug</code> is your organization's unique identifier,
              visible in the URL after you log in. Resource IDs are{' '}
              <strong>numeric</strong> (e.g. <code>42</code>). List endpoints
              return plain JSON arrays.
            </p>
            <p>
              Standard HTTP status codes apply: <code>200</code> for reads (and
              some creates), <code>201</code> for most creates, <code>204</code>{' '}
              for deletes (no body), <code>400</code> for validation errors
              (body contains an <code>error</code> string), <code>401</code>{' '}
              when credentials are missing or expired, <code>403</code> when the
              authenticated identity lacks permission, <code>404</code> when the
              resource does not exist, and <code>422</code> when an action was
              accepted but could not complete (e.g. a failed channel test
              delivery).
            </p>

            <div id='api-auth' className='doc-subsection'>
              <h3>
                Authentication
                <ShareAnchor />
              </h3>
              <p>
                Two authentication methods are supported. Both use the{' '}
                <code>Authorization: Bearer</code> header — the server
                distinguishes a short-lived JWT from a long-lived API key (
                <code>mm_</code> prefix) automatically. Choose the one that fits
                your use case.
              </p>
              <div className='doc-auth-grid'>
                <div className='doc-auth-card'>
                  <strong>JWT Bearer Token</strong>
                  <p>
                    Obtained via <code>POST /api/v1/auth/login</code> (a
                    two-step flow when the account has 2FA enabled — see the
                    login endpoint below). Login returns{' '}
                    <strong>two distinct tokens</strong>: the{' '}
                    <strong>access token</strong> (valid{' '}
                    <strong>24 hours</strong>) is the one you pass as{' '}
                    <code>Authorization: Bearer &lt;access_token&gt;</code> on
                    every request; the <strong>refresh token</strong> (valid{' '}
                    <strong>7 days</strong>) is never sent on regular requests —
                    its only purpose is <code>POST /api/v1/auth/refresh</code>,
                    which exchanges it for a fresh pair when the access token
                    expires. Best for interactive sessions and browser-based
                    tooling.
                  </p>
                </div>
                <div className='doc-auth-card'>
                  <strong>API Key</strong>
                  <p>
                    Long-lived key created via <em>Settings → API Keys</em> or
                    via the API itself. Pass it as{' '}
                    <code>Authorization: Bearer mm_YOUR_KEY</code> — the same
                    header as JWT, the server distinguishes them automatically.
                    Permissions are not configured on the key: an{' '}
                    <strong>organization key</strong> has read and write access
                    to the organization's API (admin-only endpoints such as user
                    management and billing are never reachable with an
                    organization key), while a <strong>personal token</strong>{' '}
                    (created from <em>My account → Personal API tokens</em>)
                    authenticates as you, with your role. Expiry date is
                    mandatory. Recommended for CI/CD pipelines, Terraform, SDK
                    automation, and any non-interactive integration.
                  </p>
                </div>
              </div>

              <h4>Which token for what</h4>
              <p>
                Middle Monitor deliberately keeps separate credential types,
                each scoped to its job. In particular, the agent install token
                is low-privilege by design: it lives in a config file on every
                monitored machine, so a leaked token cannot read or modify
                anything in your organization.
              </p>
              <table className='doc-api-table'>
                <thead>
                  <tr>
                    <th>Token</th>
                    <th>Where</th>
                    <th>Use it for</th>
                    <th>Permissions</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>JWT session</td>
                    <td>/auth/login</td>
                    <td>Interactive sessions, browser tooling</td>
                    <td>
                      Your role; access token 24h, refresh token 7 days (two
                      distinct tokens, see <em>JWT Bearer Token</em> above)
                    </td>
                  </tr>
                  <tr>
                    <td>Personal token (mm_)</td>
                    <td>My account</td>
                    <td>Scripts and API calls made on your behalf</td>
                    <td>Your role (read_only, read_write or admin)</td>
                  </tr>
                  <tr>
                    <td>Organization key (mm_)</td>
                    <td>Settings → API Keys</td>
                    <td>CI/CD, Terraform, SDK ingestion, integrations</td>
                    <td>
                      Read and write on the organization's API; admin-only
                      endpoints excluded
                    </td>
                  </tr>
                  <tr>
                    <td>Service token</td>
                    <td>Service creation</td>
                    <td>
                      SDK error, trace and profile ingestion for that service
                    </td>
                    <td>Ingestion only</td>
                  </tr>
                  <tr>
                    <td>Agent install token</td>
                    <td>Settings → API Keys</td>
                    <td>Registering agents and pushing agent metrics</td>
                    <td>
                      Agent endpoints only. Revoking it immediately stops all
                      agents installed with it
                    </td>
                  </tr>
                </tbody>
              </table>

              <div className='doc-endpoint'>
                <div className='doc-endpoint-header'>
                  <span className='doc-method-badge doc-method-post'>POST</span>
                  <span className='doc-endpoint-path'>/api/v1/auth/login</span>
                </div>
                <p className='doc-endpoint-desc'>
                  Authenticates a user with email and password. There are two
                  possible outcomes, depending on the account:
                </p>
                <ul
                  className='doc-endpoint-desc'
                  style={{ paddingLeft: '1.2rem' }}>
                  <li>
                    <strong>2FA disabled</strong> — the response is the user
                    profile and a token pair. Include the access token in the{' '}
                    <code>Authorization</code> header for all subsequent
                    requests.
                  </li>
                  <li>
                    <strong>2FA enabled</strong> — the password alone is not
                    enough: the response is an <strong>MFA challenge</strong>{' '}
                    (see the second example below) and you must complete the
                    login via <code>POST /api/v1/auth/login/mfa</code> to get
                    the token pair.
                  </li>
                </ul>
                <p className='doc-endpoint-section-label'>Request body</p>
                <table className='doc-param-table'>
                  <thead>
                    <tr>
                      <th>Field</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>email</td>
                      <td>string</td>
                      <td>
                        The registered account email address.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>password</td>
                      <td>string</td>
                      <td>
                        The account password.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`curl -X POST ${API_URL}/api/v1/auth/login \\
  -H "Content-Type: application/json" \\
  -d '{"email":"you@example.com","password":"s3cr3t"}'`}
                  />
                </div>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>Response 200</div>
                  <CodeBlock
                    language='json'
                    code={`{
  "user": {
    "id":    12,
    "email": "you@example.com",
    "name":  "You",
    "role":  "admin"
  },
  "tokens": {
    "access_token":  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in":    86400
  }
}`}
                  />
                </div>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    Response 200 — account with 2FA enabled
                  </div>
                  <CodeBlock
                    language='json'
                    code={`{
  "mfa_required": true,
  "mfa_token":    "eyJhbGciOiJIUzI1..."
}`}
                  />
                </div>
                <p
                  className='doc-endpoint-desc'
                  style={{ marginTop: '0.75rem' }}>
                  Complete a 2FA login by sending the challenge token together
                  with the 6-digit code (or a recovery code) to{' '}
                  <code>POST /api/v1/auth/login/mfa</code> with body{' '}
                  <code>&#123;"mfa_token": "...", "code": "123456"&#125;</code>{' '}
                  — the response is the same <code>user</code> +{' '}
                  <code>tokens</code> object as a normal login. The challenge
                  token is short-lived (about 5 minutes).
                </p>
                <p className='doc-endpoint-section-label'>
                  Response fields (tokens)
                </p>
                <table className='doc-param-table'>
                  <thead>
                    <tr>
                      <th>Field</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>access_token</td>
                      <td>string</td>
                      <td>
                        JWT to pass as{' '}
                        <code>Authorization: Bearer &lt;access_token&gt;</code>.
                        Valid 24 hours.
                      </td>
                    </tr>
                    <tr>
                      <td>refresh_token</td>
                      <td>string</td>
                      <td>JWT used to obtain a fresh pair. Valid 7 days.</td>
                    </tr>
                    <tr>
                      <td>expires_in</td>
                      <td>integer</td>
                      <td>Seconds until the access token expires (86400)</td>
                    </tr>
                  </tbody>
                </table>
              </div>

              <div className='doc-endpoint'>
                <div className='doc-endpoint-header'>
                  <span className='doc-method-badge doc-method-post'>POST</span>
                  <span className='doc-endpoint-path'>
                    /api/v1/auth/refresh
                  </span>
                </div>
                <p className='doc-endpoint-desc'>
                  Exchanges a valid refresh token for a new access token +
                  refresh token pair. The two lifetimes serve different
                  purposes: the <strong>access token</strong> expires after{' '}
                  <strong>24 hours</strong>, and this endpoint is how you get a
                  new one without re-entering credentials; the{' '}
                  <strong>refresh token</strong> lives <strong>7 days</strong>{' '}
                  and is only ever sent here. Since each successful refresh
                  returns a new pair, a session stays alive indefinitely as long
                  as it refreshes at least once every 7 days. Only when the
                  refresh token itself has expired does this endpoint return{' '}
                  <code>401</code>, and the user must log in again.
                </p>
                <p className='doc-endpoint-section-label'>Request body</p>
                <table className='doc-param-table'>
                  <thead>
                    <tr>
                      <th>Field</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>refresh_token</td>
                      <td>string</td>
                      <td>
                        The refresh token obtained from <code>/auth/login</code>{' '}
                        or a previous <code>/auth/refresh</code>.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`curl -X POST ${API_URL}/api/v1/auth/refresh \\
  -H "Content-Type: application/json" \\
  -d '{"refresh_token":"eyJhbGciOiJIUzI1..."}'`}
                  />
                </div>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>Response 200</div>
                  <CodeBlock
                    language='json'
                    code={`{
  "user": { "id": 12, "email": "you@example.com" },
  "tokens": {
    "access_token":  "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in":    86400
  }
}`}
                  />
                </div>
              </div>

              <div className='doc-endpoint'>
                <div className='doc-endpoint-header'>
                  <span className='doc-method-badge doc-method-delete'>DELETE</span>
                  <span className='doc-endpoint-path'>/api/v1/auth/me</span>
                </div>
                <p className='doc-endpoint-desc'>
                  Deletes the caller's account. Organizations the caller is the
                  only member of are deleted with it. Returns <code>204</code>,{' '}
                  <code>403</code> on a wrong password, and <code>409</code>{' '}
                  when the caller is the last admin of an organization that has
                  other members.
                </p>
                <p className='doc-endpoint-section-label'>Request body</p>
                <table className='doc-param-table'>
                  <thead>
                    <tr>
                      <th>Field</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>password</td>
                      <td>string</td>
                      <td>
                        The caller's current password.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`curl -X DELETE ${API_URL}/api/v1/auth/me \\
  -H "Authorization: Bearer $TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"password":"..."}'`}
                  />
                </div>
              </div>

              <div className='doc-endpoint'>
                <div className='doc-endpoint-header'>
                  <span className='doc-method-badge doc-method-delete'>DELETE</span>
                  <span className='doc-endpoint-path'>
                    /api/v1/organizations/{'{org_slug}'}
                  </span>
                </div>
                <p className='doc-endpoint-desc'>
                  Deletes the organization and all its data, and cancels its
                  subscription. Admin only. Members of other organizations keep
                  their account. Returns <code>204</code>, or <code>400</code>{' '}
                  when <code>confirm</code> does not match the slug.
                </p>
                <p className='doc-endpoint-section-label'>Request body</p>
                <table className='doc-param-table'>
                  <thead>
                    <tr>
                      <th>Field</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>confirm</td>
                      <td>string</td>
                      <td>
                        The organization slug, repeated.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`curl -X DELETE ${API_URL}/api/v1/organizations/acme \\
  -H "Authorization: Bearer $TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"confirm":"acme"}'`}
                  />
                </div>
              </div>
            </div>

            <div id='api-endpoints' className='doc-subsection'>
              <h3>
                Endpoints
                <ShareAnchor />
              </h3>
              <p>
                All organization-scoped routes are prefixed with{' '}
                <code>
                  /api/v1/organizations/<strong>&#123;slug&#125;</strong>
                </code>
                . The examples below omit this prefix for readability — prepend
                it to every path shown.
              </p>
              <div className='doc-callout'>
                The complete API is described in an OpenAPI document served,
                without authentication, at{' '}
                <a href='/openapi.json'>/openapi.json</a> (and at{' '}
                <code>{API_URL}/api/v1/openapi.json</code>). Generate a client
                from it rather than writing one by hand: it is produced from the
                routes the server registers, so it cannot describe an endpoint
                that is not served.
              </div>

              <h4>Conventions for automated callers</h4>
              <ul>
                <li>
                  <strong>Pagination.</strong> List endpoints accept{' '}
                  <code>limit</code> (default 50) and <code>offset</code>, and
                  return the total in the <code>X-Total-Count</code> header.
                </li>
                <li>
                  <strong>Time range.</strong> <code>since</code> and{' '}
                  <code>until</code> bound a listing, in RFC 3339 or unix
                  seconds. Supported on <code>/incidents</code>,{' '}
                  <code>/errors</code> and{' '}
                  <code>/services/&#123;id&#125;/results</code>, which is what
                  lets a tool mirror the history instead of only reading the
                  latest page.
                </li>
                <li>
                  <strong>Conflicts.</strong> A host and a host group are unique
                  by name within an organization. Re-creating one answers{' '}
                  <code>409</code> with{' '}
                  <code>
                    &#123;"error": ..., "code": "already_exists", "id": 42&#125;
                  </code>
                  , so a converging tool can turn the failure into a read.
                  Services, alert rules and notification channels have{' '}
                  <strong>no natural key</strong>: replaying their creation
                  creates a second row, so read the list first.
                </li>
                <li>
                  <strong>Idempotency.</strong> Send an{' '}
                  <code>Idempotency-Key</code> header on a create and a retry
                  carrying the same key replays the first answer, marked{' '}
                  <code>Idempotent-Replay: true</code>, instead of creating a
                  second row. While the first call is still running the retry
                  gets <code>409</code> with{' '}
                  <code>"code": "idempotency_in_progress"</code>. Keys are scoped
                  to the organization and the endpoint, and kept for a day.
                </li>
                <li>
                  <strong>Rate limits.</strong> The authentication, contact and
                  public status endpoints are limited per IP; the
                  organization-scoped routes are not limited today. Every
                  limited answer carries <code>RateLimit-Limit</code>,{' '}
                  <code>RateLimit-Remaining</code> and{' '}
                  <code>RateLimit-Reset</code>, and a <code>429</code> also
                  carries <code>Retry-After</code>.
                </li>
              </ul>

              {/* ── HOST GROUPS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Host groups</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  A <strong>host group</strong> scopes alert correlation to a
                  host and its peers, so the grouping is structural rather than
                  cosmetic. <code>GET</code>, <code>POST</code>,{' '}
                  <code>PUT /host-groups/&#123;id&#125;</code> and{' '}
                  <code>DELETE /host-groups/&#123;id&#125;</code> are available,
                  and <code>PUT /hosts/&#123;id&#125;/group</code> moves a host
                  into one.
                </p>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`# Create a group
curl -X POST "${API_URL}/api/v1/organizations/acme/host-groups" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"name": "frontends", "display_name": "Front-end servers"}'

# Move a host into it
curl -X PUT "${API_URL}/api/v1/organizations/acme/hosts/42/group" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"host_group_id": 7}'`}
                  />
                </div>
              </div>

              {/* ── WEBHOOK DELIVERIES ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  Webhook deliveries
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Every webhook attempt is recorded with the status code, the
                  number of attempts and the (truncated) response body, and can
                  be re-sent. Debugging an integration without this means asking
                  for the alert to be reproduced.
                </p>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`# Recent deliveries of one channel (paginated, X-Total-Count)
curl "${API_URL}/api/v1/organizations/acme/notification-channels/3/deliveries" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY"

# Re-send delivery 128, re-signed with a fresh timestamp
curl -X POST "${API_URL}/api/v1/organizations/acme\\
/notification-channels/3/deliveries/128/replay" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY"`}
                  />
                </div>
              </div>

              {/* ── AGENT RELEASES ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Agent releases</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Public, unauthenticated endpoints, so a configuration-management
                  tool can answer "does this host already have the right
                  version?" without downloading the binary on every run.
                </p>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>curl</div>
                  <CodeBlock
                    language='bash'
                    code={`# Version on offer, checksums and pinnable URLs
curl "${API_URL}/api/v1/agents/latest"

# The digest of one binary, in the shasum format
curl "${API_URL}/api/v1/agents/download/linux/amd64/sha256"

# A pinned version. 404 when it is no longer the one served.
curl -O "${API_URL}/api/v1/agents/download/1.4.2/linux/amd64"`}
                  />
                </div>
                <p
                  style={{ fontSize: '0.875rem', color: 'var(--doc-muted)' }}>
                  The download carries an <code>ETag</code> (the sha256), a{' '}
                  <code>Last-Modified</code>, and the{' '}
                  <code>X-Agent-Version</code> / <code>X-Agent-SHA256</code>{' '}
                  headers, so a re-run over an up-to-date fleet gets{' '}
                  <code>304</code> answers instead of the binary.
                </p>
              </div>

              {/* ── HOSTS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Hosts</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  A <strong>Host</strong> is the entity representing a physical
                  machine, VM, or container. Agent metrics attach to a host, and
                  checks can optionally be attached to one. Deleting a host does{' '}
                  <strong>not</strong> delete its services: they are detached
                  (their <code>host_id</code> becomes <code>null</code>) and
                  keep running.
                </p>
                <div className='doc-callout'>
                  Don't confuse the host's <code>service</code> field (a plain
                  text label, e.g. <code>api</code>) with its{' '}
                  <code>services</code> array (the checks/agent metrics attached
                  to it) — different things that happen to share a name.{' '}
                  <code>service</code> is the auto-correlation key: when an
                  SDK-reported application error has the same{' '}
                  <code>service</code> value as a host, Middle Monitor links
                  them automatically, without you having to configure a{' '}
                  <a href='#correlation-links'>correlation link</a>.
                </div>

                <div className='doc-endpoint' id='get-hosts'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>/hosts</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns all hosts in the organization as a JSON array, each
                    with per-status service counts so you can build a fleet
                    dashboard in one request.
                  </p>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl "${API_URL}/api/v1/organizations/acme/hosts" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY"`}
                    />
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":             42,
    "organization_id": 1,
    "name":           "web-prod-01",
    "display_name":   "Production Web Server",
    "host":           "10.0.1.10",
    "service":        "api",
    "host_group_id":  3,
    "status":         "success",
    "created_at":     "2025-01-15T10:00:00Z",
    "service_count":  3,
    "healthy_count":  3,
    "warning_count":  0,
    "critical_count": 0,
    "failing_count":  0
  }
]`}
                    />
                  </div>
                  <p className='doc-endpoint-section-label'>Status values</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Value</th>
                        <th>Meaning</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>success</td>
                        <td>All services on the host are passing</td>
                      </tr>
                      <tr>
                        <td>warning</td>
                        <td>At least one service is in warning state</td>
                      </tr>
                      <tr>
                        <td>failure</td>
                        <td>At least one service is failing or critical</td>
                      </tr>
                      <tr>
                        <td>unknown</td>
                        <td>
                          No recent results (agent metrics older than 5 min,
                          checks older than 10 min, or host just created)
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>/hosts</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Creates a new host record. After creation, generate an
                    install token (via the UI or{' '}
                    <code>POST /install-tokens</code>) and use it to install the
                    agent on the machine. The <code>name</code> field must match
                    the hostname the agent will report — the agent reads it from
                    the OS at startup and uses it to identify itself.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Machine hostname as reported by the OS (e.g.{' '}
                          <code>web-prod-01</code>). Must be unique within the
                          organization.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>host</td>
                        <td>string</td>
                        <td>
                          Resolvable address or IP of the machine (e.g.{' '}
                          <code>10.0.1.10</code>). Used for display and as a
                          check target.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>display_name</td>
                        <td>string</td>
                        <td>
                          Human-readable label shown in the dashboard (e.g.
                          "Production Web Server"). Falls back to{' '}
                          <code>name</code> if omitted.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>service</td>
                        <td>string</td>
                        <td>
                          Logical application/stack label, matched against
                          application errors reported under the same value for
                          automatic correlation (see callout above). Defaults to{' '}
                          <code>name</code> if omitted.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/hosts \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":         "web-prod-01",
    "display_name": "Production Web Server",
    "host":         "10.0.1.10",
    "service":      "api"
  }'`}
                    />
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`{
  "id":           42,
  "organization_id": 1,
  "name":         "web-prod-01",
  "display_name": "Production Web Server",
  "host":         "10.0.1.10",
  "service":      "api",
  "created_at":   "2026-06-13T12:00:00Z"
}`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>
                      /hosts/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns the full details of a single host, including its
                    computed status and the attached services. Use this endpoint
                    to build a host detail view.
                  </p>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`{
  "id":            42,
  "name":          "web-prod-01",
  "display_name":  "Production Web Server",
  "host":          "10.0.1.10",
  "service":       "api",
  "status":        "success",
  "created_at":    "2025-01-15T10:00:00Z",
  "services": [
    {
      "id":     1337,
      "name":   "api-health",
      "type":   "http",
      "service_interval": 60
    }
  ]
}`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-put'>PUT</span>
                    <span className='doc-endpoint-path'>
                      /hosts/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Updates mutable fields on a host. You cannot change the{' '}
                    <code>name</code> after creation because the agent uses it
                    to self-identify — changing it would break the live agent
                    connection.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>display_name</td>
                        <td>string</td>
                        <td>
                          New human-readable label.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>host</td>
                        <td>string</td>
                        <td>
                          Updated IP address or FQDN.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-delete'>
                      DELETE
                    </span>
                    <span className='doc-endpoint-path'>
                      /hosts/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Permanently deletes the host record. Attached services are{' '}
                    <strong>detached, not deleted</strong> — they keep running
                    with <code>host_id: null</code>; delete them separately if
                    needed. If the agent is still installed on the machine, it
                    will re-create the host the next time it registers (e.g. on
                    restart) — stop and uninstall the agent first.
                  </p>
                </div>
              </div>

              {/* ── INSTALL TOKENS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Install Tokens</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  A separate credential type from hosts and from{' '}
                  <code>mm_</code> API keys — an <strong>install token</strong>{' '}
                  is what the agent install script uses to authenticate itself
                  and register the hosts it runs on. See{' '}
                  <em>Which token for what</em> in{' '}
                  <a href='#api-auth'>Authentication</a> for how it compares to
                  the other credential types.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>/install-tokens</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Creates a named install token for the organization. The
                    token is passed to the agent install script as{' '}
                    <code>MIDDLE_MONITOR_INSTALL_TOKEN</code> — the agent sends
                    it on every request so the backend can identify which
                    organization it belongs to. If you need to reinstall the
                    agent, generate a new token here.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Human-readable label for this token (e.g.
                          "prod-fleet").{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>expires_at</td>
                        <td>string</td>
                        <td>
                          ISO 8601 expiry datetime. If omitted, the token does
                          not expire.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/install-tokens \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"name": "prod-fleet"}'`}
                    />
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 201</div>
                    <CodeBlock
                      language='json'
                      code={`{
  "id":         7,
  "name":       "prod-fleet",
  "token":      "FULL_TOKEN_ONLY_SHOWN_ONCE",
  "created_at": "2026-06-13T12:00:00Z"
}`}
                    />
                  </div>
                  <div className='doc-callout'>
                    The full <code>token</code> is returned only on creation.
                    The list endpoint (<code>GET /install-tokens</code>) exposes
                    only a <code>token_prefix</code> for identification.{' '}
                    <code>GET</code> and{' '}
                    <code>DELETE /install-tokens/&#123;id&#125;</code> complete
                    the lifecycle.
                  </div>
                </div>
              </div>

              {/* ── SERVICES ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  Services &amp; Checks
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  A <strong>Service</strong> is a monitoring check, optionally
                  attached to a host. Five check types are supported:{' '}
                  <code>http</code> (HTTP/HTTPS endpoint reachability and
                  latency), <code>ping</code> (ICMP round-trip to an IP or
                  hostname), <code>sql</code> (connection check plus built-in
                  diagnostics — no user-provided query),{' '}
                  <code>certificate</code> (TLS certificate validity and
                  days-until-expiry), and <code>snmp</code> (SNMP v2c GET on a
                  single OID with optional numeric threshold). Checks are
                  executed by the{' '}
                  <strong>Middle Monitor backend workers</strong> — the server
                  polls each service endpoint according to its configured
                  interval. No agent is required on the target machine. Error
                  services (SDK-linked applications) are a separate service type
                  — see <strong>Errors &amp; Exceptions</strong> below for their
                  token.
                </p>

                <div className='doc-endpoint' id='get-services'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>/services</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns all services in the organization as a JSON array,
                    each with its recent results embedded so you can build
                    status dashboards without a second request per service.
                    Filter by logical <code>service</code> label or time range.
                  </p>
                  <p className='doc-endpoint-section-label'>Query parameters</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Parameter</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>service</td>
                        <td>string</td>
                        <td>
                          Return only checks with this logical service label.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>start_date</td>
                        <td>string</td>
                        <td>
                          ISO 8601 — include results from this datetime.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>end_date</td>
                        <td>string</td>
                        <td>
                          ISO 8601 — include results up to this datetime.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":                 1337,
    "organization_id":    1,
    "name":               "api-health",
    "type":               "http",
    "host":               "https://api.example.com/health",
    "host_id":            42,
    "service":            "api",
    "service_interval":   60,
    "warning_threshold":  500,
    "critical_threshold": 1500,
    "max_attempts":       3,
    "created_at":         "2025-02-01T09:00:00Z",
    "results": [
      { "status": "success", "latency": 42, "timestamp": "2026-06-13T11:59:00Z" }
    ]
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>/services</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Creates a new service check. The <code>host</code> field is
                    the target — its exact meaning depends on the check type: a
                    full URL for <code>http</code>, an IP or hostname for{' '}
                    <code>ping</code>, <code>certificate</code>,{' '}
                    <code>snmp</code>, and <code>sql</code> (connection details
                    like port/database/user go in <code>credentials</code>, not
                    a DSN string in <code>host</code>). Checks are executed by
                    the Middle Monitor backend workers. A failing check is
                    retried up to <code>max_attempts</code> times within the same
                    cycle (2s, 4s then 8s apart); only a failure that survives
                    every attempt is recorded and alerted on, which prevents
                    flapping on transient network blips. Unlike error
                    services (SDK-linked applications), a check created here
                    does <strong>not</strong> get a <code>token</code> in the
                    response — see <strong>Errors &amp; Exceptions</strong>{' '}
                    below for that.
                  </p>
                  <p className='doc-endpoint-section-label'>
                    Request body — common fields
                  </p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Display name for the service in dashboards and alerts.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>type</td>
                        <td>string</td>
                        <td>
                          <code>http</code> | <code>ping</code> |{' '}
                          <code>sql</code> | <code>certificate</code> |{' '}
                          <code>snmp</code> — determines how the check runs and
                          which extra fields apply (see tabs below).{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>host</td>
                        <td>string</td>
                        <td>
                          Target of the check — see the per-type meaning above.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>service</td>
                        <td>string</td>
                        <td>
                          Logical application/stack label used to group checks
                          (e.g. <code>api</code>) — not to be confused with the
                          fact that a check is itself called a "Service". Same
                          label as a host's <code>service</code> field; matching
                          values group the two together.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>host_id</td>
                        <td>integer</td>
                        <td>
                          Attach the check to a host so it appears on the host
                          page and scopes correlation.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>service_interval</td>
                        <td>integer</td>
                        <td>
                          Check frequency in seconds. Defaults to 60. Common
                          values: 60 (standard), 300 (low-priority), 3600
                          (hourly).{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>max_attempts</td>
                        <td>integer</td>
                        <td>
                          Number of attempts a failing check gets within one
                          cycle before the status changes and alerts fire.
                          Defaults to 3. Setting to 1 fires immediately on first
                          failure.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>

                  <p className='doc-endpoint-section-label'>
                    Request body &amp; response — by check type
                  </p>
                  <CheckTypeTabs />

                  {checkType === 'http' && (
                    <>
                      <table
                        className='doc-param-table'
                        style={{ marginBottom: '1rem' }}>
                        <thead>
                          <tr>
                            <th>Field</th>
                            <th>Type</th>
                            <th>Description</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr>
                            <td>warning_threshold</td>
                            <td>number</td>
                            <td>
                              Latency (ms) above which the check reports{' '}
                              <code>warning</code>. Defaults to{' '}
                              <code>1000</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>critical_threshold</td>
                            <td>number</td>
                            <td>
                              Latency (ms) above which the check reports{' '}
                              <code>critical</code>. Defaults to{' '}
                              <code>3000</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>expected_status_code</td>
                            <td>integer</td>
                            <td>
                              Status code that counts as success. Defaults to
                              any <code>2xx</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>expected_body_contains</td>
                            <td>string</td>
                            <td>
                              Body assertion. In the default{' '}
                              <code>contains</code> mode, a substring that must
                              appear in the response body — on a JSON body, a{' '}
                              <code>key=value</code> or <code>key:value</code>{' '}
                              expression also matches the key at any depth; in{' '}
                              <code>json_path</code> mode, a{' '}
                              <code>path=value</code> expression — the path may
                              resolve at the root or in any nested
                              object/array.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>expected_body_mode</td>
                            <td>string</td>
                            <td>
                              <code>contains</code> (default) |{' '}
                              <code>json_path</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>http_auth</td>
                            <td>object</td>
                            <td>
                              Upstream auth the worker sends when polling the
                              target — a separate field from{' '}
                              <code>credentials</code>. Either{' '}
                              <code>{`{"mode":"bearer","bearer_token":"..."}`}</code>{' '}
                              or{' '}
                              <code>{`{"mode":"basic","basic_user":"...","basic_password":"..."}`}</code>
                              . Secrets are never returned by the API.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>curl</div>
                        <CodeBlock
                          language='bash'
                          code={`curl -X POST ${API_URL}/api/v1/organizations/acme/services \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":                   "api-health",
    "type":                   "http",
    "host":                   "https://api.example.com/health",
    "service":                "api",
    "host_id":                42,
    "service_interval":       60,
    "warning_threshold":      1000,
    "critical_threshold":     3000,
    "max_attempts":           3,
    "expected_status_code":   200,
    "expected_body_contains": "ok",
    "http_auth": { "mode": "bearer", "bearer_token": "upstream_token" }
  }'`}
                        />
                      </div>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>Response 200</div>
                        <CodeBlock
                          language='json'
                          code={`{
  "id":                   1337,
  "organization_id":      1,
  "name":                 "api-health",
  "type":                 "http",
  "host":                 "https://api.example.com/health",
  "service":              "api",
  "host_id":              42,
  "service_interval":     60,
  "warning_threshold":    1000,
  "critical_threshold":   3000,
  "max_attempts":         3,
  "expected_status_code": 200,
  "http_auth_configured": true,
  "http_auth_mode":       "bearer",
  "created_at":           "2026-06-13T12:00:00Z"
}`}
                        />
                      </div>
                    </>
                  )}

                  {checkType === 'ping' && (
                    <>
                      <table
                        className='doc-param-table'
                        style={{ marginBottom: '1rem' }}>
                        <thead>
                          <tr>
                            <th>Field</th>
                            <th>Type</th>
                            <th>Description</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr>
                            <td>warning_threshold</td>
                            <td>number</td>
                            <td>
                              Round-trip latency (ms) above which the check
                              reports <code>warning</code>. Defaults to{' '}
                              <code>100</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>critical_threshold</td>
                            <td>number</td>
                            <td>
                              Round-trip latency (ms) above which the check
                              reports <code>critical</code>. Defaults to{' '}
                              <code>300</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>curl</div>
                        <CodeBlock
                          language='bash'
                          code={`curl -X POST ${API_URL}/api/v1/organizations/acme/services \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":               "db-prod-01-ping",
    "type":               "ping",
    "host":               "10.0.1.20",
    "service":            "database",
    "host_id":            43,
    "service_interval":   60,
    "warning_threshold":  100,
    "critical_threshold": 300,
    "max_attempts":       3
  }'`}
                        />
                      </div>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>Response 200</div>
                        <CodeBlock
                          language='json'
                          code={`{
  "id":                 1338,
  "organization_id":    1,
  "name":               "db-prod-01-ping",
  "type":               "ping",
  "host":               "10.0.1.20",
  "service":            "database",
  "host_id":            43,
  "service_interval":   60,
  "warning_threshold":  100,
  "critical_threshold": 300,
  "max_attempts":       3,
  "created_at":         "2026-06-13T12:00:00Z"
}`}
                        />
                      </div>
                    </>
                  )}

                  {checkType === 'sql' && (
                    <>
                      <table
                        className='doc-param-table'
                        style={{ marginBottom: '1rem' }}>
                        <thead>
                          <tr>
                            <th>Field</th>
                            <th>Type</th>
                            <th>Description</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr>
                            <td>critical_threshold</td>
                            <td>number</td>
                            <td>
                              Connection latency (ms) above which the check
                              reports <code>critical</code>, overriding the
                              internal diagnostics. Defaults to{' '}
                              <code>1000</code>. There is no{' '}
                              <code>warning_threshold</code> for{' '}
                              <code>sql</code> — warnings come only from the
                              built-in diagnostics.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>credentials</td>
                            <td>string (JSON)</td>
                            <td>
                              <code>engine</code> (<code>postgres</code>{' '}
                              default, or <code>mysql</code>/
                              <code>mariadb</code>), <code>port</code>,{' '}
                              <code>database</code>, <code>username</code>,{' '}
                              <code>password</code>. <code>host</code> can also
                              be overridden here; otherwise the top-level{' '}
                              <code>host</code> field is used. Stored encrypted;
                              never returned by the API.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>curl</div>
                        <CodeBlock
                          language='bash'
                          code={`curl -X POST ${API_URL}/api/v1/organizations/acme/services \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":               "orders-db",
    "type":               "sql",
    "host":               "10.0.2.5",
    "service":            "database",
    "host_id":            43,
    "service_interval":   60,
    "critical_threshold": 1000,
    "max_attempts":       3,
    "credentials":        "{\\"engine\\":\\"postgres\\",\\"port\\":\\"5432\\",\\"database\\":\\"orders\\",\\"username\\":\\"readonly\\",\\"password\\":\\"s3cr3t\\"}"
  }'`}
                        />
                      </div>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>Response 200</div>
                        <CodeBlock
                          language='json'
                          code={`{
  "id":                 1339,
  "organization_id":    1,
  "name":               "orders-db",
  "type":               "sql",
  "host":               "10.0.2.5",
  "service":            "database",
  "host_id":            43,
  "service_interval":   60,
  "critical_threshold": 1000,
  "max_attempts":       3,
  "created_at":         "2026-06-13T12:00:00Z"
}`}
                        />
                      </div>
                    </>
                  )}

                  {checkType === 'certificate' && (
                    <>
                      <table
                        className='doc-param-table'
                        style={{ marginBottom: '1rem' }}>
                        <thead>
                          <tr>
                            <th>Field</th>
                            <th>Type</th>
                            <th>Description</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr>
                            <td>warning_threshold</td>
                            <td>number</td>
                            <td>
                              Days before expiry at which the check reports{' '}
                              <code>warning</code>. Defaults to <code>30</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>critical_threshold</td>
                            <td>number</td>
                            <td>
                              Days before expiry at which the check reports{' '}
                              <code>critical</code>. Defaults to <code>7</code>.
                              Inverted vs. every other check type: fewer days
                              remaining is worse, so this must be{' '}
                              <strong>lower</strong> than{' '}
                              <code>warning_threshold</code>.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>curl</div>
                        <CodeBlock
                          language='bash'
                          code={`curl -X POST ${API_URL}/api/v1/organizations/acme/services \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":               "api-tls-cert",
    "type":               "certificate",
    "host":               "api.example.com",
    "service":            "api",
    "host_id":            42,
    "service_interval":   3600,
    "warning_threshold":  30,
    "critical_threshold": 7
  }'`}
                        />
                      </div>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>Response 200</div>
                        <CodeBlock
                          language='json'
                          code={`{
  "id":                 1340,
  "organization_id":    1,
  "name":               "api-tls-cert",
  "type":               "certificate",
  "host":               "api.example.com",
  "service":            "api",
  "host_id":            42,
  "service_interval":   3600,
  "warning_threshold":  30,
  "critical_threshold": 7,
  "created_at":         "2026-06-13T12:00:00Z"
}`}
                        />
                      </div>
                    </>
                  )}

                  {checkType === 'snmp' && (
                    <>
                      <table
                        className='doc-param-table'
                        style={{ marginBottom: '1rem' }}>
                        <thead>
                          <tr>
                            <th>Field</th>
                            <th>Type</th>
                            <th>Description</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr>
                            <td>warning_threshold / critical_threshold</td>
                            <td>number</td>
                            <td>
                              Raw OID value above which to warn/alert. No
                              default — an SNMP check left without a threshold
                              only verifies that the device responds.{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                          <tr>
                            <td>credentials</td>
                            <td>string (JSON)</td>
                            <td>
                              <code>community</code> (defaults to{' '}
                              <code>"public"</code>) and <code>oid</code>{' '}
                              (defaults to <code>sysUpTime</code>{' '}
                              <code>1.3.6.1.2.1.1.3.0</code>, which only
                              confirms reachability).{' '}
                              <span className='doc-param-optional'>
                                optional
                              </span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>curl</div>
                        <CodeBlock
                          language='bash'
                          code={`curl -X POST ${API_URL}/api/v1/organizations/acme/services \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":               "switch-01-uptime",
    "type":               "snmp",
    "host":               "10.0.0.1",
    "service":            "network",
    "host_id":            44,
    "service_interval":   300,
    "credentials":        "{\\"community\\":\\"private\\",\\"oid\\":\\"1.3.6.1.2.1.2.2.1.10.1\\"}",
    "warning_threshold":  8000000,
    "critical_threshold": 9500000
  }'`}
                        />
                      </div>
                      <div className='doc-code-block'>
                        <div className='doc-code-header'>Response 200</div>
                        <CodeBlock
                          language='json'
                          code={`{
  "id":                 1341,
  "organization_id":    1,
  "name":               "switch-01-uptime",
  "type":               "snmp",
  "host":               "10.0.0.1",
  "service":            "network",
  "host_id":            44,
  "service_interval":   300,
  "warning_threshold":  8000000,
  "critical_threshold": 9500000,
  "created_at":         "2026-06-13T12:00:00Z"
}`}
                        />
                      </div>
                    </>
                  )}

                  <p
                    className='doc-endpoint-desc'
                    style={{ marginTop: '0.75rem' }}>
                    <code>PUT /services/&#123;id&#125;</code> updates a check
                    and <code>DELETE /services/&#123;id&#125;</code> removes it
                    along with its results.
                  </p>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>
                      /services/&#123;id&#125;/results
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns the check result history for a service, most recent
                    first. Each result captures the latency, status, and error
                    message (if any) at the time the check ran. Use this to
                    build latency trend charts or to investigate a past
                    incident.
                  </p>
                  <p className='doc-endpoint-section-label'>Query parameters</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Parameter</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>start_date</td>
                        <td>string</td>
                        <td>
                          ISO 8601 start datetime (inclusive).{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>end_date</td>
                        <td>string</td>
                        <td>
                          ISO 8601 end datetime (inclusive). Defaults to now.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":         900001,
    "service_id": 1337,
    "status":     "success",
    "latency":    43,
    "timestamp":  "2026-06-13T11:59:00Z"
  },
  {
    "id":         900000,
    "service_id": 1337,
    "status":     "failure",
    "message":    "connection refused",
    "timestamp":  "2026-06-13T11:58:00Z"
  }
]`}
                    />
                  </div>
                  <p className='doc-endpoint-section-label'>
                    Result status values
                  </p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Value</th>
                        <th>Meaning</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>success</td>
                        <td>Check passed within thresholds</td>
                      </tr>
                      <tr>
                        <td>failure</td>
                        <td>
                          Check failed (unreachable, wrong status code,
                          assertion failed, threshold crossed)
                        </td>
                      </tr>
                      <tr>
                        <td>timeout</td>
                        <td>The target did not answer in time</td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-callout'>
                    <strong>Data retention:</strong> check results, application
                    errors, system metrics, and events are kept for{' '}
                    <strong>7 days</strong> (a daily cleanup job prunes older
                    data). Export what you need for longer-term analysis.
                  </div>
                </div>
              </div>

              {/* ── ERRORS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  Errors &amp; Exceptions
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Errors are ingested by the SDK and can be read either as a raw
                  event list or <strong>grouped</strong> by (name, message,
                  file) with occurrence counts and a sparkline timeseries — a
                  bug that fires 10,000 times appears as one group instead of
                  flooding the list. Ingestion happens on the dedicated endpoint{' '}
                  <code>POST /api/v1/errors</code> (no organization prefix); it
                  is called by the SDKs internally and authenticates with{' '}
                  <code>Authorization: Bearer &lt;service token&gt;</code>.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>/errors</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns application errors, most recent first. Pass{' '}
                    <code>grouped=1</code> to aggregate identical errors (same
                    name, message, and file) into groups with an event count and
                    a timeseries for sparklines — this is what the Errors page
                    uses.
                  </p>
                  <p className='doc-endpoint-section-label'>Query parameters</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Parameter</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>service</td>
                        <td>string</td>
                        <td>
                          Filter to a specific application (its{' '}
                          <code>service</code> name).{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>limit</td>
                        <td>integer</td>
                        <td>
                          Maximum number of rows/groups returned.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>grouped</td>
                        <td>string</td>
                        <td>
                          Set to <code>1</code> to return error groups instead
                          of raw events.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>window</td>
                        <td>string</td>
                        <td>
                          Grouped mode: bucket size for the timeseries.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>start / end</td>
                        <td>string</td>
                        <td>
                          Grouped mode: ISO 8601 time range.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>
                      Response 200 — grouped=1
                    </div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "event_count": 42,
    "first_seen":  "2026-06-10T08:00:00Z",
    "last_seen":   "2026-06-13T11:55:00Z",
    "sample_id":   50123,
    "sample": {
      "id":      50123,
      "name":    "runtime.panic",
      "message": "index out of range [5] with length 5",
      "file":    "handlers/orders.go",
      "line":    87,
      "service": "api-backend",
      "timestamp": "2026-06-13T11:55:00Z",
      "trace_id":  "4bf92f3577b34da6a3ce929d0e0e4736"
    },
    "timeseries": [
      { "date": "2026-06-12", "count": 18 },
      { "date": "2026-06-13", "count": 7 }
    ]
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>
                      /errors/&#123;id&#125;/correlation
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns the signals that happened around this error, scoped
                    by the app's correlation links and its host group: failing
                    checks, host metrics out of range, directly linked
                    applications (dependencies/dependents) erroring in the same
                    window, other apps in the same host group, plus a{' '}
                    <code>recurrence</code> block (new regression vs chronic
                    error) and an overall <code>confidence</code> score (0-1).
                    This powers the <strong>Context / Correlation</strong> panel
                    on the error detail page. Companion endpoint:{' '}
                    <code>POST /errors/&#123;id&#125;/explain</code> runs the
                    AI root-cause analysis on those signals (LLM-backed, with
                    instant deterministic answers for well-known failure
                    signatures) and returns a ranked, human-readable
                    explanation.{' '}
                    <code>GET /errors/services</code> lists the applications
                    that have reported errors.
                  </p>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>
                      /api/v1/errors — ingestion (no org prefix)
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    SDK ingestion endpoint, served by the ingestion API rather
                    than the organization-scoped routes. Called internally by
                    Middle Monitor SDKs when an error is captured. Authenticates
                    with{' '}
                    <code>Authorization: Bearer &lt;service token&gt;</code>{' '}
                    (the token from your error service; an org API key also
                    works). You only need to call this directly if you are
                    building a custom SDK or an unsupported language
                    integration.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Error type/name (e.g. <code>runtime.panic</code>,{' '}
                          <code>ValueError</code>).{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>message</td>
                        <td>string</td>
                        <td>
                          Error message or description.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>file</td>
                        <td>string</td>
                        <td>
                          Source file of the top stack frame.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>line</td>
                        <td>integer</td>
                        <td>
                          Line number of the top stack frame.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>service</td>
                        <td>string</td>
                        <td>
                          Application name the error belongs to.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>timestamp</td>
                        <td>string</td>
                        <td>
                          ISO 8601 timestamp of when the error occurred.
                          Defaults to now.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>
                          http_method / http_url / http_headers / http_body
                        </td>
                        <td>string</td>
                        <td>
                          Optional HTTP request context, attached by the SDK
                          middlewares.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>trace_id</td>
                        <td>string</td>
                        <td>
                          OpenTelemetry trace ID linking the error to a
                          distributed trace — enables the root-cause engine to
                          pull the failing span chain.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>

              {/* ── METRIC SERIES ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Metric series</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Read the labelled metrics behind the{' '}
                  <a href='#custom-metrics'>custom metrics</a> explorer.{' '}
                  <code>GET /metrics/series/names</code>,{' '}
                  <code>/metrics/series/label-keys?metric=</code> and{' '}
                  <code>/metrics/series/label-values?metric=&amp;key=</code>{' '}
                  list what exists in a time range. Every series route takes{' '}
                  <code>start</code> and <code>end</code> (RFC 3339, default
                  the last hour) and an optional <code>host_id</code> or{' '}
                  <code>host_group_id</code> scope.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>
                      /metrics/series/query
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    One metric, aggregated per time bucket, optionally split by
                    a label.
                  </p>
                  <p className='doc-endpoint-section-label'>Query parameters</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Parameter</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>metric</td>
                        <td>string</td>
                        <td>
                          Metric name.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>filter</td>
                        <td>string</td>
                        <td>
                          A label constraint as <code>key:value</code>; repeat
                          the parameter for several.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>group_by</td>
                        <td>string</td>
                        <td>
                          Label key to split on, at most 50 series.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>aggregation</td>
                        <td>string</td>
                        <td>
                          avg (default), min, max, sum, count, p50, p75, p90,
                          p95 or p99.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>step</td>
                        <td>integer</td>
                        <td>
                          Bucket width in seconds. Defaults to the range split
                          in 120 buckets, and never yields more than 1000.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>
                      /metrics/series/expression
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Runs up to five queries on one shared step, so their buckets
                    line up, and combines them with an expression. It is a POST
                    only because the queries do not fit a URL: it reads, so a{' '}
                    <code>read_only</code> key may call it and an{' '}
                    <code>Idempotency-Key</code> is ignored. <code>start</code>,{' '}
                    <code>end</code>, <code>step</code>, <code>host_id</code>{' '}
                    and <code>host_group_id</code> stay query parameters, as on
                    the GET above.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>queries</td>
                        <td>array</td>
                        <td>
                          1 to 5 objects with <code>ref</code> (a distinct
                          uppercase letter), <code>metric</code>,{' '}
                          <code>filters</code> (array of{' '}
                          <code>&#123;key, value&#125;</code>),{' '}
                          <code>group_by</code> and <code>aggregation</code>{' '}
                          (the GET values plus <code>rate</code>; avg when
                          omitted).{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>expression</td>
                        <td>string</td>
                        <td>
                          Up to 256 characters: <code>$A</code>,{' '}
                          <code>+ - * /</code>, parentheses, numbers,{' '}
                          <code>abs()</code> and <code>sum()</code>. Omitted, the
                          answer holds every query&apos;s series, each tagged
                          with its <code>query</code> ref.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST "${API_URL}/api/v1/organizations/acme/metrics/series/expression?step=60" \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "queries": [
      { "ref": "A", "metric": "http_errors_total",   "group_by": "route", "aggregation": "rate" },
      { "ref": "B", "metric": "http_requests_total", "group_by": "route", "aggregation": "rate" }
    ],
    "expression": "$A / $B * 100"
  }'`}
                    />
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`{
  "series": [
    {
      "metric_name": "$A / $B * 100",
      "labels": { "route": "/checkout" },
      "points": [
        { "timestamp": "2026-10-01T12:00:00Z", "value": 1.8 },
        { "timestamp": "2026-10-01T12:01:00Z", "value": null }
      ]
    }
  ]
}`}
                    />
                  </div>
                  <p className='doc-endpoint-desc'>
                    A <code>null</code> value is a gap: no data on one side, or a
                    division by zero. A malformed query answers <code>400</code>{' '}
                    naming the mistake, in French when the request carries{' '}
                    <code>Accept-Language: fr</code>, in English otherwise.
                    Asking <code>rate</code> for more series than the step allows
                    is a <code>400</code> as well, never a truncated result.
                  </p>
                </div>
              </div>

              {/* ── ALERT RULES ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Alert Rules</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Alert rules define the conditions under which Middle Monitor
                  creates an incident and notifies your team. A rule targets a{' '}
                  <strong>service</strong>, a <strong>host</strong>, or{' '}
                  <strong>any</strong> target, applies a statistic (
                  <code>aggregation</code>) over a look-back window (
                  <code>duration</code> seconds), and compares it to
                  warning/critical thresholds — e.g.{' '}
                  <em>p95 latency over 5 minutes &gt; 1500 ms</em>. A recovery
                  threshold adds hysteresis so flapping values do not re-fire.
                  Routing flags decide which severities notify which channels.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>/alert-rules</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Lists all alert rules in the organization. Each rule
                    includes its <code>enabled</code> flag — rules can be
                    temporarily disabled without being deleted via{' '}
                    <code>PATCH /alert-rules/&#123;id&#125;/toggle</code>.
                  </p>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":                 5,
    "name":               "API latency critical",
    "type":               "threshold",
    "target_type":        "service",
    "target_id":          1337,
    "metric":             "latency",
    "operator":           "gt",
    "aggregation":        "p95",
    "duration":           300,
    "warning_threshold":  800,
    "critical_threshold": 1500,
    "recovery_threshold": 600,
    "enabled":            true,
    "channels":           [2, 3],
    "notify_warning":     false,
    "notify_critical":    true,
    "tags":               "team-backend,api",
    "created_at":         "2025-03-01T00:00:00Z"
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>/alert-rules</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Creates a new alert rule (returns <code>201</code>). The
                    rule begins evaluating immediately. CPU, RAM, and disk
                    metrics come from the infrastructure agent and are evaluated
                    on hosts; <code>latency</code>, <code>error_count</code>,
                    and <code>failure_rate</code> come from checks and errors.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Descriptive name shown in incidents and notifications.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>target_type</td>
                        <td>string</td>
                        <td>
                          <code>service</code> | <code>host</code> |{' '}
                          <code>any</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>target_id</td>
                        <td>integer</td>
                        <td>
                          ID of the targeted service or host. Omit with{' '}
                          <code>target_type: "any"</code>.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>metric</td>
                        <td>string</td>
                        <td>
                          <code>cpu</code> | <code>ram</code> |{' '}
                          <code>disk</code> (%) | <code>latency</code> (ms) |{' '}
                          <code>error_count</code> | <code>failure_rate</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>operator</td>
                        <td>string</td>
                        <td>
                          <code>gt</code> | <code>lt</code> | <code>gte</code> |{' '}
                          <code>lte</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>aggregation</td>
                        <td>string</td>
                        <td>
                          Statistic over the window: <code>avg</code> |{' '}
                          <code>min</code> | <code>max</code> | <code>sum</code>{' '}
                          | <code>p50</code> | <code>p75</code> |{' '}
                          <code>p90</code> | <code>p95</code> | <code>p99</code>
                          . <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>duration</td>
                        <td>integer</td>
                        <td>
                          Look-back window in seconds. Defaults to 60.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>warning_threshold</td>
                        <td>number</td>
                        <td>
                          Value that fires a warning-severity incident.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>critical_threshold</td>
                        <td>number</td>
                        <td>
                          Value that fires a critical-severity incident.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>recovery_threshold</td>
                        <td>number</td>
                        <td>
                          Hysteresis: the value must cross back past this before
                          the incident auto-resolves.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>channels</td>
                        <td>integer[]</td>
                        <td>
                          Notification channel IDs to notify when the rule
                          fires. If empty, an incident is created but nothing is
                          notified.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>notify_warning / notify_critical</td>
                        <td>boolean</td>
                        <td>
                          Route warning / critical severities to the channels.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>tags</td>
                        <td>string</td>
                        <td>
                          Comma-separated tags, forwarded to JSM alerts.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>enabled</td>
                        <td>boolean</td>
                        <td>
                          Whether the rule evaluates. Defaults to{' '}
                          <code>true</code>.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/alert-rules \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":               "API latency critical",
    "target_type":        "service",
    "target_id":          1337,
    "metric":             "latency",
    "operator":           "gt",
    "aggregation":        "p95",
    "duration":           300,
    "critical_threshold": 1500,
    "recovery_threshold": 600,
    "channels":           [2, 3],
    "notify_critical":    true
  }'`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-put'>PUT</span>
                    <span className='doc-endpoint-path'>
                      /alert-rules/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Updates an existing alert rule. To temporarily silence a
                    rule before a deployment, prefer{' '}
                    <code>PATCH /alert-rules/&#123;id&#125;/toggle</code> with{' '}
                    <code>&#123;"enabled": false&#125;</code> — or better,
                    schedule a <strong>maintenance window</strong> (below) so
                    history records why alerts were muted. Changing thresholds
                    does not retroactively re-evaluate past data.
                  </p>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-delete'>
                      DELETE
                    </span>
                    <span className='doc-endpoint-path'>
                      /alert-rules/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Permanently deletes the alert rule. Existing open incidents
                    linked to this rule remain open — deleting the rule does not
                    auto-resolve them.
                  </p>
                </div>
              </div>

              {/* ── INCIDENTS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>Incidents</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Incidents are created automatically when an alert rule or
                  check threshold fires, or manually via the API. The lifecycle
                  is <code>open</code> → <code>acknowledged</code> →{' '}
                  <code>resolved</code>, and a resolved incident can be{' '}
                  <strong>reopened</strong> by setting its status back to{' '}
                  <code>open</code>. Resolving closes the linked JSM/Opsgenie
                  alert if a JSM channel is configured, and notifies the other
                  channels that received the original alert. Incidents also
                  auto-resolve when the underlying check recovers.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>/incidents</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns the 100 most recent incidents, newest first. Filter
                    by <code>status</code> to build an on-call dashboard.
                  </p>
                  <p className='doc-endpoint-section-label'>Query parameters</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Parameter</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>status</td>
                        <td>string</td>
                        <td>
                          <code>open</code> | <code>acknowledged</code> |{' '}
                          <code>resolved</code>.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":            801,
    "title":         "API latency critical on api-health",
    "status":        "open",
    "severity":      "critical",
    "alert_rule_id": 5,
    "service_id":    1337,
    "host_id":       42,
    "service":       "api",
    "started_at":    "2026-06-13T11:55:00Z"
  },
  {
    "id":              800,
    "title":           "Disk usage warning on db-prod-01",
    "status":          "resolved",
    "severity":        "warning",
    "started_at":      "2026-06-12T09:10:00Z",
    "resolved_at":     "2026-06-12T10:02:00Z",
    "resolution_note": "Rotated logs and extended the volume to 200 GB.",
    "acknowledged_at": "2026-06-12T09:15:00Z",
    "acknowledged_by": 12
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>/incidents</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Manually declares an incident outside of the automatic
                    alerting flow (returns <code>201</code>). Useful for
                    announcing a known outage or tracking an issue that no rule
                    caught. Manual incidents follow the same lifecycle as
                    automatic ones.
                  </p>
                  <div className='doc-callout'>
                    An incident must be linked to something you monitor: the
                    API rejects the request with <code>400</code> unless at
                    least one of <code>service_id</code> or{' '}
                    <code>host_id</code> is set. They are real foreign keys and
                    make the incident clickable through to that resource's
                    page in the UI — pick whichever is the actual source of
                    the incident: a check, a <strong>host</strong>, or an{' '}
                    <strong>app</strong>. Look up the ID first —{' '}
                    <a href='#get-hosts'>
                      <code>GET /hosts</code>
                    </a>{' '}
                    for <code>host_id</code>,{' '}
                    <a href='#get-services'>
                      <code>GET /services</code>
                    </a>{' '}
                    for <code>service_id</code> (a check or, for an app, its{' '}
                    <em>error service</em> — same <code>services</code> table,{' '}
                    <code>type</code> starting with{' '}
                    <code>error_service_</code>, also visible in{' '}
                    <strong>Applications (APM) → Errors</strong>).{' '}
                    <code>service</code> is a separate, optional free-text
                    label (not validated against anything — same convention as
                    elsewhere in the API) for grouping/filtering; it does not
                    satisfy the linking requirement on its own.
                  </div>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>title</td>
                        <td>string</td>
                        <td>
                          Short description of the incident.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>severity</td>
                        <td>string</td>
                        <td>
                          <code>critical</code> | <code>warning</code> |{' '}
                          <code>info</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>description</td>
                        <td>string</td>
                        <td>
                          Longer description or initial investigation notes.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>service</td>
                        <td>string</td>
                        <td>
                          Affected application/service label. Cosmetic only —
                          does not link the incident by itself.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                      <tr>
                        <td>service_id / host_id</td>
                        <td>integer</td>
                        <td>
                          Link the incident to a specific check or host for
                          deep-linking in the UI.{' '}
                          <span className='doc-param-required'>
                            at least one required
                          </span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl — linked to a host</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/incidents \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "title":    "Planned failover — db-prod-01",
    "severity": "warning",
    "host_id":  42
  }'`}
                    />
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>
                      curl — linked to an app (error service)
                    </div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/incidents \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "title":      "Elevated error rate — checkout-api",
    "severity":   "critical",
    "service_id": 91
  }'`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-put'>PUT</span>
                    <span className='doc-endpoint-path'>
                      /incidents/&#123;id&#125;/status
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Transitions the incident status. Acknowledge an incident
                    while you investigate; resolve it to close the loop — this
                    sets <code>resolved_at</code>, optionally stores a{' '}
                    <code>resolution_note</code>, and closes the linked JSM
                    alert. Set the status back to <code>open</code> to reopen a
                    resolved incident.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>status</td>
                        <td>string</td>
                        <td>
                          <code>open</code> | <code>acknowledged</code> |{' '}
                          <code>resolved</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>resolution_note</td>
                        <td>string</td>
                        <td>
                          Post-mortem or resolution summary, stored on the
                          incident and shown on the Incidents page. Only used
                          when resolving.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>
                      curl — resolve with a note
                    </div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X PUT ${API_URL}/api/v1/organizations/acme/incidents/801/status \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "status":          "resolved",
    "resolution_note": "Root cause: memory leak in connection pool. Fixed in v2.4.1, deployed 12:10 UTC."
  }'`}
                    />
                  </div>
                </div>
              </div>

              {/* ── CHANNELS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  Notification Channels
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  Notification channels define where alerts are sent. Each
                  channel has a <code>type</code> and a <code>config</code>{' '}
                  object whose fields depend on the type. Channels can be
                  enabled or disabled without deleting them — useful for
                  temporarily muting a channel during weekends or planned
                  maintenance without losing the configuration.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>
                      /notification-channels
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns all configured channels. Note that{' '}
                    <code>config</code> is returned as stored — treat channel
                    configuration (webhook URLs, API keys) as sensitive and
                    restrict who holds <code>read</code> access to your
                    organization.
                  </p>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":      2,
    "name":    "Slack #incidents",
    "type":    "slack",
    "enabled": true,
    "config":  { "webhook_url": "https://hooks.slack.com/services/T00/B00/XXXX" },
    "created_at": "2025-01-20T00:00:00Z"
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>
                      /notification-channels
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Creates a notification channel. The <code>config</code>{' '}
                    object structure varies by type. See the config reference
                    below. After creation, test the channel via
                    <code>
                      {' '}
                      POST /notification-channels/&#123;id&#125;/test
                    </code>{' '}
                    to verify credentials before attaching it to alert rules.
                  </p>
                  <p className='doc-endpoint-section-label'>Common fields</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Display name (e.g. "Slack #on-call").{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>type</td>
                        <td>string</td>
                        <td>
                          <code>email</code> | <code>slack</code> |{' '}
                          <code>jsm</code> | <code>whatsapp</code> |{' '}
                          <code>webhook</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>config</td>
                        <td>object</td>
                        <td>
                          Type-specific configuration. See below.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>enabled</td>
                        <td>boolean</td>
                        <td>
                          Whether the channel is active. Defaults to{' '}
                          <code>true</code>.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <p className='doc-endpoint-section-label'>Config — email</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>emails</td>
                        <td>string</td>
                        <td>
                          Comma-separated list of recipient addresses (e.g.{' '}
                          <code>"oncall@acme.com, devs@acme.com"</code>).
                          Delivery goes through your organization's SMTP
                          settings (<em>Settings → Email / SMTP</em>) — without
                          them, email alerts are not sent.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <p className='doc-endpoint-section-label'>Config — slack</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>webhook_url</td>
                        <td>string</td>
                        <td>
                          Slack Incoming Webhook URL (
                          <code>https://hooks.slack.com/services/…</code>).
                          Create one in your Slack workspace under{' '}
                          <em>Apps → Incoming Webhooks</em>; the destination
                          channel is configured on the Slack side.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <p className='doc-endpoint-section-label'>
                    Config — jsm (Jira Service Management / Opsgenie)
                  </p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>api_key</td>
                        <td>string</td>
                        <td>
                          JSM/Opsgenie API integration key. Generate it from{' '}
                          <em>Operations → Settings → Integrations → API</em> in
                          JSM. Alerts are created with a stable alias and
                          auto-closed on resolution.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <p className='doc-endpoint-section-label'>
                    Config — whatsapp
                  </p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>phone_number</td>
                        <td>string</td>
                        <td>
                          Destination phone number in E.164 format (e.g.{' '}
                          <code>+33612345678</code>).{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>phone_number_id</td>
                        <td>string</td>
                        <td>
                          Meta WhatsApp Business <strong>sender</strong> phone
                          number ID (numeric string from{' '}
                          <em>Meta Business → WhatsApp → API Setup</em>).{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>token</td>
                        <td>string</td>
                        <td>
                          Permanent access token for the WhatsApp Business app.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <p className='doc-endpoint-section-label'>
                    Config — webhook (generic)
                  </p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>webhook_url</td>
                        <td>string</td>
                        <td>
                          Target URL that receives a <code>POST</code> request
                          on each alert. Must be reachable from the Middle
                          Monitor backend.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>secret</td>
                        <td>string</td>
                        <td>
                          If set, Middle Monitor signs the raw JSON payload with
                          HMAC-SHA256 and sends the signature as{' '}
                          <code>
                            X-Middmonitor-Signature: sha256=&lt;hex&gt;
                          </code>{' '}
                          so you can verify authenticity.{' '}
                          <span className='doc-param-optional'>optional</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-callout'>
                    The webhook payload is Slack-compatible JSON:{' '}
                    <code>text</code> (alert title) and <code>attachments</code>{' '}
                    — one attachment with <code>color</code> (red for critical,
                    amber for warning, green for <code>[RESOLVED]</code>),{' '}
                    <code>text</code> (alert body), <code>footer</code>, and{' '}
                    <code>ts</code>. Discord (via <code>/slack</code> suffix),
                    Mattermost, and most generic HTTP receivers consume it
                    directly.
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>
                      curl — create a Slack channel
                    </div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/notification-channels \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":    "Slack #on-call",
    "type":    "slack",
    "enabled": true,
    "config":  {
      "webhook_url": "https://hooks.slack.com/services/T00/B00/XXXX"
    }
  }'`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>
                      /notification-channels/&#123;id&#125;/test
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Sends a test notification through the channel using the
                    stored configuration — including channels that are currently
                    disabled, so you can verify credentials before enabling. Use
                    it right after creating or updating a channel, before
                    linking it to live alert rules. Returns <code>200</code>{' '}
                    with <code>&#123;"delivered": true&#125;</code> on success,
                    or <code>422</code> with an <code>error</code> message
                    describing the delivery failure. Also available from the UI
                    via the send icon on each channel card.
                  </p>
                </div>
              </div>

              {/* ── MAINTENANCE ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  Maintenance Windows
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  A maintenance window suppresses alerts for one target — a{' '}
                  <strong>service</strong> or a whole <strong>host</strong> —
                  during a defined time range. Check results are still recorded
                  and metrics continue to be collected — only notifications and
                  incident creation are suppressed. This allows you to deploy
                  updates or run migrations without generating false alerts,
                  while preserving the historical data for post-maintenance
                  review. To cover several services, create one window per
                  target.
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>
                      /maintenance-windows
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Returns all maintenance windows (past, active, and
                    scheduled). Whether a window is active is derived from{' '}
                    <code>starts_at</code> / <code>ends_at</code> versus now.
                  </p>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":          31,
    "name":        "v2.4.1 deployment",
    "target_type": "service",
    "target_id":   1337,
    "target_name": "api-health",
    "starts_at":   "2026-06-13T12:00:00Z",
    "ends_at":     "2026-06-13T13:00:00Z",
    "created_by":  12,
    "created_at":  "2026-06-13T11:00:00Z"
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>
                      /maintenance-windows
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Schedules a maintenance window for a service or a host. If{' '}
                    <code>starts_at</code> is in the past, suppression is
                    already active. <code>ends_at</code> must be after{' '}
                    <code>starts_at</code>.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Short description of the maintenance (e.g. "Database
                          migration").{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>target_type</td>
                        <td>string</td>
                        <td>
                          <code>service</code> | <code>host</code>. Targeting a
                          host suppresses alerts for everything on it.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>target_id</td>
                        <td>integer</td>
                        <td>
                          ID of the targeted service or host.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>starts_at</td>
                        <td>string</td>
                        <td>
                          ISO 8601 datetime when suppression starts.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>ends_at</td>
                        <td>string</td>
                        <td>
                          ISO 8601 datetime when suppression ends. Must be after{' '}
                          <code>starts_at</code>.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/maintenance-windows \\
  -H "Authorization: Bearer mm_YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":        "v2.4.1 deployment",
    "target_type": "service",
    "target_id":   1337,
    "starts_at":   "2026-06-13T12:00:00Z",
    "ends_at":     "2026-06-13T13:00:00Z"
  }'`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-delete'>
                      DELETE
                    </span>
                    <span className='doc-endpoint-path'>
                      /maintenance-windows/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Deletes a scheduled or active maintenance window. Alert
                    suppression stops immediately — any subsequent check
                    failures will trigger alerts normally.
                  </p>
                </div>
              </div>

              {/* ── API KEYS ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>API Keys</div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1.25rem',
                  }}>
                  API keys are long-lived credentials scoped to your
                  organization. They are the preferred authentication method for
                  automation (CI/CD, Terraform, SDK config). Permissions are not
                  configurable per key: an organization key always has read and
                  write access to the organization's API, and admin-only routes
                  (users, billing) are never reachable with one. The full key
                  value is returned only once at creation — after that, only a
                  short <code>key_prefix</code> is visible in the API and UI.
                  Store the key in a secret manager (e.g. HashiCorp Vault, AWS
                  Secrets Manager) immediately after creation. For credentials
                  tied to <em>you</em> rather than the organization, personal
                  API tokens are also available under{' '}
                  <code>/api/v1/auth/api-tokens</code> (managed from{' '}
                  <strong>Account</strong>).
                </p>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-get'>GET</span>
                    <span className='doc-endpoint-path'>/api-keys</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Lists all API keys in the organization. The raw key is never
                    returned — only <code>key_prefix</code> identifies it. Use
                    this to audit what keys exist, when they were last used, and
                    when they expire.
                  </p>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 200</div>
                    <CodeBlock
                      language='json'
                      code={`[
  {
    "id":           9,
    "name":         "terraform-prod",
    "key_prefix":   "mm_a1b2c3",
    "expires_at":   "2027-01-01T00:00:00Z",
    "last_used_at": "2026-06-13T10:00:00Z",
    "created_at":   "2026-01-01T00:00:00Z"
  }
]`}
                    />
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-post'>
                      POST
                    </span>
                    <span className='doc-endpoint-path'>/api-keys</span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Creates a new API key. The response includes the full key
                    value in the <code>key</code> field — this is the only time
                    it is ever returned in plaintext. Copy it immediately. After
                    this response, the key can only be identified by its{' '}
                    <code>id</code> and the masked suffix shown in list
                    responses.
                  </p>
                  <p className='doc-endpoint-section-label'>Request body</p>
                  <table className='doc-param-table'>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Type</th>
                        <th>Description</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>name</td>
                        <td>string</td>
                        <td>
                          Label for the key — use something descriptive like
                          "terraform-prod" or "github-actions".{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                      <tr>
                        <td>expires_at</td>
                        <td>string</td>
                        <td>
                          ISO 8601 expiry date, in the future. Mandatory —
                          perpetual keys are not allowed; the key is
                          automatically rejected after this date.{' '}
                          <span className='doc-param-required'>required</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>curl</div>
                    <CodeBlock
                      language='bash'
                      code={`curl -X POST ${API_URL}/api/v1/organizations/acme/api-keys \\
  -H "Authorization: Bearer mm_YOUR_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":       "terraform-prod",
    "expires_at": "2027-01-01T00:00:00Z"
  }'`}
                    />
                  </div>
                  <div className='doc-code-block'>
                    <div className='doc-code-header'>Response 201</div>
                    <CodeBlock
                      language='json'
                      code={`{
  "id":         9,
  "name":       "terraform-prod",
  "key":        "mm_FULL_KEY_ONLY_SHOWN_ONCE",
  "key_prefix": "mm_a1b2c3",
  "expires_at": "2027-01-01T00:00:00Z",
  "created_at": "2026-06-13T12:00:00Z"
}`}
                    />
                  </div>
                  <div className='doc-callout'>
                    Copy the <code>key</code> value now. It will not be shown
                    again. If you lose it, delete this key and create a new one.
                  </div>
                </div>

                <div className='doc-endpoint'>
                  <div className='doc-endpoint-header'>
                    <span className='doc-method-badge doc-method-delete'>
                      DELETE
                    </span>
                    <span className='doc-endpoint-path'>
                      /api-keys/&#123;id&#125;
                    </span>
                  </div>
                  <p className='doc-endpoint-desc'>
                    Immediately revokes and deletes the API key. Any subsequent
                    request using this key receives a <code>401</code> response.
                    Use this to rotate compromised credentials. This action
                    cannot be undone.
                  </p>
                </div>
              </div>
            </div>
          </section>

          <hr className='doc-divider' />

          {/* TERRAFORM */}
          <section id='terraform' className='doc-section'>
            <h2>
              Terraform Provider
              <ShareAnchor />
            </h2>
            <p>
              The <code>middmonitor</code> Terraform provider lets you manage
              your hosts, checks and your whole alerting configuration as
              Infrastructure-as-Code, in version-controlled HCL. Changes are
              reviewed in pull requests, applied with{' '}
              <code>terraform apply</code>, and rolled back with{' '}
              <code>terraform destroy</code> or state manipulation. Alert rules,
              notification channels and maintenance windows are resources like
              the rest — nothing has to be configured from the dashboard.
            </p>
            <p>
              The provider authenticates with <code>access_token</code>, which
              accepts either an organization API key (<code>mm_…</code>, created
              in <strong>Settings → API Keys</strong>) or a JWT access token
              from <code>POST /api/v1/auth/login</code>. Use the API key outside
              of an interactive session: a JWT expires after 24 hours, which is
              shorter than the life of a CI pipeline. The provider operates
              against a single organization identified by <code>org_slug</code>.
            </p>
            <div className='doc-callout'>
              The generated argument reference for every resource is published
              on the{' '}
              <a
                href='https://registry.terraform.io/providers/middle-monitor/middmonitor/latest/docs'
                target='_blank'
                rel='noopener noreferrer'>
                Terraform Registry
              </a>
              , produced from the provider schema itself. This page is the
              narrative version; the registry is authoritative on argument names
              and types.
            </div>
            <div id='terraform-setup' className='doc-subsection'>
              <h3>
                Provider Setup
                <ShareAnchor />
              </h3>
              <p>
                Declare the provider in your <code>versions.tf</code>, then
                configure it. All three provider arguments can alternatively be
                supplied via environment variables — this is the recommended
                approach so credentials are never stored in your HCL files or
                state.
              </p>
              <p>
                Source code:{' '}
                <a
                  href={`${GITHUB_ORG}/terraform-provider-middmonitor`}
                  target='_blank'
                  rel='noopener noreferrer'>
                  middle-monitor/terraform-provider-middmonitor
                </a>
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>versions.tf</div>
                <CodeBlock
                  language='hcl'
                  code={`terraform {
  required_version = ">= 1.5"
  required_providers {
    middmonitor = {
      source  = "registry.terraform.io/middle-monitor/middmonitor"
      version = "~> 0.2"
    }
  }
}`}
                />
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>provider.tf</div>
                <CodeBlock
                  language='hcl'
                  code={`provider "middmonitor" {
  # base_url: dashboard API root, no trailing slash.
  # Env var: MIDDLE_MONITOR_BASE_URL
  base_url = "https://api.middlemonitor.io"

  # access_token: organization API key (mm_...) or a JWT from /auth/login.
  # Env var: MIDDLE_MONITOR_ACCESS_TOKEN
  access_token = var.middmonitor_token

  # org_slug: your organization slug, visible in the dashboard URL.
  # Env var: MIDDLE_MONITOR_ORG_SLUG
  org_slug = "acme"

  # receiver_base_url: only when the receiver is served on its own hostname.
  # Defaults to base_url. Used by the middmonitor_agent_install data source.
  # Env var: MIDDLE_MONITOR_RECEIVER_BASE_URL
  # receiver_base_url = "https://api.middlemonitor.io"
}`}
                />
              </div>
              <div className='doc-code-block'>
                <div className='doc-code-header'>
                  variables.tf (keep the token out of state)
                </div>
                <CodeBlock
                  language='hcl'
                  code={`variable "middmonitor_token" {
  description = "Middle Monitor organization API key (mm_...)"
  type        = string
  sensitive   = true
}`}
                />
              </div>
              <div className='doc-callout'>
                Pass the token at apply time with{' '}
                <code>TF_VAR_middmonitor_token=mm_...</code> or store it in a
                secret manager backend. Never commit it to source control or let
                it appear in
                <code> terraform.tfvars</code> files that are not gitignored.
              </div>
              <div className='doc-callout'>
                The environment variables are prefixed{' '}
                <code>MIDDLE_MONITOR_</code>, not <code>MIDDMONITOR_</code>. A
                misspelled prefix is not an error the provider can report — it
                simply finds no token and fails on the missing argument, which
                sends you looking at the credential rather than at its name.
              </div>

              <p style={{ marginTop: '1.5rem' }}>
                Run <code>terraform init</code> to download the provider, then{' '}
                <code>terraform plan</code> to preview changes before{' '}
                <code>terraform apply</code>. On first run against an existing
                organization, use <code>terraform import</code> to bring
                existing resources under Terraform management without recreating
                them.
              </p>
              <div className='doc-code-block'>
                <div className='doc-code-header'>Import an existing host</div>
                <CodeBlock
                  language='bash'
                  code={`# Import a host that already exists in Middle Monitor into Terraform state (numeric ID)
terraform import middmonitor_host.web 42

# Import a service
terraform import middmonitor_service.api_check 1337`}
                />
              </div>
            </div>

            <div id='terraform-resources' className='doc-subsection'>
              <h3>
                Resources &amp; Data Sources
                <ShareAnchor />
              </h3>
              <p>Overview of all available resources and data sources:</p>
              <table className='doc-api-table' style={{ marginBottom: '2rem' }}>
                <thead>
                  <tr>
                    <th>Type</th>
                    <th>Name</th>
                    <th>Description</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_host</td>
                    <td>Manages a host (machine) and its metadata</td>
                  </tr>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_service</td>
                    <td>Manages a monitoring check attached to a host</td>
                  </tr>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_install_token</td>
                    <td>
                      Generates an agent install token for the organization
                    </td>
                  </tr>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_host_group</td>
                    <td>Manages a host group, which scopes alert correlation</td>
                  </tr>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_alert_rule</td>
                    <td>
                      Manages a threshold rule, including the custom_metric form
                      that watches a scraped series
                    </td>
                  </tr>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_notification_channel</td>
                    <td>
                      Manages an alert destination, webhook payload format and
                      grouping included
                    </td>
                  </tr>
                  <tr>
                    <td>resource</td>
                    <td>middmonitor_maintenance_window</td>
                    <td>
                      Manages an alert suppression window. The API has no update,
                      so a change replaces the window
                    </td>
                  </tr>
                  <tr>
                    <td>data</td>
                    <td>middmonitor_organization</td>
                    <td>Reads the current organization's metadata</td>
                  </tr>
                  <tr>
                    <td>data</td>
                    <td>middmonitor_agent_install</td>
                    <td>
                      Builds install URLs and shell snippets from an install
                      token
                    </td>
                  </tr>
                </tbody>
              </table>
              <div className='doc-callout'>
                Every resource above is manageable from Terraform, alerting
                included. Argument names come from the provider schema, which is
                not always the shape of the REST API: an alert rule takes{' '}
                <code>custom_labels</code> as a <code>map(string)</code> where
                the API takes a list of key/value objects, and it has no{' '}
                <code>type</code> argument.
              </div>

              {/* ── resource: middmonitor_host ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_host
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  Creates and manages a host record. Both <code>name</code> and{' '}
                  <code>hostname</code> are immutable after creation — changing
                  either forces a replacement (<code>-/+</code> in plan output).
                  The API only allows updating <code>display_name</code> in
                  place.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Technical host name, unique per organization. Immutable
                        — forces replacement on change.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>hostname</td>
                      <td>string</td>
                      <td>
                        Resolvable address or IP used for checks. Immutable —
                        forces replacement on change.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>service</td>
                      <td>string</td>
                      <td>
                        Logical application/stack name. Defaults to{' '}
                        <code>name</code> if omitted.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>display_name</td>
                      <td>string</td>
                      <td>
                        Human-readable label in the UI.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Attributes reference (exported after apply)
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Attribute</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>id</td>
                      <td>number</td>
                      <td>
                        Host ID, assigned by the API. Use this to reference the
                        host from services.
                      </td>
                    </tr>
                    <tr>
                      <td>created_at</td>
                      <td>string</td>
                      <td>RFC3339 creation timestamp.</td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>Example</div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_host" "web_prod" {
  name         = "web-prod-01"
  display_name = "Production Web Server"
  hostname     = "10.0.1.10"
}

resource "middmonitor_host" "db_prod" {
  name     = "db-prod-01"
  hostname = "10.0.1.20"
}`}
                  />
                </div>
              </div>

              {/* ── resource: middmonitor_service ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_service
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  Creates and manages a service check attached to a host. The{' '}
                  <code>type</code> argument is immutable — changing the check
                  type forces a replacement. Threshold and interval arguments
                  are mutable and take effect on the next check execution.
                </p>
                <div className='doc-callout'>
                  <code>failure_threshold</code> is the legacy single threshold.
                  Prefer <code>warning_threshold</code> and{' '}
                  <code>critical_threshold</code>, which the provider exposes
                  too and which drive the two severity levels.
                </div>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>host_id</td>
                      <td>number</td>
                      <td>
                        ID of the parent <code>middmonitor_host</code>.
                        Immutable — forces replacement on change.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Display name for the check (e.g.{' '}
                        <code>"api-health"</code>). Immutable — forces
                        replacement on change.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>type</td>
                      <td>string</td>
                      <td>
                        <code>http</code> | <code>ping</code> | <code>sql</code>{' '}
                        | <code>certificate</code> | <code>snmp</code>.
                        Immutable after creation.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>hostname</td>
                      <td>string</td>
                      <td>
                        Check target: full URL for <code>http</code>;
                        hostname/IP for <code>ping</code>,{' '}
                        <code>certificate</code>, and <code>snmp</code>; DSN for{' '}
                        <code>sql</code> (
                        <code>postgres://user:pass@host/db</code>).{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>service</td>
                      <td>string</td>
                      <td>
                        Logical application/stack label (same meaning as on the
                        host).{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>path</td>
                      <td>string</td>
                      <td>
                        HTTP path, for <code>http</code> checks.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>credentials</td>
                      <td>string</td>
                      <td>
                        Optional JSON credentials (SQL, SNMP, HTTP auth).
                        Sensitive.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>service_interval</td>
                      <td>number</td>
                      <td>
                        Check frequency in seconds. Defaults to 60.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>max_attempts</td>
                      <td>number</td>
                      <td>
                        Consecutive failures before status changes. Defaults to
                        3. <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>failure_threshold</td>
                      <td>number</td>
                      <td>
                        Latency in ms (or days for <code>certificate</code>)
                        above which the service fails.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>expected_status_code</td>
                      <td>number</td>
                      <td>
                        For <code>http</code> only: exact HTTP status code
                        required for success. Defaults to any <code>2xx</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Attributes reference
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Attribute</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>id</td>
                      <td>number</td>
                      <td>Service ID, assigned by the API.</td>
                    </tr>
                    <tr>
                      <td>created_at</td>
                      <td>string</td>
                      <td>RFC3339 creation timestamp.</td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    Example — HTTP, certificate, and SQL checks
                  </div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_service" "api_health" {
  host_id              = middmonitor_host.web_prod.id
  name                 = "api-health"
  type                 = "http"
  hostname             = "https://api.example.com/health"
  service              = "api"
  service_interval     = 60
  failure_threshold    = 1500
  max_attempts         = 3
  expected_status_code = 200
}

resource "middmonitor_service" "api_tls" {
  host_id           = middmonitor_host.web_prod.id
  name              = "api-tls-cert"
  type              = "certificate"
  hostname          = "api.example.com"
  service           = "api"
  service_interval  = 3600    # check every hour
  failure_threshold = 7       # critical below 7 days before expiry
}

resource "middmonitor_service" "db_check" {
  host_id      = middmonitor_host.db_prod.id
  name         = "postgres-prod"
  type         = "sql"
  hostname     = "postgres://monitor:password@10.0.1.20:5432/app"
  service      = "postgres"
  service_interval = 30
  max_attempts = 2
}`}
                  />
                </div>
              </div>

              {/* ── resource: middmonitor_install_token ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_install_token
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  Generates an agent install token for the organization. It is
                  not bound to a single host — reuse it to install the agent on
                  several machines, each one registers as a new host
                  automatically. The secret is only returned on create and
                  stored in Terraform state as a sensitive value.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Label for this token in the UI. Immutable — forces
                        replacement on change.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>expires_at</td>
                      <td>string</td>
                      <td>
                        Optional RFC3339 expiry (e.g.{' '}
                        <code>2026-12-31T23:59:59Z</code>). Immutable — forces
                        replacement on change.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Attributes reference
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Attribute</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>token</td>
                      <td>string</td>
                      <td>
                        The raw install token. Sensitive. Only returned on
                        create.
                      </td>
                    </tr>
                    <tr>
                      <td>token_prefix</td>
                      <td>string</td>
                      <td>Short prefix for display purposes.</td>
                    </tr>
                    <tr>
                      <td>created_at</td>
                      <td>string</td>
                      <td>Creation timestamp.</td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    Example — feed the token to a cloud-init script
                  </div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_install_token" "fleet" {
  name = "terraform-managed-fleet"
}

data "middmonitor_agent_install" "web" {
  install_token = middmonitor_install_token.fleet.token
}

resource "aws_instance" "web" {
  ami           = "ami-0c55b159cbfafe1f0"
  instance_type = "t3.medium"
  user_data     = "#!/bin/bash\\n\${data.middmonitor_agent_install.web.curl_install_command}"
}`}
                  />
                </div>
              </div>

              {/* ── resource: middmonitor_host_group ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_host_group
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  A group of hosts. Alert correlation is scoped to a host and
                  its group, so the grouping changes what the platform is able
                  to relate rather than only how the UI looks.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Technical name, unique per organization.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>display_name</td>
                      <td>string</td>
                      <td>
                        Human-readable label shown in the UI.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Attributes reference
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Attribute</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>id</td>
                      <td>number</td>
                      <td>Host group ID.</td>
                    </tr>
                    <tr>
                      <td>is_default</td>
                      <td>bool</td>
                      <td>
                        Whether this is the organization's default group.
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>Example</div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_host_group" "edge" {
  name         = "edge"
  display_name = "Edge tier"
}`}
                  />
                </div>
              </div>

              {/* ── resource: middmonitor_notification_channel ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_notification_channel
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  A destination for alerts: email, Slack, a generic webhook,
                  Jira Service Management or WhatsApp.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Channel name.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>type</td>
                      <td>string</td>
                      <td>
                        <code>email</code> | <code>slack</code> |{' '}
                        <code>webhook</code> | <code>jsm</code> |{' '}
                        <code>whatsapp</code>.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>config</td>
                      <td>string</td>
                      <td>
                        Channel configuration as a JSON object — write it with{' '}
                        <code>jsonencode</code>. The accepted keys depend on{' '}
                        <code>type</code>, which is why this is a string rather
                        than a typed block.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>enabled</td>
                      <td>bool</td>
                      <td>
                        Whether alerts are delivered here. Defaults to{' '}
                        <code>true</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Keys of <code>config</code>, per type
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Type</th>
                      <th>Keys</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>email</td>
                      <td>
                        <code>emails</code> — comma-separated recipients.
                        Delivery uses the organization's own SMTP server,
                        configured in <strong>Settings → Email</strong>; without
                        one, nothing is sent.
                      </td>
                    </tr>
                    <tr>
                      <td>slack, webhook</td>
                      <td>
                        <code>webhook_url</code>, <code>secret</code> (HMAC
                        key), <code>format</code> (<code>slack</code> by
                        default, <code>structured</code> for the typed payload),{' '}
                        <code>headers</code>, and the anti-burst settings{' '}
                        <code>group_by</code>, <code>group_wait</code>,{' '}
                        <code>repeat_interval</code>.
                      </td>
                    </tr>
                    <tr>
                      <td>jsm</td>
                      <td>
                        <code>api_key</code> (API-integration key),{' '}
                        <code>tags</code>.
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-callout'>
                  <code>config</code> is an opaque string to Terraform: neither{' '}
                  <code>validate</code> nor <code>plan</code> can check its keys.
                  A webhook channel written with <code>url</code> instead of{' '}
                  <code>webhook_url</code> applies cleanly and delivers nowhere.
                  Test the channel from the dashboard, or with{' '}
                  <code>
                    POST /notification-channels/&#123;id&#125;/test
                  </code>
                  , after the first apply.
                </div>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    Example — a structured webhook
                  </div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_notification_channel" "oncall" {
  name = "oncall-webhook"
  type = "webhook"

  config = jsonencode({
    webhook_url     = var.oncall_webhook_url
    secret          = var.oncall_webhook_secret
    format          = "structured"
    group_by        = "host"
    group_wait      = 30
    repeat_interval = 3600
  })
}`}
                  />
                </div>
              </div>

              {/* ── resource: middmonitor_alert_rule ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_alert_rule
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  A threshold rule evaluated every minute. Set{' '}
                  <code>metric</code> for a built-in signal, or{' '}
                  <code>custom_metric</code> to watch a series the agent scrapes
                  or an SDK reports. There is no <code>type</code> argument —
                  which of the two is set decides the form of the rule.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Rule name. It becomes the incident title.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>operator</td>
                      <td>string</td>
                      <td>
                        <code>gt</code>, <code>gte</code>, <code>lt</code>,{' '}
                        <code>lte</code> or <code>eq</code>.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>description</td>
                      <td>string</td>
                      <td>
                        Free-text description.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>metric</td>
                      <td>string</td>
                      <td>
                        Built-in signal: <code>cpu</code>, <code>ram</code>,{' '}
                        <code>disk</code>, <code>latency</code>,{' '}
                        <code>error_count</code>, <code>failure_rate</code>.
                        Leave unset when using <code>custom_metric</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>custom_metric</td>
                      <td>string</td>
                      <td>
                        Name of a scraped or SDK-reported series, e.g.{' '}
                        <code>node_load1</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>custom_labels</td>
                      <td>map(string)</td>
                      <td>
                        Label equality constraints narrowing the custom metric
                        series. A map here, unlike the REST API, which takes a
                        list of key/value objects.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>target_type</td>
                      <td>string</td>
                      <td>
                        <code>any</code> (default), <code>service</code> or{' '}
                        <code>host</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>target_id</td>
                      <td>number</td>
                      <td>
                        The service or host the rule is scoped to, when{' '}
                        <code>target_type</code> is not <code>any</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>aggregation</td>
                      <td>string</td>
                      <td>
                        <code>avg</code> (default), <code>min</code>,{' '}
                        <code>max</code>, <code>sum</code>, <code>p50</code>,{' '}
                        <code>p75</code>, <code>p90</code>, <code>p95</code>,{' '}
                        <code>p99</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>threshold</td>
                      <td>number</td>
                      <td>
                        Single threshold. Ignored when{' '}
                        <code>warning_threshold</code> or{' '}
                        <code>critical_threshold</code> is set.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>warning_threshold</td>
                      <td>number</td>
                      <td>
                        Raises a warning when crossed.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>critical_threshold</td>
                      <td>number</td>
                      <td>
                        Raises a critical alert when crossed. Wins over the
                        warning threshold.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>recovery_threshold</td>
                      <td>number</td>
                      <td>
                        Hysteresis: the incident resolves only once the value
                        crosses back past this.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>duration</td>
                      <td>number</td>
                      <td>
                        Evaluation window in seconds. Defaults to 300.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>severity</td>
                      <td>string</td>
                      <td>
                        Severity of the single-threshold form:{' '}
                        <code>warning</code> or <code>critical</code>. Defaults
                        to <code>critical</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>enabled</td>
                      <td>bool</td>
                      <td>
                        Whether the rule is evaluated. Defaults to{' '}
                        <code>true</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>channels</td>
                      <td>list(number)</td>
                      <td>
                        Notification channel IDs. Empty means every enabled
                        channel of the organization.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>notify_warning</td>
                      <td>bool</td>
                      <td>
                        Deliver warning-severity alerts on these channels.
                        Defaults to <code>true</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>notify_critical</td>
                      <td>bool</td>
                      <td>
                        Deliver critical-severity alerts on these channels.
                        Defaults to <code>true</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    Example — a rule on a scraped series
                  </div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_alert_rule" "load" {
  name          = "Load average high"
  custom_metric = "node_load1"

  custom_labels = {
    instance = "10.0.1.5:9100"
  }

  target_type = "host"
  target_id   = middmonitor_host.web_prod.id

  aggregation        = "avg"
  operator           = "gt"
  warning_threshold  = 4
  critical_threshold = 8
  recovery_threshold = 3
  duration           = 300

  channels = [middmonitor_notification_channel.oncall.id]
}`}
                  />
                </div>
              </div>

              {/* ── resource: middmonitor_maintenance_window ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  resource: middmonitor_maintenance_window
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  A period during which alerts on one target are suppressed. The
                  API has no update, so changing any argument replaces the
                  window.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>
                        Why the window exists, shown on the suppressed alert.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>target_type</td>
                      <td>string</td>
                      <td>
                        <code>service</code> or <code>host</code>.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>target_id</td>
                      <td>number</td>
                      <td>
                        ID of the service or host whose alerts are suppressed.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>starts_at</td>
                      <td>string</td>
                      <td>
                        Start of the window, RFC3339.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>ends_at</td>
                      <td>string</td>
                      <td>
                        End of the window, RFC3339.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>Example</div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_maintenance_window" "db_upgrade" {
  name        = "Postgres major version upgrade"
  target_type = "host"
  target_id   = middmonitor_host.db_prod.id
  starts_at   = "2026-10-04T22:00:00Z"
  ends_at     = "2026-10-05T02:00:00Z"
}`}
                  />
                </div>
              </div>

              {/* ── data: middmonitor_organization ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  data: middmonitor_organization
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  Reads metadata about the current organization. No arguments
                  are required — the organization is derived from the provider's{' '}
                  <code>org_slug</code>. Use the exported attributes to verify
                  that the provider is pointing at the correct organization
                  before applying changes.
                </p>
                <p className='doc-endpoint-section-label'>
                  Attributes reference
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Attribute</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>id</td>
                      <td>number</td>
                      <td>Internal organization ID.</td>
                    </tr>
                    <tr>
                      <td>slug</td>
                      <td>string</td>
                      <td>URL-safe organization identifier.</td>
                    </tr>
                    <tr>
                      <td>name</td>
                      <td>string</td>
                      <td>Display name of the organization.</td>
                    </tr>
                    <tr>
                      <td>plan</td>
                      <td>string</td>
                      <td>Current subscription plan.</td>
                    </tr>
                  </tbody>
                </table>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>Example</div>
                  <CodeBlock
                    language='hcl'
                    code={`data "middmonitor_organization" "current" {}

output "org_plan" {
  value = data.middmonitor_organization.current.plan
}

# Guard against accidental apply in the wrong org
locals {
  assert_org = (
    data.middmonitor_organization.current.slug == "acme"
    ? true
    : tobool("ERROR: wrong organization — expected acme, got \${data.middmonitor_organization.current.slug}")
  )
}`}
                  />
                </div>
                <div className='doc-callout'>
                  This guard is not a security boundary — the API already scopes
                  every token to a single organization server-side, so a token
                  for org A cannot read or write org B's resources no matter
                  what <code>org_slug</code> is set in the provider block. It
                  protects against a human/CI mistake instead: in a company with
                  several organizations (e.g. staging and prod), it's easy to
                  apply a root module meant for one org while{' '}
                  <code>TF_VAR_middmonitor_token</code> is still exported for
                  another. The assertion fails <code>terraform plan</code>{' '}
                  loudly instead of quietly applying to the wrong environment.
                </div>
              </div>

              {/* ── data: middmonitor_agent_install ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  data: middmonitor_agent_install
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  Builds install URLs and shell snippets for a given install
                  token. It does not run anything on remote machines — pass the
                  resulting command to a <code>cloud_init</code> script or a
                  <code> remote-exec</code> provisioner yourself.
                </p>
                <p className='doc-endpoint-section-label'>Argument reference</p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Argument</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>install_token</td>
                      <td>string</td>
                      <td>
                        Install token, e.g. from{' '}
                        <code>middmonitor_install_token.*.token</code>.
                        Sensitive.{' '}
                        <span className='doc-param-required'>required</span>
                      </td>
                    </tr>
                    <tr>
                      <td>os</td>
                      <td>string</td>
                      <td>
                        <code>linux</code> or <code>darwin</code>. Defaults to{' '}
                        <code>linux</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                    <tr>
                      <td>arch</td>
                      <td>string</td>
                      <td>
                        <code>amd64</code> or <code>arm64</code>. Defaults to{' '}
                        <code>amd64</code>.{' '}
                        <span className='doc-param-optional'>optional</span>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Attributes reference
                </p>
                <table
                  className='doc-param-table'
                  style={{ marginBottom: '1rem' }}>
                  <thead>
                    <tr>
                      <th>Attribute</th>
                      <th>Type</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>install_script_url</td>
                      <td>string</td>
                      <td>
                        GET URL returning the install shell script, with the
                        token embedded.
                      </td>
                    </tr>
                    <tr>
                      <td>agent_binary_url</td>
                      <td>string</td>
                      <td>Direct download URL for the agent binary.</td>
                    </tr>
                    <tr>
                      <td>curl_install_command</td>
                      <td>string</td>
                      <td>
                        One-liner to download and run the install script with
                        curl. Sensitive.
                      </td>
                    </tr>
                    <tr>
                      <td>export_env_snippet</td>
                      <td>string</td>
                      <td>
                        Shell snippet exporting the API URL and token for manual
                        installs. Sensitive.
                      </td>
                    </tr>
                  </tbody>
                </table>
                <p className='doc-endpoint-section-label'>
                  Example — install on an existing machine over SSH
                </p>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  For a machine Terraform didn't just create (so there's no{' '}
                  <code>user_data</code>/cloud-init hook to attach to), run the
                  install command over SSH with a <code>null_resource</code> +{' '}
                  <code>remote-exec</code> provisioner instead:
                </p>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    Install on an existing host via remote-exec
                  </div>
                  <CodeBlock
                    language='hcl'
                    code={`resource "middmonitor_install_token" "fleet" {
  name = "terraform-managed-fleet"
}

data "middmonitor_agent_install" "existing" {
  install_token = middmonitor_install_token.fleet.token
  os            = "linux"
  arch          = "amd64"
}

resource "null_resource" "install_agent" {
  connection {
    type        = "ssh"
    host        = "10.0.1.10"
    user        = "ubuntu"
    private_key = file("~/.ssh/id_ed25519")
  }

  provisioner "remote-exec" {
    inline = [
      data.middmonitor_agent_install.existing.curl_install_command,
    ]
  }
}`}
                  />
                </div>
              </div>

              {/* ── Full production example ── */}
              <div className='doc-resource-group'>
                <div className='doc-resource-group-title'>
                  Complete production example
                </div>
                <p
                  style={{
                    fontSize: '0.875rem',
                    color: 'var(--doc-muted)',
                    marginBottom: '1rem',
                  }}>
                  The following provisions two hosts, four service checks, a
                  reusable install token, a notification channel and the alert
                  rule that uses it — all in one <code>terraform apply</code>.
                </p>
                <div className='doc-code-block'>
                  <div className='doc-code-header'>
                    main.tf — production monitoring stack
                  </div>
                  <CodeBlock
                    language='hcl'
                    code={`# ── Provider ───────────────────────────────────────────────────────────
provider "middmonitor" {
  org_slug = "acme"
  # access_token and base_url from env vars:
  # MIDDLE_MONITOR_ACCESS_TOKEN, MIDDLE_MONITOR_BASE_URL
}

# ── Hosts ───────────────────────────────────────────────────────────────
resource "middmonitor_host" "web" {
  name         = "web-prod-01"
  display_name = "Production Web Server"
  hostname     = "10.0.1.10"
}

resource "middmonitor_host" "db" {
  name         = "db-prod-01"
  display_name = "Production Database"
  hostname     = "10.0.1.20"
}

# ── Services ─────────────────────────────────────────────────────────────
resource "middmonitor_service" "api_http" {
  host_id              = middmonitor_host.web.id
  name                 = "api-health"
  type                 = "http"
  hostname             = "https://api.example.com/health"
  service              = "api"
  service_interval     = 60
  failure_threshold    = 1500
  max_attempts         = 3
  expected_status_code = 200
}

resource "middmonitor_service" "api_tls" {
  host_id           = middmonitor_host.web.id
  name              = "api-tls-cert"
  type              = "certificate"
  hostname          = "api.example.com"
  service           = "api"
  service_interval  = 3600
  failure_threshold = 7
}

resource "middmonitor_service" "postgres" {
  host_id          = middmonitor_host.db.id
  name             = "postgres-prod"
  type             = "sql"
  hostname         = "postgres://monitor:\${var.db_monitor_password}@10.0.1.20:5432/app"
  service          = "postgres"
  service_interval = 30
  max_attempts     = 2
}

resource "middmonitor_service" "db_ping" {
  host_id           = middmonitor_host.web.id
  name              = "db-ping"
  type              = "ping"
  hostname          = "10.0.1.20"
  service           = "postgres"
  service_interval  = 30
  failure_threshold = 100
}

# ── Alerting ─────────────────────────────────────────────────────────────
resource "middmonitor_notification_channel" "oncall" {
  name = "oncall-webhook"
  type = "webhook"

  config = jsonencode({
    webhook_url = var.oncall_webhook_url
    format      = "structured"
    group_by    = "host"
    group_wait  = 30
  })
}

resource "middmonitor_alert_rule" "api_latency" {
  name        = "API latency degraded"
  metric      = "latency"
  target_type = "service"
  target_id   = middmonitor_service.api_http.id

  aggregation        = "p95"
  operator           = "gt"
  warning_threshold  = 800
  critical_threshold = 1500
  duration           = 300

  channels = [middmonitor_notification_channel.oncall.id]
}

# ── Agent install token (org-scoped, reusable across hosts) ─────────────
resource "middmonitor_install_token" "fleet" {
  name = "terraform-managed-fleet"
}

output "fleet_install_token" {
  value     = middmonitor_install_token.fleet.token
  sensitive = true
}`}
                  />
                </div>
              </div>
            </div>
          </section>
        </main>
      </div>

      <footer className='doc-footer'>
        <div className='doc-footer-inner'>
          <div className='doc-footer-links'>
            <Link to='/'>{t('public.nav_home')}</Link>
            <Link to='/docs'>{t('public.nav_docs')}</Link>
            <Link to='/pricing'>{t('docs.nav_pricing')}</Link>
            <Link to='/alternatives'>{t('public.nav_alternatives')}</Link>
            <Link to='/legal'>{t('public.nav_legal')}</Link>
            <Link to='/privacy'>{t('public.nav_privacy')}</Link>
            <Link to='/terms'>{t('public.nav_terms')}</Link>
            <LanguageSwitcher variant='compact' className='doc-footer-lang' />
          </div>
          <p className='doc-footer-copy'>
            © {new Date().getFullYear()} Middle Monitor.{' '}
            {t('pricing.copyright')}
          </p>
        </div>
      </footer>
    </div>
  );
}
