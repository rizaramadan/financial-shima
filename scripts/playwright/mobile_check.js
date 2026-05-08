// Mobile-viewport sanity sweep for the post-#16 mobile-first CSS pass.
// Logs in (password flow), navigates the major pages at iPhone-SE width
// (375×667), and dumps screenshots so we can eyeball nav stickiness,
// allocation rows, and table wrapping. Captures into out/mobile_check/.
const { chromium, devices } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8081';
const PASS = process.env.LOGIN_PASSWORD || 'dev-password';
const outDir = path.resolve(__dirname, 'out', 'mobile_check');
fs.mkdirSync(outDir, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({
    viewport: { width: 375, height: 667 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
    userAgent: devices['iPhone SE'].userAgent,
  });
  const page = await ctx.newPage();

  // 1. Login.
  await page.goto(BASE + '/login');
  await page.screenshot({ path: path.join(outDir, '01_login.png') });

  await page.fill('input[name=identifier]', '@riza_ramadan');
  await page.fill('input[name=password]', PASS);
  await page.click('button[type=submit]');
  await page.waitForURL(BASE + '/');

  const pages = [
    ['02_home', '/'],
    ['03_transactions', '/transactions'],
    ['04_spending', '/spending'],
    ['05_income', '/income-templates'],
    ['06_income_new', '/income-templates/new'],
    ['07_notifications', '/notifications'],
    ['08_settings', '/settings'],
    ['09_pos_new', '/pos/new'],
  ];
  for (const [name, url] of pages) {
    await page.goto(BASE + url);
    await page.screenshot({ path: path.join(outDir, name + '.png') });
    console.log('saved', name);
  }

  // Sticky-nav check: scroll mid-way on transactions and snap nav region.
  await page.goto(BASE + '/transactions');
  await page.evaluate(() => window.scrollTo(0, 400));
  await page.screenshot({ path: path.join(outDir, '10_sticky_nav_scrolled.png') });
  console.log('saved 10_sticky_nav_scrolled');

  await browser.close();
})();
