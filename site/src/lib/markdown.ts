// Sätteri (Astro's Markdown processor) plugins for the documentation.
//
// - Callouts: GitHub-style alerts `> [!NOTE]`, `> [!WARNING]` and `> [!SECURITY]`
//   become <div class="callout callout-…" role="note"> with a title, so the Markdown sources
//   stay readable as plain Markdown (and in the .md endpoints).
// - Tables are wrapped in a focusable scroll container for small screens.
// - A table cell that starts with a status name in code (`required`…) gets its
//   instrument light, like the StatusLight component.
import type { HastPluginDefinition } from 'satteri';
import type { Element, ElementContent } from 'hast';
import { lightSvg } from './icon';
import type { Status } from './statuses';

const STATUS_NAMES = new Set<string>(['required', 'recommended', 'available', 'deprecated', 'blocked']);

const KINDS: Record<string, string> = { NOTE: 'Note', WARNING: 'Warning', SECURITY: 'Security' };
const MARKER = /^\s*\[!(NOTE|WARNING|SECURITY)\]\s*/;

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function isEmpty(node: ElementContent): boolean {
  return node.type === 'text' && node.value.trim() === '';
}

export const calloutsPlugin: HastPluginDefinition = {
  name: 'skillcrew-callouts',
  element: [
    {
      filter: ['blockquote'],
      visit(node, ctx) {
        const match = ctx.textContent(node).match(MARKER);
        if (!match?.[1]) return;
        const kind = match[1];
        const children = clone(node.children as ElementContent[]).filter((c) => !isEmpty(c));
        const first = children[0];
        if (first?.type === 'element' && first.tagName === 'p') {
          const text = first.children[0];
          if (text?.type === 'text') text.value = text.value.replace(MARKER, '');
          first.children = first.children.filter((c, i) => !(i === 0 && isEmpty(c)));
          if (first.children.length === 0) children.shift();
        }
        const title: Element = {
          type: 'element',
          tagName: 'p',
          properties: { className: ['callout-title'] },
          children: [{ type: 'text', value: KINDS[kind] ?? kind }],
        };
        ctx.replaceNode(node, {
          type: 'element',
          tagName: 'div',
          properties: { className: ['callout', `callout-${kind.toLowerCase()}`], role: 'note' },
          children: [title, ...children],
        });
      },
    },
    {
      filter: ['td'],
      visit(node, ctx) {
        const first = node.children.find((c) => !isEmpty(c as ElementContent)) as ElementContent | undefined;
        if (first?.type !== 'element' || first.tagName !== 'code') return;
        const text = ctx.textContent(first);
        if (!STATUS_NAMES.has(text)) return;
        ctx.setProperty(node, 'className', ['status-cell']);
        ctx.prependChild(node, { type: 'raw', value: lightSvg(text as Status) });
      },
    },
    {
      filter: ['table'],
      visit(node, ctx) {
        ctx.wrapNode(node, {
          type: 'element',
          tagName: 'div',
          properties: { className: ['table-wrap'] },
          children: [],
        });
      },
    },
  ],
};
