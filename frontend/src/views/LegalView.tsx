import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import { SiteHeader } from '../components/SiteHeader';
import { useDocumentMeta, type SeoPage } from '../seo/useDocumentMeta';
import './LegalView.css';

export type LegalPage = 'notice' | 'privacy' | 'terms';

const SEO_KEYS: Record<LegalPage, SeoPage> = {
  notice: 'legal',
  privacy: 'privacy',
  terms: 'terms',
};

// Section key order per page; mirrored by legalBody() in scripts/seo-prerender.mjs.
export const LEGAL_SECTIONS: Record<LegalPage, string[]> = {
  notice: ['publisher', 'contact', 'hosting', 'ip'],
  privacy: [
    'account',
    'telemetry',
    'billing',
    'contact_form',
    'analytics',
    'hosting',
    'retention',
    'sharing',
    'security',
    'cookies',
    'rights',
    'changes',
  ],
  terms: [
    'service',
    'accounts',
    'use',
    'data',
    'billing',
    'availability',
    'liability',
    'termination',
    'changes',
    'law',
  ],
};

// Body text: "- " lines render as list items, other lines as paragraphs.
function SectionBody({ text }: { text: string }) {
  const lines = text.split('\n');
  const blocks: React.ReactNode[] = [];
  let items: string[] = [];

  const flushItems = () => {
    if (items.length === 0) return;
    blocks.push(
      <ul key={`ul-${blocks.length}`}>
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    );
    items = [];
  };

  for (const line of lines) {
    if (line.startsWith('- ')) {
      items.push(line.slice(2));
    } else {
      flushItems();
      blocks.push(<p key={`p-${blocks.length}`}>{line}</p>);
    }
  }
  flushItems();
  return <>{blocks}</>;
}

export default function LegalView({ page }: { page: LegalPage }) {
  const { t } = useTranslation();
  useDocumentMeta(SEO_KEYS[page]);

  return (
    <div className='legal-view'>
      <SiteHeader active='home' />

      <main className='legal-main'>
        <div className='legal-container'>
          <nav className='legal-toc' aria-label='Legal pages'>
            <Link
              to='/legal'
              className={page === 'notice' ? 'legal-toc-active' : ''}>
              {t('legal.notice.title')}
            </Link>
            <Link
              to='/privacy'
              className={page === 'privacy' ? 'legal-toc-active' : ''}>
              {t('legal.privacy.title')}
            </Link>
            <Link
              to='/terms'
              className={page === 'terms' ? 'legal-toc-active' : ''}>
              {t('legal.terms.title')}
            </Link>
          </nav>

          <article>
            <h1>{t(`legal.${page}.title`)}</h1>
            <p className='legal-updated'>{t('legal.updated')}</p>
            <p className='legal-intro'>{t(`legal.${page}.intro`)}</p>

            {LEGAL_SECTIONS[page].map((section) => (
              <section key={section}>
                <h2>{t(`legal.${page}.${section}_title`)}</h2>
                <SectionBody text={t(`legal.${page}.${section}_body`)} />
              </section>
            ))}

            <p className='legal-contact-cta'>
              <Link to='/contact'>{t('legal.contact_cta')}</Link>
            </p>
          </article>
        </div>
      </main>

      <footer className='legal-footer'>
        <div className='legal-footer-inner'>
          <div className='legal-footer-links'>
            <Link to='/'>{t('public.nav_home')}</Link>
            <Link to='/docs'>{t('public.nav_docs')}</Link>
            <Link to='/pricing'>{t('public.nav_pricing')}</Link>
            <Link to='/alternatives'>{t('public.nav_alternatives')}</Link>
            <Link to='/legal'>{t('public.nav_legal')}</Link>
            <Link to='/privacy'>{t('public.nav_privacy')}</Link>
            <Link to='/terms'>{t('public.nav_terms')}</Link>
            <LanguageSwitcher variant='compact' className='legal-footer-lang' />
          </div>
          <p className='legal-footer-copy'>
            © {new Date().getFullYear()} Middle Monitor.{' '}
            {t('pricing.copyright')}
          </p>
        </div>
      </footer>
    </div>
  );
}
