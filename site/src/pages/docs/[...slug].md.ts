// Raw Markdown version of every documentation page: /docs/<slug>.md
import type { APIRoute, GetStaticPaths } from 'astro';
import { getDocs, docMarkdown, type Doc } from '../../lib/docs';

export const getStaticPaths = (async () => {
  const docs = await getDocs();
  return docs.map((doc) => ({ params: { slug: doc.id }, props: { doc } }));
}) satisfies GetStaticPaths;

export const GET: APIRoute<{ doc: Doc }> = ({ props }) =>
  new Response(docMarkdown(props.doc), { headers: { 'Content-Type': 'text/markdown; charset=utf-8' } });
