// Build-time SEO prerender (no browser).
//
// The app is a client-rendered SPA: the shipped index.html has an empty
// <div id="root"></div>, so crawlers that don't execute JavaScript (most AI
// answer engines: GPTBot, ClaudeBot, PerplexityBot, ...) see no content.
//
// After `vite build`, this script rewrites the built shell into one static HTML
// file per public route with:
//   - per-route <title>, description, canonical, Open Graph, Twitter and JSON-LD
//   - real, semantic body content injected into #root (React replaces it on mount)
// It also emits sitemap.xml. Text is sourced from the same locale file the app
// renders (src/locales/en.json), so the static HTML mirrors the live page.

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.cwd(); // frontend/
const DIST = join(ROOT, 'dist');

const site = JSON.parse(readFileSync(join(ROOT, 'src/seo/pages.json'), 'utf8'));
const en = JSON.parse(readFileSync(join(ROOT, 'src/locales/en.json'), 'utf8'));
const docsSections = JSON.parse(
  readFileSync(join(ROOT, 'src/seo/docs-sections.json'), 'utf8')
);
const comparisons = JSON.parse(
  readFileSync(join(ROOT, 'src/seo/comparisons.json'), 'utf8')
);
const { siteUrl, siteName, ogImage, pages } = site;
const OG_IMAGE_URL = siteUrl + ogImage;

const esc = (s) =>
  String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');

// ----- Static body content per route (mirrors the live views) -----

function homeBody() {
  const h = en.home;
  const feature = (title, desc) =>
    `<article><h3>${esc(title)}</h3><p>${esc(desc)}</p></article>`;
  return `
    <h1>${esc(h.hero.title_1)} ${esc(h.hero.title_2)} ${esc(h.hero.title_3)}</h1>
    <p>${esc(h.hero.subtitle)}</p>
    <p><a href="/get-started">${esc(h.hero.cta_start)}</a> &middot; <a href="/demo">${esc(h.hero.cta_demo)}</a></p>
    <section>
      <h2>${esc(h.platform.section_title_1)} ${esc(h.platform.section_title_2)}</h2>
      <p>${esc(h.platform.section_subtitle)}</p>
      <ul>
        <li>${esc(h.platform.tab_traces)}</li>
        <li>${esc(h.platform.tab_metrics)}</li>
        <li>${esc(h.platform.tab_errors)}</li>
        <li>${esc(h.platform.tab_logs)}</li>
      </ul>
    </section>
    <section>
      <h2>${esc(h.integration.title_1)} ${esc(h.integration.title_2)}</h2>
      <p>${esc(h.integration.subtitle)}</p>
      <ul>
        <li>${esc(h.integration.feature_context)}</li>
        <li>${esc(h.integration.feature_metrics)}</li>
        <li>${esc(h.integration.feature_config)}</li>
      </ul>
    </section>
    <section>
      <h2>Features</h2>
      ${feature(h.bento.agent_title, h.bento.agent_desc)}
      ${feature(h.bento.rca_title, h.bento.rca_desc)}
      ${feature(h.bento.alerts_title, h.bento.alerts_desc)}
    </section>
    <section>
      <h2>${esc(h.faq.title)}</h2>
      <p>${esc(h.faq.subtitle)}</p>
      ${faqItems().map(({ q, a }) => `<article><h3>${esc(q)}</h3><p>${esc(a)}</p></article>`).join('\n      ')}
    </section>
    <nav aria-label="Site">
      <a href="/pricing">${esc(h.nav.pricing)}</a>
      <a href="/docs">${esc(h.nav.docs)}</a>
      <a href="/alternatives">${esc(en.public.nav_alternatives)}</a>
      <a href="/status">${esc(en.public.nav_status)}</a>
      <a href="/login">${esc(h.nav.sign_in)}</a>
      <a href="/get-started">${esc(h.nav.get_started)}</a>
    </nav>`;
}

// Home FAQ: q1/a1..q8/a8 in en.json, grouped by competitor category
// (Datadog, Sentry/Rollbar/Bugsnag, Icinga/Nagios/Zabbix, ...). Shared by
// homeBody() (static markup) and jsonLd() (FAQPage schema) so both stay in
// sync with the same source of truth.
function faqItems() {
  const h = en.home.faq;
  return [1, 2, 3, 4, 5, 6, 7, 8, 9].map((n) => ({ q: h[`q${n}`], a: h[`a${n}`] }));
}

function pricingBody() {
  const p = en.pricing;
  const plan = (tier) =>
    `<section>
      <h2>${esc(tier.name)}</h2>
      <p>${esc(tier.description)}</p>
      <ul>${Object.keys(tier)
        .filter((k) => k.startsWith('f_'))
        .map((k) => `<li>${esc(tier[k])}</li>`)
        .join('')}</ul>
    </section>`;
  return `
    <h1>${esc(p.title)}</h1>
    <p>${esc(p.subtitle)}</p>
    ${plan(p.free)}
    ${plan(p.pro)}
    ${plan(p.enterprise)}`;
}

// Section prose comes from docs-sections.json's `summary`, not from en.docs:
// DocumentationView.tsx renders hardcoded English for these sections and no
// longer reads most of those locale keys, so sourcing them here would serve
// crawlers text the live page never shows.
function docsBody() {
  const d = en.docs;
  const categories = [...new Set(docsSections.map((s) => s.category))];
  const article = (s) => `
        <article id="${esc(s.id)}">
          <h3>${esc(s.title)}</h3>
          <p>${esc(s.excerpt)}</p>
          <p>${esc(s.summary)}</p>
        </article>`;
  return `
    <h1>${esc(d.hero_title)}</h1>
    <p>${esc(d.hero_subtitle)}</p>
    <nav aria-label="Documentation sections">
      <ul>${docsSections
        .map((s) => `<li><a href="/docs#${esc(s.id)}">${esc(s.title)}</a></li>`)
        .join('')}</ul>
    </nav>
    ${categories
      .map(
        (c) => `<section>
      <h2>${esc(c)}</h2>${docsSections
        .filter((s) => s.category === c)
        .map(article)
        .join('')}
    </section>`
      )
      .join('\n    ')}`;
}

// The live status is fetched at runtime, so the prerendered shell must not state
// one: a crawler cached page claiming "all systems operational" would outlive the
// incident it was built before. It describes the page and links onward instead.
function statusBody() {
  const p = pages.status;
  return `
    <h1>${esc(p.title)}</h1>
    <p>${esc(p.description)}</p>
    <p><a href="/status">${esc(en.public.nav_status)}</a></p>
    <nav aria-label="Site">
      <a href="/">Home</a>
      <a href="/pricing">${esc(en.home.nav.pricing)}</a>
      <a href="/docs">${esc(en.home.nav.docs)}</a>
      <a href="/contact">${esc(en.public.nav_contact)}</a>
    </nav>`;
}

function contactBody() {
  const c = en.contact;
  return `
    <h1>${esc(c.title)}</h1>
    <p>${esc(c.subtitle)}</p>
    <p><a href="/contact">${esc(c.send)}</a></p>
    <nav aria-label="Site">
      <a href="/">Home</a>
      <a href="/pricing">${esc(en.home.nav.pricing)}</a>
      <a href="/docs">${esc(en.home.nav.docs)}</a>
      <a href="/alternatives">${esc(en.public.nav_alternatives)}</a>
    </nav>`;
}

// Mirrors LEGAL_SECTIONS in src/views/LegalView.tsx.
const LEGAL_SECTIONS = {
  notice: ['publisher', 'contact', 'hosting', 'ip'],
  privacy: [
    'account', 'telemetry', 'billing', 'contact_form', 'analytics', 'hosting',
    'retention', 'sharing', 'security', 'cookies', 'rights', 'changes',
  ],
  terms: [
    'service', 'accounts', 'use', 'data', 'billing', 'availability',
    'liability', 'termination', 'changes', 'law',
  ],
};

// "- " lines become list items, other lines paragraphs (same as SectionBody).
function legalHtml(body) {
  const out = [];
  let items = [];
  const flush = () => {
    if (items.length === 0) return;
    out.push(`<ul>${items.map((i) => `<li>${esc(i)}</li>`).join('')}</ul>`);
    items = [];
  };
  for (const line of body.split('\n')) {
    if (line.startsWith('- ')) items.push(line.slice(2));
    else {
      flush();
      out.push(`<p>${esc(line)}</p>`);
    }
  }
  flush();
  return out.join('');
}

function legalBody(pageKey) {
  const l = en.legal[pageKey];
  const sections = LEGAL_SECTIONS[pageKey]
    .map(
      (s) =>
        `<section><h2>${esc(l[`${s}_title`])}</h2>${legalHtml(l[`${s}_body`])}</section>`
    )
    .join('\n    ');
  return `
    <h1>${esc(l.title)}</h1>
    <p>${esc(en.legal.updated)}</p>
    <p>${esc(l.intro)}</p>
    ${sections}
    <nav aria-label="Legal pages">
      <a href="/legal">${esc(en.legal.notice.title)}</a>
      <a href="/privacy">${esc(en.legal.privacy.title)}</a>
      <a href="/terms">${esc(en.legal.terms.title)}</a>
      <a href="/alternatives">${esc(en.public.nav_alternatives)}</a>
      <a href="/contact">${esc(en.legal.contact_cta)}</a>
    </nav>`;
}

// Mirrors ComparisonView.tsx: index when slug is omitted, one page per competitor.
function comparisonBody(slug) {
  const nav = `
    <nav aria-label="Comparisons">
      <a href="/alternatives">${esc(comparisons.intro.h1)}</a>
      ${comparisons.pages
        .map(
          (p) =>
            `<a href="/alternatives/${esc(p.slug)}">${esc(p.competitor)}</a>`
        )
        .join('\n      ')}
    </nav>`;

  if (!slug) {
    return `
    <h1>${esc(comparisons.intro.h1)}</h1>
    <p>${esc(comparisons.intro.lead)}</p>
    <p>${esc(comparisons.intro.note)}</p>
    <ul>${comparisons.pages
      .map(
        (p) =>
          `<li><a href="/alternatives/${esc(p.slug)}"><strong>${esc(p.competitor)}</strong></a> ${esc(p.lead)}</li>`
      )
      .join('')}</ul>
    ${nav}`;
  }

  const p = comparisons.pages.find((c) => c.slug === slug);
  return `
    <h1>${esc(p.h1)}</h1>
    <p>${esc(p.lead)}</p>
    <p>${esc(p.fair)}</p>
    ${p.sections
      .map(
        (s) => `<section><h2>${esc(s.title)}</h2><p>${esc(s.body)}</p></section>`
      )
      .join('\n    ')}
    <section>
      <h2>Frequently asked questions</h2>
      ${p.faq
        .map((f) => `<article><h3>${esc(f.q)}</h3><p>${esc(f.a)}</p></article>`)
        .join('\n      ')}
    </section>
    <p><a href="/get-started">Start monitoring free</a> &middot; <a href="/docs">Documentation</a> &middot; <a href="/pricing">Pricing</a></p>
    ${nav}`;
}

const BODIES = {
  home: homeBody,
  pricing: pricingBody,
  docs: docsBody,
  contact: contactBody,
  status: statusBody,
  legal: () => legalBody('notice'),
  privacy: () => legalBody('privacy'),
  terms: () => legalBody('terms'),
  alternatives: () => comparisonBody(),
  ...Object.fromEntries(
    comparisons.pages.map((p) => [`alt-${p.slug}`, () => comparisonBody(p.slug)])
  ),
};

// ----- JSON-LD -----

function jsonLd(key, url) {
  const blocks = [];
  if (key === 'home') {
    blocks.push({
      '@context': 'https://schema.org',
      '@type': 'SoftwareApplication',
      name: siteName,
      applicationCategory: 'DeveloperApplication',
      operatingSystem: 'Linux, macOS',
      description: pages.home.description,
      url: siteUrl,
      offers: { '@type': 'Offer', price: '0', priceCurrency: 'EUR' },
    });
    blocks.push({
      '@context': 'https://schema.org',
      '@type': 'FAQPage',
      mainEntity: faqItems().map(({ q, a }) => ({
        '@type': 'Question',
        name: q,
        acceptedAnswer: { '@type': 'Answer', text: a },
      })),
    });
  }
  if (key === 'docs') {
    blocks.push({
      '@context': 'https://schema.org',
      '@type': 'TechArticle',
      headline: pages.docs.title,
      description: pages.docs.description,
      url,
      hasPart: docsSections.map((s) => ({
        '@type': 'TechArticle',
        '@id': `${url}#${s.id}`,
        headline: s.title,
        articleSection: s.category,
        description: s.excerpt,
        keywords: s.keywords.join(', '),
      })),
    });
  }
  if (key.startsWith('alt-')) {
    const p = comparisons.pages.find((c) => `alt-${c.slug}` === key);
    blocks.push({
      '@context': 'https://schema.org',
      '@type': 'FAQPage',
      mainEntity: p.faq.map((f) => ({
        '@type': 'Question',
        name: f.q,
        acceptedAnswer: { '@type': 'Answer', text: f.a },
      })),
    });
  }
  blocks.push({
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'Home', item: siteUrl + '/' },
      ...(key.startsWith('alt-')
        ? [
            {
              '@type': 'ListItem',
              position: 2,
              name: pages.alternatives.title,
              item: siteUrl + pages.alternatives.path,
            },
            { '@type': 'ListItem', position: 3, name: pages[key].title, item: url },
          ]
        : key === 'home'
          ? []
          : [{ '@type': 'ListItem', position: 2, name: pages[key].title, item: url }]),
    ],
  });
  return blocks
    .map(
      (b) => `<script type="application/ld+json">${JSON.stringify(b)}</script>`
    )
    .join('\n    ');
}

// ----- Head assembly -----

function headTags(key) {
  const p = pages[key];
  const url = siteUrl + p.path;
  return [
    `<link rel="canonical" href="${esc(url)}" />`,
    `<meta name="robots" content="index, follow" />`,
    `<meta property="og:type" content="website" />`,
    `<meta property="og:site_name" content="${esc(siteName)}" />`,
    `<meta property="og:title" content="${esc(p.title)}" />`,
    `<meta property="og:description" content="${esc(p.description)}" />`,
    `<meta property="og:url" content="${esc(url)}" />`,
    `<meta property="og:image" content="${esc(OG_IMAGE_URL)}" />`,
    `<meta property="og:image:width" content="1200" />`,
    `<meta property="og:image:height" content="630" />`,
    `<meta property="og:image:alt" content="${esc(siteName)}" />`,
    `<meta name="twitter:card" content="summary_large_image" />`,
    `<meta name="twitter:title" content="${esc(p.title)}" />`,
    `<meta name="twitter:description" content="${esc(p.description)}" />`,
    `<meta name="twitter:image" content="${esc(OG_IMAGE_URL)}" />`,
    jsonLd(key, url),
  ].join('\n    ');
}

// Inline dark shell so the crawler-facing content matches theme-color and does
// not flash a bare white page before React mounts and replaces #root.
//
// data-prerender is what index.css keys on to keep this block from painting.
// The marker stays an attribute rather than an inline `display:none` on purpose:
// text extractors ignore an external stylesheet, but some (readability,
// trafilatura) do strip inline-hidden nodes — which would drop the very content
// this prerender exists to serve.
const WRAP_OPEN =
  '<div data-prerender style="max-width:860px;margin:0 auto;padding:64px 24px;min-height:100vh;' +
  'background:#09090b;color:#e4e4e7;font-family:system-ui,-apple-system,sans-serif;line-height:1.6">';
const WRAP_CLOSE = '</div>';

// ----- Render one route -----

const template = readFileSync(join(DIST, 'index.html'), 'utf8');

function render(key) {
  const p = pages[key];
  let html = template;

  html = html.replace(/<title>[^<]*<\/title>/, `<title>${esc(p.title)}</title>`);
  html = html.replace(
    // Tolerates the tag being split across lines: index.html is prettier-formatted,
    // so a single-line pattern silently matched nothing and every prerendered page
    // shipped the landing's description.
    /<meta\s+name="description"[\s\S]*?\/>/,
    `<meta name="description" content="${esc(p.description)}" />`
  );
  html = html.replace('</head>', `    ${headTags(key)}\n  </head>`);
  html = html.replace(
    '<div id="root"></div>',
    `<div id="root">${WRAP_OPEN}${BODIES[key]()}${WRAP_CLOSE}</div>`
  );

  const outDir = key === 'home' ? DIST : join(DIST, p.path);
  mkdirSync(outDir, { recursive: true });
  writeFileSync(join(outDir, 'index.html'), html, 'utf8');
  return join(outDir, 'index.html');
}

// ----- sitemap.xml -----

function sitemap() {
  const today = new Date().toISOString().slice(0, 10);
  const urls = Object.values(pages)
    .map(
      (p) =>
        `  <url><loc>${siteUrl}${p.path}</loc><lastmod>${today}</lastmod>` +
        `<changefreq>weekly</changefreq><priority>${p.path === '/' ? '1.0' : '0.8'}</priority></url>`
    )
    .join('\n');
  const xml = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`;
  writeFileSync(join(DIST, 'sitemap.xml'), xml, 'utf8');
}

// ----- Run -----

if (!template.includes('<div id="root"></div>')) {
  throw new Error('seo-prerender: dist/index.html has no empty #root to inject into. Did vite build run?');
}

const written = Object.keys(pages).map(render);
sitemap();

console.log('[seo-prerender] wrote:');
written.forEach((f) => console.log('  - ' + f.replace(ROOT + '/', '')));
console.log('  - dist/sitemap.xml');
