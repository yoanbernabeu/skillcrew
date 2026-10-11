// Open Graph image of every page: /og/index.png, /og/docs.png, /og/docs/<slug>.png
import type { APIRoute, GetStaticPaths } from 'astro';
import { getDocs, sectionTitle } from '../../lib/docs';
import { docImage, landingImage } from '../../lib/og';

type Props = { kind: 'landing' } | { kind: 'doc'; section: string[]; title: string; summary: string };

export const getStaticPaths = (async () => {
  const docs = await getDocs();
  return [
    { params: { slug: 'index' }, props: { kind: 'landing' } satisfies Props },
    {
      params: { slug: 'docs' },
      props: {
        kind: 'doc',
        section: [],
        title: 'Documentation',
        summary: "Install Skillcrew, publish your team's skills from one registry, and keep every developer's agents in sync.",
      } satisfies Props,
    },
    ...docs.map((d) => ({
      params: { slug: `docs/${d.id}` },
      props: { kind: 'doc', section: [sectionTitle(d.data.section)], title: d.data.title, summary: d.data.description } satisfies Props,
    })),
  ];
}) satisfies GetStaticPaths;

export const GET: APIRoute<Props> = async ({ props }) => {
  const png = props.kind === 'landing' ? await landingImage() : await docImage(props);
  return new Response(new Uint8Array(png), { headers: { 'Content-Type': 'image/png' } });
};
