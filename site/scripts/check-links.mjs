// Checks every internal link of the built site (dist/): href and src
// attributes in HTML files, and Markdown links in .md and .txt files that
// point to this site. A link must resolve to a built file, and a #fragment to
// an element id in the target page. Usage: node scripts/check-links.mjs [dir]
import { readFile, readdir, stat } from 'node:fs/promises';
import { join, relative, resolve, dirname, posix } from 'node:path';

const SITE = 'https://skillcrew.yoandev.co';
const root = resolve(process.argv[2] ?? 'dist');

async function walk(dir) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...(await walk(path)));
    else out.push(path);
  }
  return out;
}

async function isFile(path) {
  try {
    return (await stat(path)).isFile();
  } catch {
    return false;
  }
}

/** Resolve a site path to a file in dist, or null. */
async function target(pathname) {
  const p = join(root, decodeURIComponent(pathname));
  if (pathname.endsWith('/')) return (await isFile(join(p, 'index.html'))) ? join(p, 'index.html') : null;
  if (await isFile(p)) return p;
  if (await isFile(join(p, 'index.html'))) return join(p, 'index.html');
  return null;
}

const idCache = new Map();
async function ids(file) {
  if (!idCache.has(file)) {
    const html = await readFile(file, 'utf8');
    idCache.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
  }
  return idCache.get(file);
}

const files = await walk(root);
const errors = [];
let checked = 0;

for (const file of files) {
  const rel = '/' + relative(root, file).split('\\').join('/');
  let links = [];
  if (file.endsWith('.html')) {
    const html = await readFile(file, 'utf8');
    links = [...html.matchAll(/\s(?:href|src)="([^"]*)"/g)].map((m) => m[1].replaceAll('&amp;', '&'));
  } else if (file.endsWith('.md') || file.endsWith('.txt')) {
    const text = await readFile(file, 'utf8');
    links = [...text.matchAll(/\]\((https:\/\/skillcrew\.yoandev\.co[^)\s]*)\)/g)].map((m) => m[1]);
  } else continue;

  for (const raw of links) {
    let link = raw;
    if (link.startsWith(SITE)) link = link.slice(SITE.length) || '/';
    if (!link || /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(link)) continue; // external, mailto:, data:
    checked++;
    const base = rel.endsWith('/index.html') || rel.endsWith('.html') ? posix.dirname(rel) + '/' : rel;
    const url = new URL(link, `http://site${base}`);
    const page = link.startsWith('#') ? file : await target(url.pathname);
    if (!page) {
      errors.push(`${rel}: broken link ${raw}`);
      continue;
    }
    const fragment = decodeURIComponent(url.hash.slice(1));
    if (fragment && page.endsWith('.html') && !(await ids(page)).has(fragment)) {
      errors.push(`${rel}: missing anchor ${raw}`);
    }
  }
}

if (errors.length) {
  console.error(errors.join('\n'));
  console.error(`\n${errors.length} broken link(s) out of ${checked} checked.`);
  process.exit(1);
}
console.log(`All ${checked} internal links resolve (${files.length} files in ${relative(dirname(root), root)}/).`);
