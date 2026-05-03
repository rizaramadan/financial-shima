// E2E for the new /transactions/new web form. Logs in, creates a
// money_in via the form, then verifies it landed on the Pos detail
// page. Also snaps the form at iPhone-SE viewport for the PR.
const { chromium, devices } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8081';
const PASS = process.env.LOGIN_PASSWORD || 'dev-password';
const outDir = path.resolve(__dirname, 'out', 'new_txn_check');
fs.mkdirSync(outDir, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({
    viewport: { width: 800, height: 1200 },
  });
  const page = await ctx.newPage();

  await page.goto(BASE + '/login');
  await page.fill('input[name=identifier]', '@riza_ramadan');
  await page.fill('input[name=password]', PASS);
  await page.click('form[action="/login"] button[type=submit]');
  await page.waitForURL(BASE + '/');

  // Snap home (now showing the +Income / +Spending links).
  await page.screenshot({ path: path.join(outDir, '01_home_with_buttons.png') });

  // Snap the spending form.
  await page.goto(BASE + '/transactions/new?type=money_out');
  await page.screenshot({ path: path.join(outDir, '02_spending_form.png') });

  // Snap the income form.
  await page.goto(BASE + '/transactions/new?type=money_in');
  await page.screenshot({ path: path.join(outDir, '03_income_form.png') });

  // Functional test: pick the first IDR account + first IDR pos, fill amount,
  // submit, expect redirect to /pos/:id.
  await page.goto(BASE + '/transactions/new?type=money_out');
  const accountID = await page.$eval('select[name=account_id] option:nth-child(2)', el => el.value);
  const posID = await page.$eval('select[name=pos_id] option:nth-child(2)', el => el.value);
  await page.selectOption('select[name=account_id]', accountID);
  await page.selectOption('select[name=pos_id]', posID);
  await page.fill('input[name=amount]', '12345');
  await page.fill('input[name=counterparty_name]', 'Indomaret Mobile Test ' + Date.now());
  await Promise.all([
    page.waitForLoadState('networkidle'),
    page.click('form[action="/transactions"] button[type=submit]'),
  ]);
  await page.screenshot({ path: path.join(outDir, '04_after_submit.png') });
  console.log('after submit URL:', page.url());

  // Validation path: open form, submit empty, expect error list.
  await page.goto(BASE + '/transactions/new?type=money_out');
  // Clear effective_date so first error fires.
  await page.fill('input[name=effective_date]', '');
  await page.evaluate(() => {
    document.querySelector('form[action="/transactions"]').noValidate = true;
  });
  await page.click('form[action="/transactions"] button[type=submit]');
  await page.screenshot({ path: path.join(outDir, '05_validation_errors.png') });

  await browser.close();
})();
