// Site-wide constants shared by pages, layouts and generated files.

export const SITE_URL = 'https://skillcrew.yoandev.co';
export const SITE_NAME = 'Skillcrew';
export const REPO_URL = 'https://github.com/yoanbernabeu/skillcrew';
export const INSTALL_COMMAND = 'brew install yoanbernabeu/tap/skillcrew';

export const SUMMARY =
  'Skillcrew distributes Agent Skills from one Git repository to Claude Code, Codex, Cursor, Copilot and Gemini CLI, and keeps them up to date with no action from developers.';

/** Documentation sections, in reading order. Keys are the folder names under src/content/docs. */
export const SECTION_IDS = ['getting-started', 'concepts', 'guides', 'reference', 'troubleshooting'] as const;
export type SectionId = (typeof SECTION_IDS)[number];

export const SECTIONS: Record<SectionId, string> = {
  'getting-started': 'Getting started',
  concepts: 'Concepts',
  guides: 'Guides',
  reference: 'Reference',
  troubleshooting: 'Troubleshooting',
};

/** Absolute URL for a site path. */
export function absolute(path: string): string {
  return new URL(path, SITE_URL).toString();
}

/** Source file of a doc page in the repository, for "Edit this page". */
export function editUrl(id: string): string {
  return `${REPO_URL}/edit/main/site/src/content/docs/${id}.md`;
}
