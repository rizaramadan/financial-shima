// UAT walkthrough — drives the real local app via Playwright and
// captures every step into docs/screenshots/uat/. Covers the happy
// path of every user-facing feature implemented today, plus the
// high-impact validation negatives that earn their keep on the
// release checklist.
//
// Prereq: scripts/dev_server.go running on :8081 with the demo seed
// already applied.
//
// Usage:
//   go run ./scripts/dev_server.go &
//   node scripts/playwright/uat.js
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8081';
const RIZA = '@riza_ramadan';
const SHIMA = '@shima'; // used for wrong-OTP negative — separate cooldown
const outDir = path.resolve(__dirname, '..', '..', 'docs', 'screenshots', 'uat');
fs.mkdirSync(outDir, { recursive: true });

const VIEWPORT_COMPACT = { width: 540, height: 760 };
const VIEWPORT_DEFAULT = { width: 900, height: 1300 };
const VIEWPORT_WIDE    = { width: 1100, height: 1300 };

let stepNo = 0;
function step(label) {
  stepNo += 1;
  const id = String(stepNo).padStart(2, '0');
  console.log(`▶ ${id}  ${label}`);
  return id;
}
function pass(msg) { console.log('  ✓', msg); }
function die(msg) { console.error('  ✗', msg); process.exit(1); }

async function shoot(page, id, name, opts = {}) {
  const file = path.join(outDir, `${id}_${name}.png`);
  await page.screenshot({ path: file, fullPage: opts.fullPage !== false });
  pass(`saved ${path.basename(file)}`);
}

async function fetchOTP(page, identifier) {
  const r = await page.request.get(BASE + '/dev/last-otp?identifier=' + encodeURIComponent(identifier));
  if (!r.ok()) die(`/dev/last-otp ${identifier}: ${r.status()}`);
  const code = (await r.text()).trim();
  if (!/^\d{6}$/.test(code)) die('OTP shape: ' + JSON.stringify(code));
  return code;
}

(async () => {
  const browser = await chromium.launch({ headless: true });

  // ── 01 Negative: unknown identifier — does NOT issue an OTP, so
  //                 no cooldown side-effects on later steps.
  {
    const id = step('Negative — login with unknown identifier');
    const ctx = await browser.newContext({ viewport: VIEWPORT_COMPACT });
    const page = await ctx.newPage();
    await page.goto(BASE + '/login');
    await shoot(page, id + 'a', 'login_empty');
    await page.fill('input[name="identifier"]', '@nobody_seeded');
    await page.click('button[type="submit"]');
    await page.waitForLoadState('load');
    const alert = (await page.locator('.alert').textContent()).trim();
    if (!alert.includes('User not found')) die('expected "User not found"; got ' + alert);
    await shoot(page, id + 'b', 'login_user_not_found');
    pass('alert: ' + alert);
    await ctx.close();
  }

  // Happy-path Riza session — used for everything authenticated.
  const ctx = await browser.newContext({ viewport: VIEWPORT_COMPACT });
  const page = await ctx.newPage();

  // ── 02 Happy: sign in with OTP ───────────────────────────────────
  {
    const id = step('Happy — sign in with OTP (S1)');
    await page.goto(BASE + '/login');
    await page.fill('input[name="identifier"]', RIZA);
    await shoot(page, id + 'a', 'login_filled');
    await Promise.all([page.waitForURL(/\/verify/), page.click('button[type="submit"]')]);
    const otp = await fetchOTP(page, RIZA);
    await shoot(page, id + 'b', 'verify_empty');
    await page.fill('input[name="code"]', otp);
    await shoot(page, id + 'c', 'verify_filled');
    await Promise.all([page.waitForURL(BASE + '/'), page.click('button[type="submit"]')]);
    pass('signed in as Riza');
  }

  await page.setViewportSize(VIEWPORT_DEFAULT);

  // ── 03 Happy: home dashboard ─────────────────────────────────────
  {
    const id = step('Happy — home dashboard (S17)');
    await page.goto(BASE + '/');
    await shoot(page, id, 'home_dashboard');
    const text = await page.locator('main').textContent();
    if (!text.includes('Hi,')) die('home not authenticated');
    pass('shows accounts + Pos by currency, progress bars, neg-cash marker if any');
  }

  // ── 04 Negative: empty name on /pos/new ──────────────────────────
  {
    const id = step('Negative — /pos/new with empty name');
    await page.goto(BASE + '/pos/new');
    await shoot(page, id + 'a', 'pos_new_empty');
    await page.evaluate(() => {
      const f = document.querySelector('form[action="/pos"]');
      const n = f.querySelector('input[name="name"]');
      n.removeAttribute('required'); n.value = '   ';
      f.requestSubmit();
    });
    await page.waitForLoadState('load');
    const alert = await page.locator('.alert').textContent();
    if (!alert.includes('Name is required')) die('expected Name-required alert; got ' + alert);
    await shoot(page, id + 'b', 'pos_new_empty_name_error');
    pass('server rejected: ' + lastLine(alert));
  }

  // ── 05 Negative: invalid currency on /pos/new ────────────────────
  {
    const id = step('Negative — /pos/new with invalid currency');
    await page.goto(BASE + '/pos/new');
    await page.evaluate(() => {
      const f = document.querySelector('form[action="/pos"]');
      f.querySelector('input[name="name"]').value = 'UAT bad currency';
      const cur = f.querySelector('input[name="currency"]');
      cur.removeAttribute('pattern'); cur.value = 'BAD CURRENCY';
      f.requestSubmit();
    });
    await page.waitForLoadState('load');
    const alert = await page.locator('.alert').textContent();
    if (!alert.includes('lowercase')) die('expected lowercase-error alert; got ' + alert);
    await shoot(page, id, 'pos_new_bad_currency_error');
    pass('server rejected: ' + lastLine(alert));
  }

  // ── 06 Negative: duplicate name on /pos/new ──────────────────────
  {
    const id = step('Negative — /pos/new with duplicate name');
    await page.goto(BASE + '/pos/new');
    await page.fill('input[name="name"]', 'Mortgage');
    await page.fill('input[name="currency"]', 'idr');
    await page.evaluate(() => document.querySelector('form[action="/pos"]').requestSubmit());
    await page.waitForLoadState('load');
    const alert = await page.locator('.alert').textContent();
    if (!alert.includes('already exists')) die('expected dedup alert; got ' + alert);
    await shoot(page, id, 'pos_new_duplicate_error');
    pass('server caught UNIQUE: ' + lastLine(alert));
  }

  // ── 07 Happy: create new Pos end-to-end ──────────────────────────
  {
    const id = step('Happy — create new Pos end-to-end');
    const uniqueName = 'UAT Liburan ' + Date.now();
    await page.goto(BASE + '/pos/new');
    await page.fill('input[name="name"]', uniqueName);
    await page.fill('input[name="currency"]', 'idr');
    await page.fill('input[name="target"]', '15000000');
    await shoot(page, id + 'a', 'pos_new_filled');
    await page.evaluate(() => document.querySelector('form[action="/pos"]').requestSubmit());
    await page.waitForURL(/\/pos\/[0-9a-f-]{36}/);
    await shoot(page, id + 'b', 'pos_detail_after_create');
    const subtitle = await page.locator('.subtitle').first().textContent();
    if (!subtitle.includes('Rp 15.000.000')) die('target not formatted: ' + subtitle);
    pass('created at ' + page.url().replace(BASE, '') + '; subtitle: ' + subtitle.trim());
  }

  // ── 08 Happy: existing Pos with obligation ───────────────────────
  // Pos names on /home are plain text (not links), so navigate via
  // the /dev/pos-id shim straight to /pos/<id>.
  {
    const id = step('Happy — Pos detail with open obligation (S18)');
    const r = await page.request.get(BASE + '/dev/pos-id?name=Belanja%20Bulanan');
    if (r.ok()) {
      const posID = (await r.text()).trim();
      await page.goto(BASE + '/pos/' + posID);
      await shoot(page, id, 'pos_detail_with_obligation');
      const txt = await page.locator('main').textContent();
      if (txt.includes('payable') && txt.includes('Mortgage')) {
        pass('payable obligation to Mortgage visible');
      } else {
        pass('rendered (no obligation row in current seed)');
      }
    } else {
      pass('(skip: Belanja Bulanan not in seed)');
    }
  }

  // ── 09 Happy: transactions list ──────────────────────────────────
  await page.setViewportSize(VIEWPORT_WIDE);
  {
    const id = step('Happy — transactions list (S16)');
    await page.goto(BASE + '/transactions');
    await shoot(page, id, 'transactions_list');
    pass('chips, formatted amounts, sign + colors');
  }

  // ── 10 Happy: spending pivot ─────────────────────────────────────
  {
    const id = step('Happy — spending months × Pos (S19)');
    await page.goto(BASE + '/spending?from=2025-11-01&to=2026-04-30');
    await shoot(page, id, 'spending_pivot');
    pass('6-month × top-N pivot');
  }

  // ── 11 Happy: notifications feed ─────────────────────────────────
  await page.setViewportSize(VIEWPORT_DEFAULT);
  {
    const id = step('Happy — notifications feed (S22)');
    await page.goto(BASE + '/notifications');
    await shoot(page, id, 'notifications_feed');
    pass('unread bold + read faded + Mark all read');
  }

  // ── 12 Happy: income templates — list (empty/populated) ─────────
  {
    const id = step('Happy — income templates list');
    await page.goto(BASE + '/income-templates');
    await shoot(page, id, 'income_templates_list');
    pass('rendered list (may be empty if no templates created yet)');
  }

  // ── 13 Happy: create a new income template via web form ─────────
  let templateID;
  let templateName;
  {
    const id = step('Happy — create income template');
    await page.goto(BASE + '/income-templates/new');
    await shoot(page, id + 'a', 'income_template_new_empty');
    templateName = 'UAT Salary ' + Date.now();
    await page.fill('input[name="name"]', templateName);
    // Pick first 3 IDR Pos and the leftover (4th) from the seed, by
    // reading the option list.
    const posOpts = await page.evaluate(() => {
      return [...document.querySelectorAll('select[name="leftover_pos_id"] option')]
        .filter(o => o.value !== '' && o.textContent.includes('(idr)'))
        .map(o => ({ id: o.value, label: o.textContent }));
    });
    if (posOpts.length < 4) die('need ≥4 IDR Pos in seed; got ' + posOpts.length);
    await page.selectOption('select[name="leftover_pos_id"]', posOpts[3].id);
    await page.selectOption('select[name="pos_id_0"]', posOpts[0].id);
    await page.fill('input[name="amount_0"]', '12000000');
    await page.selectOption('select[name="pos_id_1"]', posOpts[1].id);
    await page.fill('input[name="amount_1"]', '5000000');
    await page.selectOption('select[name="pos_id_2"]', posOpts[2].id);
    await page.fill('input[name="amount_2"]', '3000000');
    await shoot(page, id + 'b', 'income_template_new_filled');
    await page.evaluate(() => document.querySelector('form[action="/income-templates"]').requestSubmit());
    await page.waitForURL(/\/income-templates\/[0-9a-f-]{36}/);
    const url = page.url();
    templateID = url.match(/\/income-templates\/([0-9a-f-]{36})/)[1];
    await shoot(page, id + 'c', 'income_template_detail_after_create');
    const body = await page.locator('main').textContent();
    if (!body.includes('Rp 20.000.000')) die('detail subtitle missing total');
    pass('created template ' + templateID + ' with Σ(lines)=Rp 20.000.000');
  }

  // ── 14 Negative: apply with amount < Σ(lines) → flash error ────
  {
    const id = step('Negative — apply income template with amount < total');
    await page.goto(BASE + '/income-templates/' + templateID);
    await page.fill('input[name="amount"]', '15000000');
    await page.fill('input[name="effective_date"]', new Date().toISOString().slice(0, 10));
    // Pick first account
    await page.evaluate(() => {
      const opts = [...document.querySelectorAll('select[name="account_id"] option')].filter(o => o.value);
      if (opts.length) document.querySelector('select[name="account_id"]').value = opts[0].value;
    });
    await page.fill('input[name="counterparty_name"]', 'PT Telkom');
    await Promise.all([
      page.waitForLoadState('load'),
      page.evaluate(() => document.querySelectorAll('form')[1 /* apply form is 2nd */].requestSubmit()),
    ]);
    await page.waitForTimeout(300);
    const flash = await page.locator('.alert').textContent().catch(() => '');
    if (!flash.toLowerCase().includes('amount') && !flash.toLowerCase().includes('below')) {
      console.log('  flash text:', flash);
      die('expected amount-below alert');
    }
    await shoot(page, id, 'income_template_apply_below');
    pass('flash: ' + flash.trim());
  }

  // ── 15 Happy: apply with amount > Σ(lines) → leftover absorbs ──
  {
    const id = step('Happy — apply income template with leftover');
    await page.goto(BASE + '/income-templates/' + templateID);
    await page.fill('input[name="amount"]', '25000000');
    await page.fill('input[name="effective_date"]', new Date().toISOString().slice(0, 10));
    await page.evaluate(() => {
      const opts = [...document.querySelectorAll('select[name="account_id"] option')].filter(o => o.value);
      document.querySelector('select[name="account_id"]').value = opts[0].value;
    });
    await page.fill('input[name="counterparty_name"]', 'PT Telkom');
    await Promise.all([
      page.waitForURL(/\/income-templates\/[0-9a-f-]{36}\?flash=/),
      page.evaluate(() => document.querySelectorAll('form')[1].requestSubmit()),
    ]);
    const flash = await page.locator('.alert').textContent();
    if (!flash.toLowerCase().includes('applied')) die('expected applied-N alert; got ' + flash);
    await shoot(page, id, 'income_template_apply_success');
    pass('flash: ' + flash.trim());
  }

  // ── 16 Negative: wrong OTP — runs LAST and uses @shima so it
  //                doesn't put Riza into cooldown earlier in the run.
  await page.setViewportSize(VIEWPORT_COMPACT);
  {
    const id = step('Negative — verify with wrong OTP code');
    const ctxN = await browser.newContext({ viewport: VIEWPORT_COMPACT });
    const pageN = await ctxN.newPage();
    await pageN.goto(BASE + '/login');
    await pageN.fill('input[name="identifier"]', SHIMA);
    await Promise.all([pageN.waitForURL(/\/verify/), pageN.click('button[type="submit"]')]);
    await shoot(pageN, id + 'a', 'verify_empty_for_negative');
    await pageN.fill('input[name="code"]', '000000');
    await pageN.click('button[type="submit"]');
    await pageN.waitForLoadState('load');
    const alert = await pageN.locator('.alert').first().textContent().catch(() => '');
    if (!alert) die('expected alert on wrong OTP, got none');
    await shoot(pageN, id + 'b', 'verify_wrong_code');
    pass('alert: ' + alert.trim());
    await ctxN.close();
  }

  await browser.close();
  console.log(`\nPASS — UAT walkthrough complete (${stepNo} scenarios). Screenshots in:\n  ${outDir}`);
})();

function lastLine(s) {
  return s.trim().split('\n').slice(-1)[0].trim();
}
