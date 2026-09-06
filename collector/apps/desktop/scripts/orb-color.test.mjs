import test from 'node:test';
import assert from 'node:assert/strict';
import {
  QUOTA_COLOR_STOPS,
  hexToOkLab,
  hexToRgbCss,
  interpolateAdjacentStopsInOKLab,
  interpolateOkLab,
  okLabToRgb,
  quotaVisual,
  rgbToOkLab,
} from '../src/orb/color.ts';
import { classifyOrbEvent, isNewerRevision, shouldSnapQuotaVisual } from '../src/orb/types.ts';

function parseRgb(css) {
  const match = /^rgb\((\d+), (\d+), (\d+)\)$/.exec(css);
  assert.ok(match, `expected rgb() color, got ${css}`);
  return [Number(match[1]), Number(match[2]), Number(match[3])];
}

test('nine quota stops map to their exact hex colors', () => {
  assert.equal(QUOTA_COLOR_STOPS.length, 9);
  for (const stop of QUOTA_COLOR_STOPS) {
    assert.equal(interpolateAdjacentStopsInOKLab(stop.remaining), hexToRgbCss(stop.hex));
    assert.equal(quotaVisual(stop.remaining, 'fresh').color, hexToRgbCss(stop.hex));
  }
});

test('62.5% is the OKLab midpoint between the 70 and 55 stops', () => {
  const upper = hexToOkLab('#88CE46');
  const lower = hexToOkLab('#CBD33C');
  const mid = interpolateOkLab(upper, lower, 0.5);
  const color = interpolateAdjacentStopsInOKLab(62.5);
  const [r, g, b] = parseRgb(color);
  const got = rgbToOkLab(r, g, b);
  assert.ok(Math.abs(got[0] - mid[0]) < 0.002);
  assert.ok(Math.abs(got[1] - mid[1]) < 0.002);
  assert.ok(Math.abs(got[2] - mid[2]) < 0.002);
  const [er, eg, eb] = okLabToRgb(...mid).map(v => Math.round(v));
  assert.deepEqual(parseRgb(color), [er, eg, eb]);
  assert.notEqual(color, hexToRgbCss('#88CE46'));
  assert.notEqual(color, hexToRgbCss('#CBD33C'));
});

test('0% and 100% arc degrees and colors', () => {
  assert.equal(quotaVisual(100, 'fresh').arcDegrees, 360);
  assert.equal(quotaVisual(0, 'fresh').arcDegrees, 0);
  assert.equal(quotaVisual(72, 'fresh').arcDegrees, 72 * 3.6);
  assert.equal(quotaVisual(100, 'fresh').color, 'rgb(36, 200, 138)');
  assert.equal(quotaVisual(0, 'fresh').color, 'rgb(222, 61, 75)');
});

test('stale and not_connected stay neutral with no arc', () => {
  for (const state of ['stale', 'not_connected', 'unavailable', 'loading', 'auth_required', 'no_quota', 'unlimited']) {
    const visual = quotaVisual(0, state);
    assert.equal(visual.arcDegrees, 0);
    assert.equal(visual.color, 'rgb(138, 144, 152)');
    assert.equal(quotaVisual(72, state).arcDegrees, 0);
  }
});

test('green to red interpolation never passes through blue or purple', () => {
  for (let p = 0; p <= 100; p += 1) {
    const [r, g, b] = parseRgb(interpolateAdjacentStopsInOKLab(p));
    const lab = rgbToOkLab(r, g, b);
    assert.ok(lab[2] > 0, `OKLab b went toward blue at ${p}%: ${r},${g},${b}`);
    assert.ok(b < Math.max(r, g), `blue was the dominant channel at ${p}%: ${r},${g},${b}`);
  }
});

test('source switch or stale quota snaps instead of animating to 0%', () => {
  assert.equal(shouldSnapQuotaVisual('fresh', 'stale', 'codex:5h', 'codex:5h'), true);
  assert.equal(shouldSnapQuotaVisual('fresh', 'not_connected', 'codex:5h', 'codex:5h'), true);
  assert.equal(shouldSnapQuotaVisual('fresh', 'fresh', 'codex:5h', 'cursor:auto'), true);
  assert.equal(shouldSnapQuotaVisual('fresh', 'fresh', 'codex:5h', 'codex:5h'), false);
});

test('revision and stream rejection helpers', () => {
  assert.equal(classifyOrbEvent('s1', '1', 's1', '2'), 'accept');
  assert.equal(classifyOrbEvent('s1', '2', 's1', '2'), 'drop');
  assert.equal(classifyOrbEvent('s1', '3', 's1', '2'), 'drop');
  assert.equal(classifyOrbEvent('s1', '3', 's2', '9'), 'resync');
  assert.equal(classifyOrbEvent(null, null, 's1', '1'), 'drop');
  assert.equal(isNewerRevision('9', '10'), true);
  assert.equal(isNewerRevision('10', '10'), false);
  assert.equal(isNewerRevision(null, '1'), true);
  const huge = (10n ** 20n).toString();
  const bigger = (10n ** 20n + 1n).toString();
  assert.equal(isNewerRevision(huge, bigger), true);
  assert.equal(classifyOrbEvent('s1', huge, 's1', bigger), 'accept');
});
