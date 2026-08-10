// Captures the documentation screenshots of a running demo instance.
//
// Usage: node capture.mjs <job.json>
// The job file is written by internal/docsite (see shotJob there) and lists the
// base URL, the output directory and every shot to take.
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright';

const jobFile = process.argv[2];
if (!jobFile) {
  console.error('usage: node capture.mjs <job.json>');
  process.exit(2);
}

const job = JSON.parse(await readFile(jobFile, 'utf8'));
const baseURL = job.baseUrl.replace(/\/$/, '');
const origin = new URL(baseURL);

// Use the full Chromium in new-headless mode rather than the headless shell:
// it renders exactly like a headed browser, which is what the screenshots document.
const browser = await chromium.launch({ channel: 'chromium' });
try {
  for (const shot of job.shots) {
    const context = await browser.newContext({
      viewport: { width: job.width, height: job.height },
      deviceScaleFactor: job.deviceScaleFactor ?? 2,
      colorScheme: 'light',
      // A fixed locale and time zone keep the rendered dates reproducible.
      locale: 'de-DE',
      timezoneId: 'UTC',
      reducedMotion: 'reduce',
    });
    if (shot.private) {
      await context.addCookies([
        { name: 'forecast_private', value: '1', domain: origin.hostname, path: '/' },
      ]);
    }
    const page = await context.newPage();
    const response = await page.goto(baseURL + shot.path, { waitUntil: 'networkidle' });
    if (!response || !response.ok()) {
      throw new Error(`${shot.path} responded with ${response ? response.status() : 'no response'}`);
    }
    const target = shot.selector ? page.locator(shot.selector).first() : page;
    if (shot.selector) {
      await target.waitFor({ state: 'visible' });
    }
    const file = `${job.outDir}/${shot.file}`;
    await target.screenshot({
      path: file,
      ...(shot.selector ? {} : { fullPage: !!shot.fullPage }),
      animations: 'disabled',
      scale: 'device',
    });
    console.log(`captured ${shot.file}`);
    await context.close();
  }
} finally {
  await browser.close();
}
