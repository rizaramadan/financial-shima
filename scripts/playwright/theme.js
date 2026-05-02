// Captures /settings + /home in all three theme states (auto, light,
// dark) for the docs page. Drives the live local dev_server.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8081';
const IDENT = '@riza_ramadan';
const outDir = path.resolve(__dirname, '..', '..', 'docs', 'screenshots', 'theme');
fs.mkdirSync(outDir, { recursive: true });

async function login(ctx, page) {
  await page.goto(BASE + '/login');
  await page.fill('input[name="identifier"]', IDENT);
  await Promise.all([page.waitForURL(/\/verify/), page.click('button[type="submit"]')]);
  const r = await page.request.get(BASE + '/dev/last-otp?identifier=' + encodeURIComponent(IDENT));
  const otp = (await r.text()).trim();
  await page.fill('input[name="code"]', otp);
  await Promise.all([page.waitForURL(BASE + '/'), page.click('button[type="submit"]')]);
}

async function setTheme(page, theme) {
  await page.goto(BASE + '/settings');
  // Click the matching button; wait for navigation chain to settle so
  // the response (and Set-Cookie) is fully applied before we screenshot.
  await Promise.all([
    page.waitForResponse(r => r.url().endsWith('/settings/theme') && r.request().method() === 'POST'),
    page.locator('button[name="theme"][value="' + theme + '"]').click(),
  ]);
  await page.waitForLoadState('load');
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({
    viewport: { width: 900, height: 1100 },
    // Force "system prefers dark" so the auto-mode shot is unambiguously dark.
    colorScheme: 'dark',
  });
  const page = await ctx.newPage();
  await login(ctx, page);

  // 1. Auto (no cookie) — system prefers dark → dark renders
  await setTheme(page, 'auto'); // ensure cookie cleared
  await page.goto(BASE + '/settings');
  await page.screenshot({ path: path.join(outDir, '1_auto_settings_dark_os.png'), fullPage: true });
  await page.goto(BASE + '/');
  await page.screenshot({ path: path.join(outDir, '1_auto_home_dark_os.png'), fullPage: true });

  // 2. Explicit light — overrides the dark OS preference
  await setTheme(page, 'light');
  await page.goto(BASE + '/settings');
  await page.screenshot({ path: path.join(outDir, '2_light_settings.png'), fullPage: true });
  await page.goto(BASE + '/');
  await page.screenshot({ path: path.join(outDir, '2_light_home.png'), fullPage: true });

  // 3. Explicit dark
  await setTheme(page, 'dark');
  await page.goto(BASE + '/settings');
  await page.screenshot({ path: path.join(outDir, '3_dark_settings.png'), fullPage: true });
  await page.goto(BASE + '/');
  await page.screenshot({ path: path.join(outDir, '3_dark_home.png'), fullPage: true });

  await browser.close();
  console.log('captured 6 screenshots into', outDir);
})();
