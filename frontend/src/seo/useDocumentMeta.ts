import { useEffect } from 'react';
import pagesData from './pages.json';

export type SeoPage = keyof typeof pagesData.pages;

// Upsert a <meta> tag by its name/property attribute so client-side navigation
// keeps the head in sync with the current page. Crawlers that don't run JS get
// the correct tags from the prerendered HTML (see scripts/seo-prerender.mjs);
// this hook covers soft navigation and JS-rendering engines (Googlebot).
function upsertMeta(attr: 'name' | 'property', key: string, content: string) {
  let el = document.head.querySelector<HTMLMetaElement>(`meta[${attr}="${key}"]`);
  if (!el) {
    el = document.createElement('meta');
    el.setAttribute(attr, key);
    document.head.appendChild(el);
  }
  el.setAttribute('content', content);
}

function upsertLink(rel: string, href: string) {
  let el = document.head.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`);
  if (!el) {
    el = document.createElement('link');
    el.setAttribute('rel', rel);
    document.head.appendChild(el);
  }
  el.setAttribute('href', href);
}

export function useDocumentMeta(page: SeoPage) {
  useEffect(() => {
    const p = pagesData.pages[page];
    const url = pagesData.siteUrl + p.path;
    const image = pagesData.siteUrl + pagesData.ogImage;

    document.title = p.title;
    upsertMeta('name', 'description', p.description);
    upsertLink('canonical', url);

    upsertMeta('property', 'og:type', 'website');
    upsertMeta('property', 'og:site_name', pagesData.siteName);
    upsertMeta('property', 'og:title', p.title);
    upsertMeta('property', 'og:description', p.description);
    upsertMeta('property', 'og:url', url);
    upsertMeta('property', 'og:image', image);
    upsertMeta('property', 'og:image:width', '1200');
    upsertMeta('property', 'og:image:height', '630');
    upsertMeta('property', 'og:image:alt', pagesData.siteName);

    upsertMeta('name', 'twitter:card', 'summary_large_image');
    upsertMeta('name', 'twitter:title', p.title);
    upsertMeta('name', 'twitter:description', p.description);
    upsertMeta('name', 'twitter:image', image);
  }, [page]);
}
