// The site icon: one amber indicator light on an anthracite square.
// Keep in sync with public/favicon.svg.
export function iconSvg({ rounded }: { rounded: boolean }): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="${rounded ? 10 : 0}" fill="#0A0A0B"/><circle cx="32" cy="32" r="14" fill="#F2A541"/></svg>`;
}

/** Inline SVG of a status light for HTML pages (colors from CSS variables). Used by StatusLight.astro, Markdown tables and OG images. */
export function lightSvg(status: 'required' | 'recommended' | 'available' | 'deprecated' | 'blocked', className = 'light'): string {
  const shapes = {
    required: '<circle cx="6" cy="6" r="4.5" fill="var(--amber)"/>',
    recommended: '<circle cx="6" cy="6" r="4" fill="none" stroke="var(--amber)" stroke-width="1.5"/>',
    available: '<circle cx="6" cy="6" r="4" fill="none" stroke="var(--muted)" stroke-width="1.5"/>',
    deprecated:
      '<circle cx="6" cy="6" r="4" fill="var(--amber)"/><line x1="0.75" y1="11.25" x2="11.25" y2="0.75" stroke="var(--bg)" stroke-width="3"/><line x1="0.75" y1="11.25" x2="11.25" y2="0.75" stroke="var(--text)" stroke-width="1.2"/>',
    blocked: '<rect x="2" y="2" width="8" height="8" fill="var(--red)"/>',
  };
  return `<svg class="${className}" viewBox="0 0 12 12" aria-hidden="true" focusable="false">${shapes[status]}</svg>`;
}
