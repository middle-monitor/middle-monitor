import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { LanguageSwitcher } from '../components/LanguageSwitcher/LanguageSwitcher';
import { SiteHeader } from '../components/SiteHeader';
import { useDocumentMeta, type SeoPage } from '../seo/useDocumentMeta';
import comparisons from '../seo/comparisons.json';
import './ComparisonView.css';

// Index page when slug is omitted; mirrored by comparisonBody() in
// scripts/seo-prerender.mjs.
export default function ComparisonView({ slug }: { slug?: string }) {
  const { t } = useTranslation();
  const page = slug ? comparisons.pages.find((p) => p.slug === slug) : undefined;
  useDocumentMeta((page ? `alt-${page.slug}` : 'alternatives') as SeoPage);

  return (
    <div className='comparison-view'>
      <SiteHeader active='home' />

      <main className='comparison-main'>
        <div className='comparison-container'>
          <nav className='comparison-toc' aria-label='Comparisons'>
            <Link to='/alternatives' className={page ? '' : 'comparison-toc-active'}>
              {t('public.nav_home')}
            </Link>
            {comparisons.pages.map((p) => (
              <Link
                key={p.slug}
                to={`/alternatives/${p.slug}`}
                className={p.slug === slug ? 'comparison-toc-active' : ''}>
                {p.competitor}
              </Link>
            ))}
          </nav>

          {page ? (
            <article>
              <h1>{page.h1}</h1>
              <p className='comparison-lead'>{page.lead}</p>
              <p className='comparison-fair'>{page.fair}</p>

              {page.sections.map((s) => (
                <section key={s.title}>
                  <h2>{s.title}</h2>
                  <p>{s.body}</p>
                </section>
              ))}

              <section>
                <h2>Frequently asked questions</h2>
                {page.faq.map((f) => (
                  <div key={f.q} className='comparison-faq-item'>
                    <h3>{f.q}</h3>
                    <p>{f.a}</p>
                  </div>
                ))}
              </section>

              <p className='comparison-cta'>
                <Link to='/get-started'>Start monitoring free</Link>
                <Link to='/docs'>{t('public.nav_docs')}</Link>
                <Link to='/pricing'>{t('public.nav_pricing')}</Link>
              </p>
            </article>
          ) : (
            <article>
              <h1>{comparisons.intro.h1}</h1>
              <p className='comparison-lead'>{comparisons.intro.lead}</p>
              <p className='comparison-fair'>{comparisons.intro.note}</p>

              <ul className='comparison-index'>
                {comparisons.pages.map((p) => (
                  <li key={p.slug}>
                    <Link to={`/alternatives/${p.slug}`}>
                      <strong>{p.competitor}</strong>
                      <span>{p.lead}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </article>
          )}
        </div>
      </main>

      <footer className='comparison-footer'>
        <div className='comparison-footer-inner'>
          <div className='comparison-footer-links'>
            <Link to='/'>{t('public.nav_home')}</Link>
            <Link to='/docs'>{t('public.nav_docs')}</Link>
            <Link to='/pricing'>{t('public.nav_pricing')}</Link>
            <Link to='/alternatives'>{t('public.nav_alternatives')}</Link>
            <Link to='/legal'>{t('public.nav_legal')}</Link>
            <Link to='/privacy'>{t('public.nav_privacy')}</Link>
            <Link to='/terms'>{t('public.nav_terms')}</Link>
            <LanguageSwitcher
              variant='compact'
              className='comparison-footer-lang'
            />
          </div>
          <p className='comparison-footer-copy'>
            © {new Date().getFullYear()} Middle Monitor.{' '}
            {t('pricing.copyright')}
          </p>
        </div>
      </footer>
    </div>
  );
}
