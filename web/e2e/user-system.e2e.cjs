const { chromium } = require('playwright-core');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

(async () => {
  const base = process.env.TOKENDANCE_E2E_BASE_URL || 'http://127.0.0.1:3000';
  const outDir = process.env.TOKENDANCE_E2E_ARTIFACTS || path.join(__dirname, 'artifacts');
  fs.mkdirSync(outDir, { recursive: true });
  const log = [];
  const failures = [];
  const browser = await chromium.launch({
    headless: true,
    executablePath: process.env.TOKENDANCE_E2E_CHROME || 'C:/Program Files/Google/Chrome/Application/chrome.exe',
    args: ['--no-sandbox'],
  });

  function watch(page, label) {
    page.on('pageerror', error => failures.push(`${label}:pageerror:${error.message}`));
    page.on('console', message => {
      if (message.type() !== 'error') return;
      const text = message.text();
      if (page.url().includes('/u/definitely_missing_profile') && text.includes('404')) return;
      failures.push(`${label}:console:${text}`);
    });
  }
  async function record(name, page, extra = '') {
    log.push(`${name}: PASS url=${page.url()} ${extra}`.trim());
  }

  const desktop = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await desktop.newPage();
  watch(page, 'desktop');

  await page.goto(`${base}/me`, { waitUntil: 'networkidle' });
  await page.getByRole('heading', { name: '需要登录' }).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-unauthorized.png'), fullPage: true });
  await record('desktop-unauthorized-state', page);

  await page.goto(`${base}/u/definitely_missing_profile`, { waitUntil: 'networkidle' });
  await page.getByText('公开主页不存在或未开启').waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-error-public-profile.png'), fullPage: true });
  await record('desktop-public-error-state', page, 'PUBLIC_PROFILE_NOT_FOUND');

  const runID = process.env.TOKENDANCE_E2E_RUN_ID || Date.now().toString(36);
  const email = process.env.TOKENDANCE_E2E_EMAIL || `browser-flow-${runID}@tokendance.dev`;
  const handle = process.env.TOKENDANCE_E2E_HANDLE || `browser_flow_${runID}`.slice(0, 32);
  const password = process.env.TOKENDANCE_E2E_PASSWORD || 'BrowserFlowPassword123!';
  const verificationCode = process.env.TOKENDANCE_E2E_AUTH_CODE || '123456';
  const devicePublicKey = crypto.createHash('sha256').update(`tokendance-e2e:${runID}`).digest('hex');
  await page.goto(`${base}/register?return_to=%2Fme`, { waitUntil: 'networkidle' });
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByRole('button', { name: '获取验证码' }).click();
  await page.getByText('验证码已发送至邮箱').waitFor();
  await page.getByLabel('邮箱验证码').fill(verificationCode);
  await page.getByLabel('密码').fill(password);
  await page.screenshot({ path: path.join(outDir, 'desktop-register.png'), fullPage: true });
  await page.getByRole('button', { name: '验证并完成注册' }).click();
  await page.waitForURL('**/onboarding**');
  await page.getByRole('heading', { name: '你想以什么身份出现？' }).waitFor();
  await record('desktop-registration', page, 'email-code-password flow');

  await page.getByLabel('公开昵称').fill('Browser Flow');
  await page.getByLabel('唯一 Handle').fill(handle);
  await page.getByText('公开摘要', { exact: true }).click();
  await page.screenshot({ path: path.join(outDir, 'desktop-onboarding.png'), fullPage: true });
  await page.getByRole('button', { name: '保存并继续' }).click();
  await page.waitForURL('**/me');
  await page.getByRole('heading', { name: '你的 Token 正在起舞' }).waitFor();
  await page.getByText('用户消息数', { exact: true }).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-dashboard-expanded.png'), fullPage: true });
  await record('desktop-onboarding-dashboard', page, 'public profile enabled');

  await page.goto(`${base}/explore?q=${encodeURIComponent(handle)}`, { waitUntil: 'networkidle' });
  await page.getByText('Browser Flow', { exact: true }).waitFor();
  await page.getByText(`@${handle}`, { exact: false }).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-search-result.png'), fullPage: true });
  await record('desktop-public-search-result', page);

  const searchBox = page.getByPlaceholder('输入 Handle、昵称或 Agent 名称...');
  await searchBox.fill('definitely_no_such_public_record');
  await page.getByRole('button', { name: '搜索' }).click();
  await page.getByText('未找到符合条件的公开记录').first().waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-search-empty.png'), fullPage: true });
  await record('desktop-search-empty-state', page);

  await page.goto(`${base}/settings/devices`, { waitUntil: 'networkidle' });
  await page.getByText('暂无连接设备').waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-devices-empty.png'), fullPage: true });
  await record('desktop-devices-empty-state', page);

  await page.getByRole('button', { name: /连接新设备/ }).first().click();
  const dialog = page.getByRole('dialog', { name: '绑定本地 Collector 设备' });
  await dialog.waitFor();
  const dialogText = await dialog.innerText();
  const codeMatch = dialogText.match(/\b[0-9A-HJKMNP-TV-Z]{8}\b/);
  if (!codeMatch) throw new Error(`binding code missing: ${dialogText}`);
  const bindingCode = codeMatch[0];
  await page.screenshot({ path: path.join(outDir, 'desktop-device-binding-code.png'), fullPage: true });
  const claim = await page.evaluate(async ({ code, publicKey }) => {
    const response = await fetch('/v1/installations/claim', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code,
        publicKey,
        deviceName: 'Browser Device',
        osType: 'windows',
        osVersion: '11',
        architecture: 'x86_64',
        collectorVersion: '1.0.0',
      }),
    });
    return { status: response.status, body: await response.json() };
  }, { code: bindingCode, publicKey: devicePublicKey });
  if (claim.status !== 200 || !claim.body.installationId) throw new Error(`claim failed: ${JSON.stringify(claim)}`);
  await dialog.getByRole('button', { name: '关闭' }).last().click();
  await page.reload({ waitUntil: 'networkidle' });
  await page.getByText('Browser Device', { exact: true }).waitFor();
  await page.getByText('active', { exact: true }).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-device-active.png'), fullPage: true });
  await record('desktop-device-binding', page, `installation=${claim.body.installationId}`);

  await page.getByRole('button', { name: '暂停同步' }).click();
  await page.getByText('disabled', { exact: true }).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-device-paused.png'), fullPage: true });
  await record('desktop-device-pause', page);

  await page.getByRole('button', { name: '恢复同步' }).click();
  await page.getByText('active', { exact: true }).waitFor();
  await record('desktop-device-resume', page);

  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: '撤销设备' }).click();
  await page.getByText('revoked', { exact: true }).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-device-revoked.png'), fullPage: true });
  await record('desktop-device-revoke', page);

  await page.goto(`${base}/settings/exports`, { waitUntil: 'networkidle' });
  await page.getByText('暂无导出任务').waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-exports-empty.png'), fullPage: true });
  await record('desktop-export-empty-state', page);
  await page.getByRole('button', { name: /创建导出任务/ }).click();
  await page.getByText('pending', { exact: true }).waitFor();
  await page.getByText(/CSV 导出/).waitFor();
  await page.screenshot({ path: path.join(outDir, 'desktop-export-pending.png'), fullPage: true });
  await record('desktop-export-create-status', page, 'pending');

  // USR-010: persist locale through the real profile API, visit every P0/P1
  // user surface, and compare every analytics payload used by those pages.
  const metricSurfacePaths = [
    '/api/v1/me/summary?range=30d',
    '/api/v1/me/trends/tokens?range=30d&mode=total',
    '/api/v1/me/breakdowns/agents?range=30d',
    '/api/v1/me/breakdowns/models?range=30d',
    '/api/v1/me/skills?range=30d',
    '/api/v1/me/calendar?range=10w',
    '/api/v1/me/activity?range=30d&limit=50',
    '/api/v1/me/filter-options',
    `/api/v1/public/users/${encodeURIComponent(handle)}/trends?range=30d`,
    `/api/v1/public/users/${encodeURIComponent(handle)}/skills?range=30d`,
    '/api/v1/public/leaderboards?board=global&window=30d&metric=tokens',
    `/api/v1/public/compare?handles=${encodeURIComponent(handle)}&range=30d&metric=tokens`,
  ];
  const snapshotMetricSurfaces = async () => page.evaluate(async paths => {
    const volatileFields = new Set([
      'dataWatermarkAt', 'lastCommittedAt', 'generatedAt', 'updatedAt',
      'publishedAt', 'snapshotGeneratedAt',
    ]);
    const canonicalize = value => {
      if (Array.isArray(value)) return value.map(canonicalize);
      if (value && typeof value === 'object') {
        const isRelativeRange = typeof value.key === 'string' && typeof value.timezone === 'string';
        return Object.fromEntries(Object.keys(value).sort()
          .filter(key => !volatileFields.has(key) && !(isRelativeRange && (key === 'from' || key === 'to')))
          .map(key => [key, canonicalize(value[key])]));
      }
      return value;
    };
    const snapshots = {};
    for (const path of paths) {
      const response = await fetch(path);
      const body = await response.text();
      if (!response.ok) throw new Error(`metric surface ${path} returned ${response.status}: ${body}`);
      snapshots[path] = canonicalize(JSON.parse(body));
    }
    return snapshots;
  }, metricSurfacePaths);
  const metricsBeforeLocale = await snapshotMetricSurfaces();

  await page.goto(`${base}/settings/profile`, { waitUntil: 'networkidle' });
  await page.getByLabel(/界面语言|Interface Language/).selectOption('en-US');
  await page.getByRole('button', { name: /^(保存|Save)$/ }).click();
  await page.getByRole('heading', { name: 'Profile', exact: true }).first().waitFor();
  await record('desktop-locale-persist-en', page, 'PATCH /me/profile locale=en-US');

  const englishSurfaces = [
    ['/me', 'Your tokens are dancing'],
    ['/me/activity', 'View Activity'],
    ['/explore', 'Discover people building with AI'],
    ['/leaderboard?window=30d&metric=tokens', 'Token Leaderboard'],
    [`/compare?handles=${encodeURIComponent(handle)}`, 'Developer Comparison'],
    [`/u/${encodeURIComponent(handle)}`, 'Browser Flow'],
    ['/settings/profile', 'Profile'],
    ['/settings/privacy', 'Leaderboard and Public Profile'],
    ['/settings/devices', 'Collector Devices'],
    ['/settings/exports', 'Export My Data'],
  ];
  for (const [surface, heading] of englishSurfaces) {
    await page.goto(`${base}${surface}`, { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: heading, exact: true }).first().waitFor();
    await record(`desktop-en-surface-${surface}`, page);
  }
  const metricsAfterEnglish = await snapshotMetricSurfaces();
  const englishMetricChanges = metricSurfacePaths.filter(path =>
    JSON.stringify(metricsBeforeLocale[path]) !== JSON.stringify(metricsAfterEnglish[path]));
  if (englishMetricChanges.length) {
    const firstChanged = englishMetricChanges[0];
    throw new Error(`persisted en-US locale changed analytics surfaces: ${englishMetricChanges.join(', ')}\nbefore=${JSON.stringify(metricsBeforeLocale[firstChanged])}\nafter=${JSON.stringify(metricsAfterEnglish[firstChanged])}`);
  }

  await page.goto(`${base}/leaderboard?window=30d&metric=tokens`, { waitUntil: 'networkidle' });
  await page.getByRole('button', { name: '中文' }).click();
  await page.getByRole('heading', { name: 'Token 排行榜' }).waitFor();
  if (!page.url().includes('window=30d') || !page.url().includes('metric=tokens')) {
    throw new Error('locale switch changed leaderboard URL filters');
  }
  await page.goto(`${base}/settings/profile`, { waitUntil: 'networkidle' });
  await page.getByLabel(/界面语言|Interface Language/).selectOption('zh-CN');
  await page.getByRole('button', { name: /^(保存|Save)$/ }).click();
  await page.getByRole('heading', { name: '个人资料', exact: true }).first().waitFor();
  const metricsAfterChinese = await snapshotMetricSurfaces();
  const chineseMetricChanges = metricSurfacePaths.filter(path =>
    JSON.stringify(metricsBeforeLocale[path]) !== JSON.stringify(metricsAfterChinese[path]));
  if (chineseMetricChanges.length) {
    throw new Error(`persisted zh-CN locale changed analytics surfaces: ${chineseMetricChanges.join(', ')}`);
  }
  await record('desktop-locale-roundtrip-all-surfaces', page, 'all analytics payloads unchanged; URL filters preserved');

  await desktop.close();

  // The acceptance journey intentionally performs enough full-page navigations to
  // approach the production per-IP session-check limit. Cross one complete window
  // before starting the independent mobile cohorts instead of disabling rate limits.
  await new Promise(resolve => setTimeout(resolve, 61_000));
  log.push('browser-rate-limit-window-reset: PASS waited=61s');

  const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1 });
  const mobilePage = await mobile.newPage();
  watch(mobilePage, 'mobile');
  await mobilePage.goto(`${base}/login`, { waitUntil: 'networkidle' });
  await mobilePage.getByLabel('邮箱', { exact: true }).fill(email);
  await mobilePage.getByLabel('密码').fill(password);
  await mobilePage.getByRole('button', { name: '登录 TokenDance' }).click();
  await mobilePage.waitForURL('**/me');
  await mobilePage.getByRole('heading', { name: '你的 Token 正在起舞' }).waitFor();
  await mobilePage.getByText('用户消息数', { exact: true }).waitFor();
  await mobilePage.screenshot({ path: path.join(outDir, 'mobile-dashboard-expanded.png'), fullPage: true });
  await record('mobile-login-dashboard', mobilePage);

  await mobilePage.goto(`${base}/explore?q=${encodeURIComponent(handle)}`, { waitUntil: 'networkidle' });
  await mobilePage.getByText('Browser Flow', { exact: true }).waitFor();
  await mobilePage.screenshot({ path: path.join(outDir, 'mobile-search-result.png'), fullPage: true });
  await record('mobile-public-search', mobilePage);

  await mobilePage.goto(`${base}/settings/devices`, { waitUntil: 'networkidle' });
  await mobilePage.getByText('Browser Device', { exact: true }).waitFor();
  await mobilePage.getByText('revoked', { exact: true }).waitFor();
  await mobilePage.screenshot({ path: path.join(outDir, 'mobile-device-revoked.png'), fullPage: true });
  await record('mobile-device-state', mobilePage, 'revoked');

  await mobilePage.goto(`${base}/settings/exports`, { waitUntil: 'networkidle' });
  await mobilePage.getByText(/CSV 导出/).waitFor();
  await mobilePage.screenshot({ path: path.join(outDir, 'mobile-export-status.png'), fullPage: true });
  await record('mobile-export-status', mobilePage);

  await mobile.close();

  // A separate mobile account exercises registration, onboarding, and the full
  // device lifecycle at the mobile viewport rather than only reusing desktop state.
  const mobileRegistration = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1 });
  const mobileRegistrationPage = await mobileRegistration.newPage();
  watch(mobileRegistrationPage, 'mobile-registration');
  const mobileEmail = `mobile-browser-flow-${runID}@tokendance.dev`;
  const mobileHandle = `mobile_${runID}`.slice(0, 32);
  const mobilePublicKey = crypto.createHash('sha256').update(`tokendance-mobile-e2e:${runID}`).digest('hex');

  await mobileRegistrationPage.goto(`${base}/register?return_to=%2Fme`, { waitUntil: 'networkidle' });
  await mobileRegistrationPage.getByLabel('邮箱', { exact: true }).fill(mobileEmail);
  await mobileRegistrationPage.getByRole('button', { name: '获取验证码' }).click();
  await mobileRegistrationPage.getByLabel('邮箱验证码').fill(verificationCode);
  await mobileRegistrationPage.getByLabel('密码').fill(password);
  await mobileRegistrationPage.screenshot({ path: path.join(outDir, 'mobile-registration.png'), fullPage: true });
  await mobileRegistrationPage.getByRole('button', { name: '验证并完成注册' }).click();
  await mobileRegistrationPage.waitForURL('**/onboarding**');
  await mobileRegistrationPage.getByRole('heading', { name: '你想以什么身份出现？' }).waitFor();
  await record('mobile-registration', mobileRegistrationPage, 'email-code-password flow');

  await mobileRegistrationPage.getByLabel('公开昵称').fill('Mobile Browser Flow');
  await mobileRegistrationPage.getByLabel('唯一 Handle').fill(mobileHandle);
  await mobileRegistrationPage.getByText('公开摘要', { exact: true }).click();
  await mobileRegistrationPage.screenshot({ path: path.join(outDir, 'mobile-onboarding.png'), fullPage: true });
  await mobileRegistrationPage.getByRole('button', { name: '保存并继续' }).click();
  await mobileRegistrationPage.waitForURL('**/me');
  await mobileRegistrationPage.getByRole('heading', { name: '你的 Token 正在起舞' }).waitFor();
  await record('mobile-onboarding-dashboard', mobileRegistrationPage, `handle=${mobileHandle}`);

  await mobileRegistrationPage.goto(`${base}/settings/devices`, { waitUntil: 'networkidle' });
  await mobileRegistrationPage.getByText('暂无连接设备').waitFor();
  await mobileRegistrationPage.getByRole('button', { name: /连接新设备/ }).first().click();
  const mobileDialog = mobileRegistrationPage.getByRole('dialog', { name: '绑定本地 Collector 设备' });
  await mobileDialog.waitFor();
  const mobileCodeText = await mobileDialog.innerText();
  const mobileCodeMatch = mobileCodeText.match(/\b[0-9A-HJKMNP-TV-Z]{8}\b/);
  if (!mobileCodeMatch) throw new Error(`mobile binding code missing: ${mobileCodeText}`);
  const mobileClaim = await mobileRegistrationPage.evaluate(async ({ code, publicKey }) => {
    const response = await fetch('/v1/installations/claim', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code,
        publicKey,
        deviceName: 'Mobile Browser Device',
        osType: 'windows',
        osVersion: '11',
        architecture: 'x86_64',
        collectorVersion: '1.0.0',
      }),
    });
    return { status: response.status, body: await response.json() };
  }, { code: mobileCodeMatch[0], publicKey: mobilePublicKey });
  if (mobileClaim.status !== 200 || !mobileClaim.body.installationId) throw new Error(`mobile claim failed: ${JSON.stringify(mobileClaim)}`);
  await mobileDialog.getByRole('button', { name: '关闭' }).last().click();
  await mobileRegistrationPage.reload({ waitUntil: 'networkidle' });
  await mobileRegistrationPage.getByText('Mobile Browser Device', { exact: true }).waitFor();
  await record('mobile-device-binding', mobileRegistrationPage, `installation=${mobileClaim.body.installationId}`);
  await mobileRegistrationPage.getByRole('button', { name: '暂停同步' }).click();
  await mobileRegistrationPage.getByText('disabled', { exact: true }).waitFor();
  await record('mobile-device-pause', mobileRegistrationPage);
  await mobileRegistrationPage.getByRole('button', { name: '恢复同步' }).click();
  await mobileRegistrationPage.getByText('active', { exact: true }).waitFor();
  await record('mobile-device-resume', mobileRegistrationPage);
  mobileRegistrationPage.once('dialog', dialog => dialog.accept());
  await mobileRegistrationPage.getByRole('button', { name: '撤销设备' }).click();
  await mobileRegistrationPage.getByText('revoked', { exact: true }).waitFor();
  if (await mobileRegistrationPage.getByRole('button', { name: '恢复同步' }).count() !== 0) {
    throw new Error('revoked mobile device still exposes the resume action');
  }
  await mobileRegistrationPage.screenshot({ path: path.join(outDir, 'mobile-device-lifecycle.png'), fullPage: true });
  await record('mobile-device-revoke', mobileRegistrationPage, 'resume action absent');

  await mobileRegistration.close();
  await browser.close();

  if (failures.length) throw new Error(failures.join('\n'));
  log.push('FULL_BROWSER_E2E_OK');
  fs.writeFileSync(path.join(outDir, 'web-e2e.log'), log.join('\n'), 'utf8');
  console.log(log.join('\n'));
})().catch(error => {
  console.error(error);
  process.exit(1);
});
