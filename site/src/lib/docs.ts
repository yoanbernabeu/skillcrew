// Documentation entries in reading order, and helpers shared by pages and
// generated files (Markdown endpoints, llms.txt, OG images).
import { getCollection, type CollectionEntry } from 'astro:content';
import { SECTION_IDS, SECTIONS, SITE_URL, type SectionId } from './site';

export type Doc = CollectionEntry<'docs'>;

export async function getDocs(): Promise<Doc[]> {
  const docs = await getCollection('docs');
  return docs.sort(
    (a, b) => SECTION_IDS.indexOf(a.data.section) - SECTION_IDS.indexOf(b.data.section) || a.data.order - b.data.order,
  );
}

export function docPath(doc: Doc): string {
  return `/docs/${doc.id}/`;
}

export function docMarkdownPath(doc: Doc): string {
  return `/docs/${doc.id}.md`;
}

export function docOgPath(doc: Doc): string {
  return `/og/docs/${doc.id}.png`;
}

export function sectionTitle(id: SectionId): string {
  return SECTIONS[id];
}

export function groupBySection(docs: Doc[]): { id: SectionId; title: string; docs: Doc[] }[] {
  return SECTION_IDS.map((id) => ({ id, title: SECTIONS[id], docs: docs.filter((d) => d.data.section === id) })).filter(
    (g) => g.docs.length > 0,
  );
}

/** Root-relative links in Markdown become absolute, so the Markdown works outside the site. */
export function absolutizeLinks(markdown: string): string {
  return markdown.replace(/\]\((\/[^)\s]*)\)/g, (_m, path: string) => `](${SITE_URL}${path})`);
}

/** The Markdown version of a page: title, summary, body. */
export function docMarkdown(doc: Doc): string {
  const body = (doc.body ?? '').trim();
  return `# ${doc.data.title}\n\n> ${doc.data.description}\n\n${absolutizeLinks(body)}\n`;
}
