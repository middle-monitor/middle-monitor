// Build-time machine-readable documentation.
//
// /docs is a client-rendered React view: `curl` returns the SPA shell, so an
// agent or a crawler reading the docs sees nothing. The SEO prerender fixes the
// HTML; this script produces the plain-text form the same content is asked for
// by tooling:
//
//   dist/llms.txt        index of the documentation (llmstxt.org convention)
//   dist/llms-full.txt   the whole documentation, concatenated
//   dist/docs/<id>.md    one markdown file per documentation section
//
// The source is the view itself (src/views/DocumentationView.tsx), so a section
// added to the page appears here without anyone remembering to update a copy.

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.cwd(); // frontend/
const DIST = join(ROOT, 'dist');

const site = JSON.parse(readFileSync(join(ROOT, 'src/seo/pages.json'), 'utf8'));
const en = JSON.parse(readFileSync(join(ROOT, 'src/locales/en.json'), 'utf8'));
const sections = JSON.parse(
  readFileSync(join(ROOT, 'src/seo/docs-sections.json'), 'utf8')
);
const view = readFileSync(join(ROOT, 'src/views/DocumentationView.tsx'), 'utf8');

const { siteUrl } = site;
const API_URL = 'https://api.middlemonitor.io';
const sectionIds = new Set(sections.map((s) => s.id));

// ----- Reading the view -----

// translate resolves {t('docs.key')} against the same locale file the page
// renders, so the text below is what a visitor actually reads.
function translate(key) {
  return key.split('.').reduce((node, part) => (node == null ? undefined : node[part]), en) ?? '';
}

// cleanText turns a run of JSX into a line of prose: tags out, entities and
// t() calls resolved, whitespace collapsed.
function cleanText(raw) {
  return raw
    .replace(/\{'\s*'\}/g, ' ')
    .replace(/\{t\('([^']+)'\)\}/g, (_, key) => translate(key))
    .replace(/\{`([^`]*)`\}/g, '$1')
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, '')
    .replace(/<[^>]*>/g, ' ')
    .replace(/\{[^{}]*\}/g, ' ')
    .replace(/&nbsp;/g, ' ')
    .replace(/&rarr;/g, '->')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&#123;/g, '{')
    .replace(/&#125;/g, '}')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/\s+/g, ' ')
    .trim();
}

// blockAt returns the raw source of one element starting at `from`, matching
// its closing tag by depth so nested markup comes along.
function blockAt(source, from, tag) {
  const open = new RegExp(`<${tag}(\\s|>)`, 'g');
  const close = new RegExp(`</${tag}>`, 'g');
  let depth = 0;
  let index = from;
  while (index < source.length) {
    open.lastIndex = index;
    close.lastIndex = index;
    const nextOpen = open.exec(source);
    const nextClose = close.exec(source);
    if (!nextClose) return { text: '', end: source.length };
    if (nextOpen && nextOpen.index < nextClose.index) {
      depth += 1;
      index = nextOpen.index + 1;
      continue;
    }
    depth -= 1;
    if (depth === 0) {
      return { text: source.slice(from, nextClose.index), end: close.lastIndex };
    }
    index = nextClose.index + 1;
  }
  return { text: '', end: source.length };
}

// codeBlockAt reads the template literal of a <CodeBlock code={`...`} />.
function codeBlockAt(source, from) {
  const languageMatch = /language='([^']*)'/.exec(source.slice(from, from + 200));
  const start = source.indexOf('code={`', from);
  if (start === -1) return null;
  const end = source.indexOf('`}', start + 7);
  if (end === -1) return null;
  const code = source
    .slice(start + 7, end)
    .replace(/\$\{API_URL\}/g, API_URL)
    // The source is a template literal: \$ and \` are escapes the browser
    // resolves, so the extracted text has to resolve them too.
    .replace(/\\\$/g, '$')
    .replace(/\\\\/g, '\\')
    .replace(/\\`/g, '`');
  return { language: languageMatch ? languageMatch[1] : '', code, end: end + 2 };
}

// extract walks the view once and collects the markdown of every section. The
// view is regular enough for this: each documented block is a
// <div id='<section>' className='doc-subsection'>.
function extract() {
  const collected = new Map();
  let current = null;
  let index = 0;

  const push = (line) => {
    if (!current) return;
    const lines = collected.get(current);
    if (line === '' && lines[lines.length - 1] === '') return;
    lines.push(line);
  };

  while (index < view.length) {
    const idMatch = /id='([a-z0-9-]+)'/g;
    idMatch.lastIndex = index;

    const nextSection = idMatch.exec(view);
    const nextHeading = /<(h2|h3|h4)>/g;
    nextHeading.lastIndex = index;
    const heading = nextHeading.exec(view);
    const nextCode = view.indexOf('<CodeBlock', index);
    const nextPara = /<(p|li)[\s>]/g;
    nextPara.lastIndex = index;
    const para = nextPara.exec(view);

    const candidates = [
      nextSection && sectionIds.has(nextSection[1]) ? { kind: 'section', at: nextSection.index, m: nextSection } : null,
      heading ? { kind: 'heading', at: heading.index, m: heading } : null,
      nextCode !== -1 ? { kind: 'code', at: nextCode } : null,
      (() => {
        const row = /<tr>/g;
        row.lastIndex = index;
        const found = row.exec(view);
        return found ? { kind: 'row', at: found.index } : null;
      })(),
      para ? { kind: 'para', at: para.index, m: para } : null,
    ].filter(Boolean);

    if (candidates.length === 0) break;
    candidates.sort((a, b) => a.at - b.at);
    const next = candidates[0];

    if (next.kind === 'section') {
      current = next.m[1];
      if (!collected.has(current)) collected.set(current, []);
      index = next.at + next.m[0].length;
      continue;
    }

    if (next.kind === 'heading') {
      const tag = next.m[1];
      const { text, end } = blockAt(view, next.at, tag);
      const title = cleanText(text);
      if (title) {
        push('');
        push(`${tag === 'h2' ? '##' : '###'} ${title}`);
        push('');
      }
      index = end;
      continue;
    }

    if (next.kind === 'code') {
      const block = codeBlockAt(view, next.at);
      if (!block) {
        index = next.at + 10;
        continue;
      }
      push('');
      push('```' + block.language);
      block.code.split('\n').forEach(push);
      push('```');
      push('');
      index = block.end;
      continue;
    }

    if (next.kind === 'row') {
      const { text, end } = blockAt(view, next.at, 'tr');
      const cells = [];
      let header = false;
      const cellRe = /<(td|th)[\s>]/g;
      let cursor = 0;
      while (true) {
        cellRe.lastIndex = cursor;
        const cell = cellRe.exec(text);
        if (!cell) break;
        if (cell[1] === 'th') header = true;
        const inner = blockAt(text, cell.index, cell[1]);
        cells.push(cleanText(inner.text).replace(/\|/g, '\\|'));
        cursor = inner.end;
      }
      if (cells.length > 0) {
        push(`| ${cells.join(' | ')} |`);
        if (header) push(`|${cells.map(() => ' --- ').join('|')}|`);
      }
      index = end;
      continue;
    }

    const tag = next.m[1];
    const { text, end } = blockAt(view, next.at, tag);
    const line = cleanText(text);
    if (line) {
      push(tag === 'li' ? `- ${line}` : line);
      if (tag === 'p') push('');
    }
    index = end;
  }

  return collected;
}

// ----- Writing the artifacts -----

const bodies = extract();

function sectionMarkdown(section) {
  const lines = bodies.get(section.id) ?? [];
  const body = lines.join('\n').replace(/\n{3,}/g, '\n\n').trim();
  return [
    `# ${section.title}`,
    '',
    `> ${section.excerpt}`,
    '',
    section.summary,
    '',
    body,
    '',
    `Source: ${siteUrl}/docs#${section.id}`,
    '',
  ].join('\n');
}

mkdirSync(join(DIST, 'docs'), { recursive: true });
for (const section of sections) {
  writeFileSync(join(DIST, 'docs', `${section.id}.md`), sectionMarkdown(section), 'utf8');
}

const categories = [...new Set(sections.map((s) => s.category))];
const index = [
  '# Middle Monitor',
  '',
  `> ${site.pages.docs.description}`,
  '',
  'Middle Monitor is an observability platform: infrastructure metrics from a single',
  'binary agent, uptime checks run from the platform, OpenTelemetry traces, logs,',
  'errors and continuous profiling, with rule-based alerting on top.',
  '',
  ...categories.flatMap((category) => [
    `## ${category}`,
    '',
    ...sections
      .filter((s) => s.category === category)
      .map((s) => `- [${s.title}](${siteUrl}/docs/${s.id}.md): ${s.excerpt}`),
    '',
  ]),
  '## Optional',
  '',
  `- [Full documentation](${siteUrl}/llms-full.txt): every section concatenated`,
  `- [OpenAPI specification](${siteUrl}/openapi.json): the REST API, machine-readable`,
  `- [Webhook payload schema](${siteUrl}/schemas/webhook-payload.json): the structured notification payload`,
  `- [Agent configuration schema](${siteUrl}/schemas/agent-config.json): config.yaml, for editor validation`,
  `- [Pricing](${siteUrl}/pricing)`,
  '',
].join('\n');

writeFileSync(join(DIST, 'llms.txt'), index, 'utf8');

const full = [
  '# Middle Monitor — full documentation',
  '',
  `Generated from ${siteUrl}/docs. One section per heading below.`,
  '',
  ...sections.map(sectionMarkdown),
].join('\n---\n\n');

writeFileSync(join(DIST, 'llms-full.txt'), full, 'utf8');

console.log('[docs-artifacts] wrote:');
console.log('  - dist/llms.txt');
console.log(`  - dist/llms-full.txt (${(full.length / 1024).toFixed(1)} KB)`);
console.log(`  - dist/docs/*.md (${sections.length} files)`);
