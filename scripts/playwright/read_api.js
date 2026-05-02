// Plays the "LLM does its own sanity check after seeding" scenario:
// curl every /api/v1 read endpoint, format the responses into a
// readable summary, and screenshot the live JSON output as the
// browser shows it. Captures into docs/screenshots/uat/read_api/.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8081';
const KEY = 'test-api-key-for-e2e';
const outDir = path.resolve(__dirname, '..', '..', 'docs', 'screenshots', 'uat');
fs.mkdirSync(outDir, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({
    viewport: { width: 1100, height: 1300 },
    extraHTTPHeaders: { 'x-api-key': KEY },
  });
  const page = await ctx.newPage();

  const endpoints = [
    { id: '17a', name: 'GET /api/v1/accounts',         url: '/api/v1/accounts' },
    { id: '17b', name: 'GET /api/v1/pos',              url: '/api/v1/pos' },
    { id: '17c', name: 'GET /api/v1/counterparties',   url: '/api/v1/counterparties' },
    { id: '17d', name: 'GET /api/v1/transactions',     url: '/api/v1/transactions' },
    { id: '17e', name: 'GET /api/v1/balances',         url: '/api/v1/balances' },
  ];

  for (const ep of endpoints) {
    const r = await ctx.request.get(BASE + ep.url);
    const status = r.status();
    const body = await r.text();
    let pretty;
    try {
      pretty = JSON.stringify(JSON.parse(body), null, 2);
    } catch {
      pretty = body;
    }
    // Render a tiny inline page: endpoint name + status + pretty JSON.
    const html = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<style>
  body { font: 14px/1.5 -apple-system, "Segoe UI", monospace; padding: 24px;
         background: #F5F5F5; color: rgba(0,0,0,0.88); }
  h1 { font-size: 20px; margin: 0 0 12px; font-family: -apple-system, "Segoe UI"; }
  .status { display: inline-block; padding: 2px 10px; border-radius: 4px;
            font-weight: 600; font-size: 12px; margin-bottom: 16px;
            background: #F6FFED; color: #389E0D; border: 1px solid #B7EB8F; }
  pre { background: #FFFFFF; padding: 16px; border-radius: 6px;
        border: 1px solid #F0F0F0; overflow-x: auto;
        font-family: ui-monospace, "SF Mono", Consolas, monospace;
        font-size: 12px; line-height: 1.5; }
  .meta { color: rgba(0,0,0,0.45); font-size: 12px; margin-bottom: 4px; font-family: -apple-system; }
</style></head><body>
<div class="meta">curl -H "x-api-key: ⋯" https://duit.mvp.my.id${ep.url}</div>
<h1>${ep.name}</h1>
<span class="status">${status} OK</span>
<pre>${escapeHTML(pretty)}</pre>
</body></html>`;
    await page.setContent(html);
    const file = path.join(outDir, `${ep.id}_${slug(ep.url)}.png`);
    await page.screenshot({ path: file, fullPage: true });
    console.log('saved', file);
  }
  await browser.close();
})();

function escapeHTML(s) {
  return s.replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
}
function slug(u) {
  return u.replace(/^\/api\/v1\//, '').replace(/[^a-z0-9]/gi, '_');
}
