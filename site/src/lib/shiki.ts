// Syntax highlighting: a Shiki theme in the site colors, and a transformer that
// wraps every block with a header (language + copy button) and marks terminal
// sessions so commands and output can be styled and copied differently.
import type { ShikiTransformer, ThemeRegistration } from 'shiki';
import type { Element, ElementContent } from 'hast';

const C = {
  bg: '#111113',
  text: '#EDEDED',
  muted: '#8B8B93',
  amber: '#F2A541',
  sand: '#D4CBB8',
};

export const theme: ThemeRegistration = {
  name: 'skillcrew-night',
  type: 'dark',
  colors: {
    'editor.background': C.bg,
    'editor.foreground': C.text,
  },
  fg: C.text,
  bg: C.bg,
  tokenColors: [
    { settings: { foreground: C.text } },
    {
      scope: ['comment', 'punctuation.definition.comment'],
      settings: { foreground: C.muted },
    },
    {
      scope: ['punctuation', 'meta.brace', 'punctuation.separator.key-value'],
      settings: { foreground: C.muted },
    },
    {
      scope: [
        'entity.name.tag',
        'entity.name.command',
        'support.function.builtin',
        'support.type.property-name',
        'keyword',
        'storage',
        'markup.heading',
        'entity.name.function',
      ],
      settings: { foreground: C.amber },
    },
    {
      scope: ['string', 'string.unquoted', 'markup.inline.raw'],
      settings: { foreground: C.sand },
    },
    {
      scope: ['constant.numeric', 'constant.language'],
      settings: { foreground: C.amber },
    },
    { scope: ['constant.other.option', 'variable'], settings: { foreground: C.text } },
    // Terminal sessions: the prompt and the program output are dimmed.
    {
      scope: ['punctuation.separator.prompt.shell-session', 'meta.output.shell-session'],
      settings: { foreground: C.muted },
    },
    { scope: ['text.shell-session source.shell'], settings: { foreground: C.text } },
  ],
};

const CONSOLE_LANGS = new Set(['console', 'shellsession', 'shell-session']);

const LANG_LABELS: Record<string, string> = {
  console: 'terminal',
  sh: 'shell',
  bash: 'shell',
  shellscript: 'shell',
  yaml: 'yaml',
  yml: 'yaml',
  json: 'json',
  text: 'text',
  plaintext: 'text',
  md: 'markdown',
  markdown: 'markdown',
};

function el(tagName: string, className: string, children: ElementContent[], props: Element['properties'] = {}): Element {
  return { type: 'element', tagName, properties: { className: [className], ...props }, children };
}

export const codeTransformer: ShikiTransformer = {
  name: 'skillcrew-code',
  line(node, line) {
    if (!CONSOLE_LANGS.has(this.options.lang)) return;
    const text = this.source.split('\n')[line - 1] ?? '';
    this.addClassToHast(node, /^\$( |$)/.test(text) ? 'cmd' : 'out');
  },
  span(node, _line, col, _lineElement, token) {
    if (CONSOLE_LANGS.has(this.options.lang) && col === 0 && token.content === '$') {
      this.addClassToHast(node, 'prompt');
    }
  },
  root(root) {
    const lang = this.options.lang;
    const label = LANG_LABELS[lang] ?? lang;
    const pre = root.children.find((c): c is Element => c.type === 'element' && c.tagName === 'pre');
    if (!pre) return;
    pre.properties['data-lang'] = lang;
    const head = el('div', 'code-head', [
      el('span', 'code-lang', [{ type: 'text', value: label }]),
      el('button', 'copy-btn', [{ type: 'text', value: 'Copy' }], {
        type: 'button',
        'data-copy': CONSOLE_LANGS.has(lang) ? 'commands' : 'all',
        'aria-label': CONSOLE_LANGS.has(lang) ? 'Copy the commands' : 'Copy the code',
      }),
    ]);
    root.children = [el('div', 'code-block', [head, pre])];
  },
};
