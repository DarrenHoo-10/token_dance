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

  await desktop.close();

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
  await browser.close();

  if (failures.length) throw new Error(failures.join('\n'));
  log.push('FULL_BROWSER_E2E_OK');
  fs.writeFileSync(path.join(outDir, 'web-e2e.log'), log.join('\n'), 'utf8');
  console.log(log.join('\n'));
})().catch(error => {
  console.error(error);
  process.exit(1);
});
