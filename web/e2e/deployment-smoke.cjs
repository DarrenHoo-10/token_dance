const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright-core');

(async () => {
  const base = (process.env.TOKENDANCE_E2E_BASE_URL || 'https://www.nexorai.com.cn/token-dance').replace(/\/$/, '');
  const artifact = path.join(__dirname, 'artifacts', 'deployment');
  fs.mkdirSync(artifact, { recursive: true });
  const browser = await chromium.launch({ headless: true, executablePath:
    process.env.TOKENDANCE_E2E_CHROME || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    const badResponses = [];
    page.on('pageerror', e => errors.push(e.message));
    page.on('response', r => { if (r.status() >= 400) badResponses.push(`${r.status()} ${new URL(r.url()).pathname}`); });
    await page.goto(base + '/', { waitUntil: 'networkidle' });
    await page.waitForURL(base + '/login?return_to=/');
    assert.equal(await page.locator('input[type="email"]').count(), 1);
    assert.equal(await page.locator('input[type="password"]').count(), 1);
    for (const img of await page.locator('img').all()) {
      assert(await img.evaluate(node => node.complete && node.naturalWidth > 0), 'Logo failed to load');
    }
    await page.screenshot({ path: path.join(artifact, 'login.png'), fullPage: true });
    await page.goto(base + '/register', { waitUntil: 'networkidle' });
    assert.equal(await page.locator('input[type="email"]').count(), 1);
    await page.reload({ waitUntil: 'networkidle' });
    await page.goto(base + '/leaderboard', { waitUntil: 'networkidle' });
    const allTime = page.getByRole('tab', { name: 'All Time', exact: true });
    const allTimeResponse = page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname.endsWith('/api/v1/public/leaderboards') && url.searchParams.get('window') === 'all';
    });
    await allTime.click();
    assert.equal((await allTimeResponse).status(), 200, 'All Time API request failed');
    await page.getByText('加载中...', { exact: true }).waitFor({ state: 'hidden' });
    assert.equal(await allTime.getAttribute('aria-selected'), 'true');
    await page.waitForLoadState('networkidle');
    await page.screenshot({ path: path.join(artifact, 'leaderboard.png'), fullPage: true });
    assert.deepEqual(errors, [], 'Browser runtime errors');
    assert.deepEqual(badResponses, [], 'Failed page or API requests');
    console.log('PASS: HTTPS, login redirect/form/logo, registration deep-link reload, leaderboard All Time, no runtime/request failures.');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
