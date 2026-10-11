// /llms-full.txt: the whole documentation in reading order, as Markdown.
import type { APIRoute } from 'astro';
import { getDocs, docMarkdown, docPath } from '../lib/docs';
import { SITE_URL, SUMMARY } from '../lib/site';

export const GET: APIRoute = async () => {
  const docs = await getDocs();
  const parts = [`# Skillcrew documentation\n\n> ${SUMMARY}\n`];
  for (const d of docs) parts.push(`---\n\nSource: ${SITE_URL}${docPath(d)}\n\n${docMarkdown(d).replace(/^# /, '## ').replace(/\n(#{2,5}) /g, '\n#$1 ')}`);
  return new Response(parts.join('\n'), { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
};
