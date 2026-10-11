import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { z } from 'astro/zod';
import { SECTION_IDS } from './lib/site';

const docs = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/docs' }),
  schema: z.object({
    title: z.string(),
    /** One-sentence summary: shown under the title, used as meta description and in llms.txt. */
    description: z.string().max(200),
    section: z.enum(SECTION_IDS),
    order: z.number().int(),
  }),
});

export const collections = { docs };
