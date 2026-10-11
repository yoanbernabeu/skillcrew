// Open Graph images (1200×630), rendered at build time with Satori and resvg.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import satori from 'satori';
import { Resvg } from '@resvg/resvg-js';
import { lightSvg } from './icon';
import type { Status } from './statuses';

const C = {
  bg: '#0A0A0B',
  surface: '#111113',
  line: '#1F1F23',
  text: '#EDEDED',
  muted: '#8B8B93',
  amber: '#F2A541',
  red: '#C8553D',
};

type Style = Record<string, string | number>;
interface Node {
  type: string;
  props: { style?: Style; children?: Child | Child[]; src?: string; width?: number; height?: number };
}
type Child = Node | string;

function h(type: string, style: Style, ...children: Child[]): Node {
  return { type, props: { style: { display: 'flex', ...style }, children } };
}
function img(src: string, width: number, height: number, style: Style = {}): Node {
  return { type: 'img', props: { src, width, height, style } };
}
function svgUri(svg: string): string {
  return `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`;
}

function ogLightSvg(status: Status): string {
  const vars: Record<string, string> = { amber: C.amber, muted: C.muted, bg: C.bg, text: C.text, red: C.red };
  return lightSvg(status)
    .replace('<svg ', '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" ')
    .replace(/var\(--(\w+)\)/g, (_m, name: string) => vars[name] ?? C.text);
}
function light(status: Status, size: number): Node {
  return img(svgUri(ogLightSvg(status)), size, size);
}

/** The faint 32px grid of the site, faded toward the bottom and the edges. */
function grid(): string {
  const lines: string[] = [];
  for (let x = 0; x <= 1200; x += 32) lines.push(`M${x + 0.5} 0V630`);
  for (let y = 0; y <= 630; y += 32) lines.push(`M0 ${y + 0.5}H1200`);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630" width="1200" height="630">
<defs><radialGradient id="f" cx="0.5" cy="0" r="0.75"><stop offset="0.2" stop-color="#fff"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient>
<mask id="m"><rect width="1200" height="630" fill="url(#f)"/></mask></defs>
<path d="${lines.join('')}" stroke="${C.text}" stroke-opacity="0.07" fill="none" mask="url(#m)"/></svg>`;
  return `url(${svgUri(svg)})`;
}

let fonts: Promise<{ name: string; data: Buffer; weight: 400 | 500 | 700; style: 'normal' }[]> | undefined;
function loadFonts() {
  const file = (pkg: string, name: string) => readFile(join(process.cwd(), 'node_modules', '@fontsource', pkg, 'files', name));
  const face = (name: string, weight: 400 | 500 | 700) => (data: Buffer) => ({ name, data, weight, style: 'normal' as const });
  fonts ??= Promise.all([
    file('geist-sans', 'geist-sans-latin-400-normal.woff').then(face('Geist', 400)),
    file('geist-sans', 'geist-sans-latin-700-normal.woff').then(face('Geist', 700)),
    file('geist-mono', 'geist-mono-latin-400-normal.woff').then(face('Geist Mono', 400)),
    file('geist-mono', 'geist-mono-latin-500-normal.woff').then(face('Geist Mono', 500)),
  ]);
  return fonts;
}

const mono = (size: number, color: string, extra: Style = {}): Style => ({ fontFamily: 'Geist Mono', fontSize: size, color, ...extra });
const sans = (size: number, color: string, extra: Style = {}): Style => ({
  fontFamily: 'Geist',
  fontWeight: 700,
  fontSize: size,
  letterSpacing: -size * 0.035,
  lineHeight: 1.04,
  color,
  ...extra,
});

async function render(tree: Node): Promise<Buffer> {
  const svg = await satori(tree as unknown as Parameters<typeof satori>[0], { width: 1200, height: 630, fonts: await loadFonts() });
  return new Resvg(svg, { fitTo: { mode: 'width', value: 1200 } }).render().asPng();
}

function frame(...children: Child[]): Node {
  return h(
    'div',
    { width: 1200, height: 630, backgroundColor: C.bg, backgroundImage: grid(), backgroundSize: '1200px 630px' },
    h('div', { flexDirection: 'column', justifyContent: 'space-between', width: 1056, height: 518, marginTop: 64, marginLeft: 72 }, ...children),
  );
}

export function docImage({ section, title, summary }: { section: string[]; title: string; summary: string }): Promise<Buffer> {
  const size = title.length > 30 ? 64 : 76;
  return render(
    frame(
      h(
        'div',
        { alignItems: 'center', gap: 14 },
        light('required', 16),
        h('div', mono(22, C.text, { letterSpacing: 1 }), 'skillcrew'),
        ...['docs', ...section].map((p) => h('div', mono(22, C.muted), `/ ${p.toLowerCase()}`)),
      ),
      h(
        'div',
        { flexDirection: 'column', gap: 24, maxWidth: 1000 },
        h('div', sans(size, C.text), title),
        h('div', { fontFamily: 'Geist', fontSize: 30, lineHeight: 1.4, color: C.muted }, summary),
      ),
      h('div', { justifyContent: 'flex-end' }, h('div', mono(22, C.amber), 'skillcrew.yoandev.co')),
    ),
  );
}

export function landingImage(): Promise<Buffer> {
  const agents = ['Claude Code', 'Codex', 'Cursor', 'Copilot', 'Gemini CLI'];
  const panel = h(
    'div',
    { flexDirection: 'column', width: 400, border: `1px solid ${C.line}`, borderRadius: 8, backgroundColor: C.surface },
    h(
      'div',
      { alignItems: 'center', gap: 14, height: 44, padding: '0 18px', borderBottom: `1px solid ${C.line}`, backgroundColor: C.bg, borderRadius: '8px 8px 0 0' },
      h('div', { gap: 7 }, ...[0, 1, 2].map(() => h('div', { width: 11, height: 11, borderRadius: 6, backgroundColor: C.line }))),
      h('div', mono(15, C.muted), 'acme/skills · main · just now'),
    ),
    h(
      'div',
      { alignItems: 'center', gap: 16, height: 50, padding: '0 22px', borderBottom: `1px solid ${C.line}` },
      h('div', mono(13, C.muted, { letterSpacing: 1 }), 'SKILL'),
      h('div', mono(17, C.text), 'security:review'),
      h('div', mono(17, C.muted), '· v2'),
    ),
    ...agents.map((a, i) =>
      h(
        'div',
        { justifyContent: 'space-between', alignItems: 'center', height: 46, padding: '0 22px', ...(i > 0 ? { borderTop: `1px solid ${C.line}` } : {}) },
        h('div', mono(17, C.text), a),
        h('div', { alignItems: 'center', gap: 10 }, light('required', 13), h('div', mono(16, C.text), 'synced')),
      ),
    ),
  );
  return render(
    h(
      'div',
      { width: 1200, height: 630, backgroundColor: C.bg, backgroundImage: grid(), backgroundSize: '1200px 630px', alignItems: 'center' },
      h(
        'div',
        { flexDirection: 'column', width: 620, marginLeft: 72 },
        h('div', sans(66, C.text), "Your team's"),
        h('div', sans(66, C.text), 'AI skills.'),
        h('div', sans(66, C.text), 'Every agent.'),
        h('div', sans(66, C.amber), 'Always in sync.'),
        h('div', mono(22, C.amber, { marginTop: 40 }), 'skillcrew.yoandev.co'),
      ),
      h('div', { marginLeft: 36 }, panel),
    ),
  );
}

/** Render a standalone SVG (favicon) to PNG. */
export function svgToPng(svg: string, size: number): Buffer {
  return new Resvg(svg, { fitTo: { mode: 'width', value: size } }).render().asPng();
}
