// Home screen icon (iOS rounds the corners itself).
import type { APIRoute } from 'astro';
import { svgToPng } from '../lib/og';
import { iconSvg } from '../lib/icon';

export const GET: APIRoute = () =>
  new Response(new Uint8Array(svgToPng(iconSvg({ rounded: false }), 180)), { headers: { 'Content-Type': 'image/png' } });
