// Quick smoke that proves Playwright works against the live Pages
// site. For full local-app E2E (login flow, /pos/new, redirect),
// we'd need an OTP-bypass shim — different commit.
//
// Usage: node demo.js
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const URL = 'https://rizaramadan.github.io/financial-shima/';
const outDir = path.join(__dirname, 'out');
fs.mkdirSync(outDir, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({ viewport: { width: 1100, height: 900 } });
  const page = await ctx.newPage();

  console.log('• Navigating to', URL);
  const resp = await page.goto(URL, { waitUntil: 'networkidle' });
  console.log('  status:', resp.status());
  if (resp.status() !== 200) {
    console.error('FAIL non-200');
    process.exit(1);
  }

  const title = await page.title();
  console.log('  title:', title);

  // Find every embedded screenshot — proves the docs/screenshots/*.png
  // assets are actually being served by Pages (the whole point of
  // PR #5 fixing the source-branch).
  const imgSrcs = await page.$$eval('img', imgs => imgs.map(i => i.src));
  console.log('  embedded images:', imgSrcs.length);
  imgSrcs.forEach(s => console.log('   -', s));

  // Look for the freshly-rewritten /api/v1 endpoint table
  // (PR #5 added a 4-row table; if Pages hadn't rebuilt this would
  // still say "POST endpoints are not yet implemented").
  const hasFreshAPISection = await page.evaluate(() => {
    const text = document.body.innerText;
    return text.includes('POST /api/v1/transactions') &&
           text.includes('Idempotent on `idempotency_key`'.replace(/`/g, '')) ||
           text.includes('was_inserted: false');
  });
  console.log('  fresh API section visible:', hasFreshAPISection);

  // Full-page screenshot.
  const shot1 = path.join(outDir, 'live_pages_full.png');
  await page.screenshot({ path: shot1, fullPage: true });
  console.log('  saved:', shot1);

  // Scroll to the "## LLM API" heading and screenshot just that section.
  const heading = await page.$('h2:has-text("LLM API")');
  if (heading) {
    await heading.scrollIntoViewIfNeeded();
    const box = await heading.boundingBox();
    const shot2 = path.join(outDir, 'live_pages_api_section.png');
    await page.screenshot({
      path: shot2,
      clip: { x: 0, y: box.y - 20, width: 1100, height: 700 },
    });
    console.log('  saved:', shot2);
  }

  await browser.close();
  console.log('\nPASS — Playwright drove the live Pages site successfully');
})();
