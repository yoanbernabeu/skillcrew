// @ts-check
import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';
import { satteri } from '@astrojs/markdown-satteri';
import { theme, codeTransformer } from './src/lib/shiki.ts';
import { calloutsPlugin } from './src/lib/markdown.ts';

export default defineConfig({
  site: 'https://skillcrew.yoandev.co',
  trailingSlash: 'ignore',
  build: { format: 'directory' },
  integrations: [sitemap()],
  markdown: {
    processor: satteri({ hastPlugins: [calloutsPlugin] }),
    shikiConfig: { theme, transformers: [codeTransformer] },
  },
  vite: {
    plugins: [tailwindcss()],
    ssr: { external: ['@resvg/resvg-js', 'satori'] },
  },
});
