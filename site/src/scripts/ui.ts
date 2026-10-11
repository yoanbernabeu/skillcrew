// The only client-side behavior of the site: copy buttons and the search dialog.
export {};

// ---------------------------------------------------------------- copy

function announce(message: string): void {
  const status = document.getElementById('copy-status');
  if (status) {
    status.textContent = '';
    window.setTimeout(() => (status.textContent = message), 50);
  }
}

function done(button: HTMLButtonElement, ok: boolean): void {
  const label = button.dataset['label'] ?? button.textContent ?? 'Copy';
  button.dataset['label'] = label;
  button.textContent = ok ? 'Copied' : 'Failed';
  button.dataset['state'] = 'done';
  announce(ok ? 'Copied to the clipboard' : 'Copy failed');
  window.setTimeout(() => {
    button.textContent = label;
    delete button.dataset['state'];
  }, 1600);
}

function codeText(button: HTMLButtonElement): string {
  const pre = button.closest('.code-block')?.querySelector('pre');
  if (!pre) return '';
  if (button.dataset['copy'] === 'commands') {
    return Array.from(pre.querySelectorAll('.line.cmd'))
      .map((line) => (line.textContent ?? '').replace(/^\$ ?/, ''))
      .join('\n');
  }
  return Array.from(pre.querySelectorAll('.line'))
    .map((line) => line.textContent ?? '')
    .join('\n');
}

async function copyMarkdown(url: string): Promise<void> {
  // Safari needs the clipboard write to start inside the click: pass a promise.
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    const blob = (async () => {
      const r = await fetch(url);
      if (!r.ok) throw new Error(String(r.status));
      return new Blob([await r.text()], { type: 'text/plain' });
    })();
    await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]);
    return;
  }
  const text = await fetch(url).then((r) => r.text());
  await navigator.clipboard.writeText(text);
}

document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest<HTMLButtonElement>('button[data-copy], button[data-copy-text], button[data-copy-md]');
  if (!button) return;
  let job: Promise<void>;
  if (button.dataset['copyMd']) job = copyMarkdown(button.dataset['copyMd']);
  else job = navigator.clipboard.writeText(button.dataset['copyText'] ?? codeText(button));
  job.then(
    () => done(button, true),
    () => done(button, false),
  );
});

// ---------------------------------------------------------------- search

interface PagefindResultData {
  url: string;
  excerpt: string;
  meta: { title?: string; section?: string };
}
interface Pagefind {
  options(o: Record<string, unknown>): Promise<void>;
  debouncedSearch(q: string, o?: unknown, ms?: number): Promise<{ results: { data(): Promise<PagefindResultData> }[] } | null>;
}

const dialog = document.getElementById('search') as HTMLDialogElement | null;
const input = document.getElementById('search-input') as HTMLInputElement | null;
const list = document.getElementById('search-results');
const status = document.getElementById('search-status');
let pagefind: Promise<Pagefind | null> | undefined;

function loadPagefind(): Promise<Pagefind | null> {
  pagefind ??= (async () => {
    try {
      const url = '/pagefind/pagefind.js';
      const pf = (await import(/* @vite-ignore */ url)) as Pagefind;
      await pf.options({ excerptLength: 22 });
      return pf;
    } catch {
      return null;
    }
  })();
  return pagefind;
}

function openSearch(): void {
  if (!dialog || dialog.open) return;
  dialog.showModal();
  input?.select();
  void loadPagefind();
}

async function runSearch(query: string): Promise<void> {
  if (!list || !status) return;
  const q = query.trim();
  if (!q) {
    list.replaceChildren();
    status.textContent = 'Type to search the documentation.';
    return;
  }
  const pf = await loadPagefind();
  if (!pf) {
    status.textContent = 'Search is available on the built site (npm run build, then npm run preview).';
    return;
  }
  const search = await pf.debouncedSearch(q, {}, 150);
  if (!search) return; // superseded by a newer query
  const results = await Promise.all(search.results.slice(0, 8).map((r) => r.data()));
  list.replaceChildren(
    ...results.map((r) => {
      const li = document.createElement('li');
      const a = document.createElement('a');
      a.href = r.url;
      const title = document.createElement('span');
      title.className = 'r-title';
      title.textContent = r.meta.title ?? r.url;
      const path = document.createElement('span');
      path.className = 'r-path';
      path.textContent = r.meta.section ?? r.url;
      const excerpt = document.createElement('span');
      excerpt.className = 'r-excerpt';
      excerpt.innerHTML = r.excerpt; // Pagefind output: our own content, with <mark> around matches
      a.append(title, path, excerpt);
      li.append(a);
      return li;
    }),
  );
  status.textContent = results.length === 0 ? `No results for “${q}”.` : `${search.results.length} result${search.results.length > 1 ? 's' : ''}`;
}

if (dialog && input && list) {
  const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  document.querySelectorAll('[data-search-kbd]').forEach((k) => (k.textContent = isMac ? '⌘K' : 'Ctrl K'));
  document.querySelectorAll('[data-search-open]').forEach((b) => b.addEventListener('click', openSearch));
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      if (dialog.open) dialog.close();
      else openSearch();
    } else if (e.key === '/' && !dialog.open && !(e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement)) {
      e.preventDefault();
      openSearch();
    }
  });
  input.addEventListener('input', () => void runSearch(input.value));
  dialog.addEventListener('click', (e) => {
    if (e.target === dialog) dialog.close(); // click on the backdrop
  });
  dialog.addEventListener('keydown', (e) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const links = Array.from(list.querySelectorAll('a'));
    if (links.length === 0) return;
    e.preventDefault();
    const i = links.indexOf(document.activeElement as HTMLAnchorElement);
    const next = e.key === 'ArrowDown' ? (i + 1) % links.length : i <= 0 ? -1 : i - 1;
    if (next === -1) input.focus();
    else links[next]?.focus();
  });
}
