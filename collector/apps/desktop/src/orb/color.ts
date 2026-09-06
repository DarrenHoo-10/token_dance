import type { QuotaState } from './types';

export interface QuotaColorStop {
  remaining: number;
  hex: string;
}

export interface QuotaVisual {
  color: string;
  arcDegrees: number;
  remaining: number;
}

// Adjacent OKLab stops from the v2 spec. Never RGB-lerp these; green→red must not pass through blue.
export const QUOTA_COLOR_STOPS: readonly QuotaColorStop[] = [
  { remaining: 100, hex: '#24C88A' },
  { remaining: 85, hex: '#48CC68' },
  { remaining: 70, hex: '#88CE46' },
  { remaining: 55, hex: '#CBD33C' },
  { remaining: 40, hex: '#F0C443' },
  { remaining: 25, hex: '#F5A03C' },
  { remaining: 15, hex: '#F47B3D' },
  { remaining: 5, hex: '#ED5546' },
  { remaining: 0, hex: '#DE3D4B' },
];

export const NEUTRAL_QUOTA_COLOR = 'rgb(138, 144, 152)';

export function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

export function hexToRgb(hex: string): [number, number, number] {
  const raw = hex.replace('#', '');
  const n = Number.parseInt(raw.length === 3 ? raw.split('').map(ch => ch + ch).join('') : raw, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

export function rgbCss(r: number, g: number, b: number): string {
  return `rgb(${clamp(Math.round(r), 0, 255)}, ${clamp(Math.round(g), 0, 255)}, ${clamp(Math.round(b), 0, 255)})`;
}

export function hexToRgbCss(hex: string): string {
  const [r, g, b] = hexToRgb(hex);
  return rgbCss(r, g, b);
}

function srgbToLinear(channel: number): number {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function linearToSrgb(channel: number): number {
  const c = channel <= 0.0031308 ? 12.92 * channel : 1.055 * channel ** (1 / 2.4) - 0.055;
  return clamp(c, 0, 1) * 255;
}

export function rgbToOkLab(r: number, g: number, b: number): [number, number, number] {
  const lr = srgbToLinear(r);
  const lg = srgbToLinear(g);
  const lb = srgbToLinear(b);
  const l = Math.cbrt(0.4122214708 * lr + 0.5363325363 * lg + 0.0514459929 * lb);
  const m = Math.cbrt(0.2119034982 * lr + 0.6806995451 * lg + 0.1073969566 * lb);
  const s = Math.cbrt(0.0883024619 * lr + 0.2817188376 * lg + 0.6299787005 * lb);
  return [
    0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s,
  ];
}

export function okLabToRgb(L: number, a: number, b: number): [number, number, number] {
  const l = L + 0.3963377774 * a + 0.2158037573 * b;
  const m = L - 0.1055613458 * a - 0.0638541728 * b;
  const s = L - 0.0894841775 * a - 1.2914855480 * b;
  const l3 = l * l * l;
  const m3 = m * m * m;
  const s3 = s * s * s;
  return [
    linearToSrgb(+4.0767416621 * l3 - 3.3077115913 * m3 + 0.2309699292 * s3),
    linearToSrgb(-1.2684380046 * l3 + 2.6097574011 * m3 - 0.3413193965 * s3),
    linearToSrgb(-0.0041960863 * l3 - 0.7034186147 * m3 + 1.7076147010 * s3),
  ];
}

export function hexToOkLab(hex: string): [number, number, number] {
  const [r, g, b] = hexToRgb(hex);
  return rgbToOkLab(r, g, b);
}

export function interpolateOkLab(from: [number, number, number], to: [number, number, number], t: number): [number, number, number] {
  const k = clamp(t, 0, 1);
  return [
    from[0] + (to[0] - from[0]) * k,
    from[1] + (to[1] - from[1]) * k,
    from[2] + (to[2] - from[2]) * k,
  ];
}

export function interpolateAdjacentStopsInOKLab(remaining: number): string {
  const p = clamp(remaining, 0, 100);
  const stops = QUOTA_COLOR_STOPS;
  for (let i = 0; i < stops.length - 1; i++) {
    const upper = stops[i];
    const lower = stops[i + 1];
    if (p <= upper.remaining && p >= lower.remaining) {
      const span = upper.remaining - lower.remaining;
      const t = span === 0 ? 0 : (upper.remaining - p) / span;
      if (t <= 0) return hexToRgbCss(upper.hex);
      if (t >= 1) return hexToRgbCss(lower.hex);
      const [L, a, b] = interpolateOkLab(hexToOkLab(upper.hex), hexToOkLab(lower.hex), t);
      return rgbCss(...okLabToRgb(L, a, b));
    }
  }
  return hexToRgbCss(p >= 100 ? stops[0].hex : stops[stops.length - 1].hex);
}

export function neutralVisual(): QuotaVisual {
  return { color: NEUTRAL_QUOTA_COLOR, arcDegrees: 0, remaining: 0 };
}

export function quotaVisual(remaining: number | null | undefined, state: QuotaState): QuotaVisual {
  if (state !== 'fresh' || remaining == null || !Number.isFinite(remaining)) return neutralVisual();
  const p = clamp(remaining, 0, 100);
  return {
    color: interpolateAdjacentStopsInOKLab(p),
    arcDegrees: p * 3.6,
    remaining: p,
  };
}

export function mixFreshVisual(fromRemaining: number, toRemaining: number, t: number): QuotaVisual {
  return quotaVisual(fromRemaining + (toRemaining - fromRemaining) * clamp(t, 0, 1), 'fresh');
}
