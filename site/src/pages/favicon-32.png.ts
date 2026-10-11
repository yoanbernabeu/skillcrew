// PNG fallback of /favicon.svg.
import type { APIRoute } from 'astro';
import { svgToPng } from '../lib/og';
import { iconSvg } from '../lib/icon';

export const GET: APIRoute = () =>
  new Response(new Uint8Array(svgToPng(iconSvg({ rounded: true }), 32)), { headers: { 'Content-Type': 'image/png' } });
