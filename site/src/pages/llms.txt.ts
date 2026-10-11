// /llms.txt (https://llmstxt.org): what Skillcrew is, and links to the Markdown docs.
import type { APIRoute } from 'astro';
import { getDocs, groupBySection, docMarkdownPath } from '../lib/docs';
import { SITE_URL, SUMMARY, REPO_URL } from '../lib/site';

export const GET: APIRoute = async () => {
  const groups = groupBySection(await getDocs());
  const lines = [
    '# Skillcrew',
    '',
    `> ${SUMMARY}`,
    '',
    'Skillcrew is an open source Go CLI (MIT, macOS and Linux). An organization keeps its Agent Skills (SKILL.md) in one Git repository, the registry: a Claude Code marketplace plus a skillcrew.yaml governance file. Each skill is required, recommended, available, deprecated or blocked, per organization and per team. On each developer machine, `skillcrew init` installs session-start hooks in the agents and an hourly timer that run `skillcrew sync`: skills are copied to ~/.agents/skills and symlinked from ~/.claude/skills.',
    '',
    `Source code: ${REPO_URL}`,
    '',
  ];
  for (const g of groups) {
    lines.push(`## ${g.title}`, '');
    for (const d of g.docs) lines.push(`- [${d.data.title}](${SITE_URL}${docMarkdownPath(d)}): ${d.data.description}`);
    lines.push('');
  }
  lines.push('## Optional', '', `- [Full documentation](${SITE_URL}/llms-full.txt): every page above in one file`, '');
  return new Response(lines.join('\n'), { headers: { 'Content-Type': 'text/plain; charset=utf-8' } });
};
