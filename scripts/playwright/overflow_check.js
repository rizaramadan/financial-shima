// Reports any horizontal overflow at iPhone-SE viewport. For each
// page: (innerWidth, scrollWidth) and a list of elements whose right
// edge exceeds innerWidth (the actual culprits forcing pinch-zoom).
const { chromium, devices } = require('playwright');

const BASE = 'http://localhost:8081';
const PASS = process.env.LOGIN_PASSWORD || 'dev-password';

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

  await page.goto(BASE + '/login');
  await page.fill('input[name=identifier]', '@riza_ramadan');
  await page.fill('input[name=password]', PASS);
  await page.click('button[type=submit]');
  await page.waitForURL(BASE + '/');

  const urls = [
    '/', '/transactions', '/spending',
    '/income-templates', '/income-templates/new',
    '/notifications', '/settings', '/pos/new',
  ];
  for (const url of urls) {
    await page.goto(BASE + url);
    const report = await page.evaluate(() => {
      const innerWidth = window.innerWidth;
      const scrollWidth = document.documentElement.scrollWidth;
      const culprits = [];
      const all = document.querySelectorAll('*');
      for (const el of all) {
        const r = el.getBoundingClientRect();
        if (r.right > innerWidth + 1 && r.width > 0) {
          // Walk up — only report if no parent already overflows by a
          // larger amount. Otherwise we get noise.
          culprits.push({
            tag: el.tagName.toLowerCase(),
            cls: el.className && el.className.toString ? el.className.toString().slice(0, 60) : '',
            id:  el.id || '',
            right: Math.round(r.right),
            width: Math.round(r.width),
            text: (el.innerText || '').slice(0, 50).replace(/\s+/g, ' '),
          });
        }
      }
      // Sort by right edge desc; show top 8.
      culprits.sort((a, b) => b.right - a.right);
      return { innerWidth, scrollWidth, top: culprits.slice(0, 8) };
    });
    const overflow = report.scrollWidth - report.innerWidth;
    console.log(`\n${url}  inner=${report.innerWidth}  scroll=${report.scrollWidth}  overflow=${overflow}px`);
    for (const c of report.top) {
      console.log(`  ${c.tag}.${c.cls}${c.id ? '#'+c.id : ''}  right=${c.right}  width=${c.width}  "${c.text}"`);
    }
  }

  await browser.close();
})();
