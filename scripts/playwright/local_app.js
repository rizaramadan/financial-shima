// Drives the real local app end-to-end via Playwright:
// /login → POST → /verify (OTP fetched from /dev/last-otp shim) →
// signed in → /pos/new → fill form → submit → /pos/<uuid>.
//
// Prereq: dev_server running on :8081 (DATABASE_URL set, demo seed
// applied for accounts/users).
//
//   go run ./scripts/dev_server.go &
//   node scripts/playwright/local_app.js
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8081';
const IDENT = '@riza_ramadan';
const outDir = path.join(__dirname, 'out');
fs.mkdirSync(outDir, { recursive: true });

function pass(msg) { console.log('✓', msg); }
function die(msg) { console.error('✗', msg); process.exit(1); }

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({ viewport: { width: 800, height: 1000 } });
  const page = await ctx.newPage();

  // ── /login ───────────────────────────────────────────────────────
  await page.goto(BASE + '/login');
  if (!(await page.locator('h1').textContent()).includes('Sign in')) {
    die('login page missing h1');
  }
  await page.screenshot({ path: path.join(outDir, '01_login.png'), fullPage: true });
  pass('rendered /login');

  // Type identifier and submit
  await page.fill('input[name="identifier"]', IDENT);
  await page.screenshot({ path: path.join(outDir, '02_login_filled.png'), fullPage: true });
  await Promise.all([
    page.waitForURL(/\/verify/),
    page.click('button[type="submit"]'),
  ]);
  pass('submitted /login → redirected to /verify');

  // ── /verify (read OTP from dev shim) ────────────────────────────
  const otpResp = await page.request.get(
    BASE + '/dev/last-otp?identifier=' + encodeURIComponent(IDENT)
  );
  if (!otpResp.ok()) die('dev /last-otp failed: ' + otpResp.status());
  const otp = (await otpResp.text()).trim();
  if (!/^\d{6}$/.test(otp)) die('OTP shape invalid: ' + JSON.stringify(otp));
  pass('read OTP from dev shim: ' + '*'.repeat(otp.length));

  await page.screenshot({ path: path.join(outDir, '03_verify_empty.png'), fullPage: true });
  await page.fill('input[name="code"]', otp);
  await page.screenshot({ path: path.join(outDir, '04_verify_filled.png'), fullPage: true });
  await Promise.all([
    page.waitForURL(BASE + '/'),
    page.click('button[type="submit"]'),
  ]);
  pass('submitted /verify → signed in (redirected to /)');

  // ── /home — proves session works ─────────────────────────────────
  await page.screenshot({ path: path.join(outDir, '05_home.png'), fullPage: true });
  if (!(await page.locator('h1').textContent()).startsWith('Hi,')) {
    die('home not authenticated');
  }
  pass('rendered authenticated /home');

  // ── /pos/new ─────────────────────────────────────────────────────
  await page.goto(BASE + '/pos/new');
  await page.screenshot({ path: path.join(outDir, '06_pos_new_empty.png'), fullPage: true });
  if (!(await page.locator('h1').textContent()).includes('New Pos')) {
    die('pos/new missing h1');
  }
  pass('rendered /pos/new');

  // Fill form
  const uniqueName = 'pw-' + Date.now();
  await page.fill('input[name="name"]', uniqueName);
  await page.fill('input[name="currency"]', 'idr');
  await page.fill('input[name="target"]', '4444444');
  await page.screenshot({ path: path.join(outDir, '07_pos_new_filled.png'), fullPage: true });
  pass('filled form: ' + uniqueName + ' / idr / 4444444');

  // Debug: dump cookies before submitting
  const beforeCookies = await ctx.cookies();
  console.log('  cookies before submit:', JSON.stringify(beforeCookies.map(c => ({name: c.name, value: c.value.slice(0,8)+'…', path: c.path, sameSite: c.sameSite}))));

  // Listen for ALL network responses until we land somewhere stable
  const allResponses = [];
  page.on('response', r => {
    allResponses.push({ url: r.url(), status: r.status(), method: r.request().method() });
  });

  // Submit the NEW-POS form specifically. Selecting "form" alone
  // would grab the layout's Sign-out form (which lives in the nav
  // and is first in DOM order).
  await page.evaluate(() => document.querySelector('form[action="/pos"]').requestSubmit());
  // Give the navigation chain time to complete
  await page.waitForLoadState('load', { timeout: 10000 }).catch(() => {});
  await page.waitForTimeout(500);

  console.log('  network trace:');
  allResponses.forEach(r => console.log(`    ${r.method} ${r.url} → ${r.status}`));

  const finalURL = page.url();
  pass('submitted form → ' + finalURL);
  if (!/\/pos\/[0-9a-f-]{36}/.test(finalURL)) {
    die('expected redirect to /pos/<uuid>, got ' + finalURL);
  }
  await page.screenshot({ path: path.join(outDir, '08_pos_detail.png'), fullPage: true });

  // Verify the page actually shows our created Pos
  const h1 = await page.locator('h1').first().textContent();
  if (!h1.includes(uniqueName)) {
    die('pos detail h1 = ' + JSON.stringify(h1) + ' (want ' + uniqueName + ')');
  }
  const subtitle = await page.locator('.subtitle').first().textContent();
  if (!subtitle.includes('Rp 4.444.444')) {
    die('subtitle missing formatted target; got ' + JSON.stringify(subtitle));
  }
  pass('Pos detail shows name + formatted target Rp 4.444.444');

  // Cleanup: navigate home, prove the new Pos appears in the list
  await page.goto(BASE + '/');
  const homeText = await page.locator('main').textContent();
  if (!homeText.includes(uniqueName)) {
    die('new Pos not visible on /home after creation');
  }
  pass('new Pos visible on /home (read/write consistency)');

  await browser.close();
  console.log('\nPASS — Playwright drove /pos/new end-to-end through the real local app');
  console.log('   screenshots in:', outDir);
})();
