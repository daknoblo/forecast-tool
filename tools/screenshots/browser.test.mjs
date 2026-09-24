import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from 'playwright';
import { expect } from 'playwright/test';

const baseURL = process.env.FORECAST_BROWSER_URL;
const scenario = process.env.FORECAST_BROWSER_SCENARIO;
const month = process.env.FORECAST_BROWSER_MONTH;
assert.ok(baseURL && scenario, 'Run through TestBrowserRegression to use isolated Go fixtures.');

async function checkKPIAlignment(page) {
  for (const width of [360, 760, 761, 1280, 1281, 1920, 2400, 2401, 2560]) {
    await page.setViewportSize({ width, height: 1000 });
    const geometry = await page.locator('.kpi-row > .kpi').evaluateAll(cards =>
      cards.map(card => {
        const box = card.getBoundingClientRect();
        const split = card.querySelector('.kpi-split');
        const values = split ? [...split.querySelectorAll('.kpi-part')] : [card.querySelector('.kpi-value')];
        const captions = split ? [...split.querySelectorAll('small')] : [card.querySelector('.kpi-label')];
        const headings = [...card.querySelectorAll('.kpi-heading')];
        return {
          top: box.top,
          headingTop: headings.map(el => el.getBoundingClientRect().top),
          headingBottom: headings.map(el => el.getBoundingClientRect().bottom),
          valueTop: values.map(el => el.getBoundingClientRect().top),
          captionTop: captions.map(el => el.getBoundingClientRect().top),
          textOffsets: [...headings, ...values, ...captions].map(el => {
            const range = document.createRange();
            range.selectNodeContents(el);
            const text = range.getBoundingClientRect();
            const area = el.getBoundingClientRect();
            return Math.abs(text.x + text.width / 2 - area.x - area.width / 2);
          }),
        };
      }),
    );
    assert.equal(geometry.length, 8);
    for (const card of geometry) {
      const sameRow = geometry.filter(other => Math.abs(other.top - card.top) < 1);
      const columns = width <= 760 ? 1 : width <= 1280 ? 2 : width <= 2400 ? 4 : 8;
      assert.equal(sameRow.length, columns, `wrong number of KPI columns at ${width}px`);
      for (const key of ['headingTop', 'valueTop', 'captionTop']) {
        const positions = sameRow.flatMap(other => other[key]);
        if (positions.length) assert.ok(Math.max(...positions) - Math.min(...positions) <= 1, `${key} misaligned at ${width}px`);
      }
      assert.ok(card.textOffsets.every(offset => offset <= 1), `KPI text not centered at ${width}px`);
      assert.ok(card.headingBottom.every(bottom => bottom <= Math.min(...card.valueTop) + 1), `heading below figure at ${width}px`);
    }
    assert.ok(await page.locator('.kpi-row').evaluate(el => el.scrollWidth <= el.clientWidth + 1), `KPI overflow at ${width}px`);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
}

async function dashboard(page) {
  await page.goto('/');
  await expect(page.locator('nav a')).toHaveText(['Dashboard', 'Projekte', 'Monatsplanung', 'Ziele', 'Einstellungen']);
  await expect(page.locator('#forecast-accuracy')).toHaveAttribute('title', /Noch kein ESXP-Wert/);
  await checkKPIAlignment(page);
  const year = await page.locator('#fySelect').inputValue();
  const asOf = new Date().toISOString().slice(0, 10);
  const imported = await page.request.put(`/api/v1/forecast-accuracy/${year}`, {
    headers: { Authorization: 'Bearer browser-fixture-write' },
    data: { percentage: 84.5, asOf },
  });
  assert.equal(imported.status(), 200, await imported.text());
  await page.reload();
  await expect(page.locator('#forecast-accuracy')).toContainText('84.5 %');
  assert.ok(!(await page.locator('#forecast-accuracy').innerText()).includes('Stand '), 'date must remain tooltip-only');
  await checkKPIAlignment(page);
  const link = page.locator('.kpi-row a[href^="/month?month="]');
  const target = await link.getAttribute('href');
  await link.click();
  await expect(page).toHaveURL(new URL(target, baseURL).href);
  await expect(page.locator(new URL(target, baseURL).hash)).toHaveCount(1);
  await expect(page.locator('nav a.active')).toHaveText('Monatsplanung');
  for (const path of ['/week', '/week/1', '/week/cells']) {
    assert.equal((await page.request.get(path)).status(), 404);
    assert.equal((await page.request.post(path, { data: {} })).status(), 404);
  }
  await page.goto('/');
  await page.locator('.privbtn').click();
  await expect(page.locator('.privnote')).toBeVisible();
  await expect(page.locator('#forecast-accuracy')).not.toContainText('84.5 %');
  await expect(page.locator('#forecast-accuracy')).toContainText('90 %');
}

async function monthly(page) {
  await page.goto(`/month?month=${month}`);
  const activity = page.locator('#activity');
  const prompts = page.locator('.month-method');
  const generate = page.locator('#month-generate');
  await expect(activity).toHaveAttribute('aria-busy', 'false');
  const rows = await page.locator('.month-calendar tbody tr').evaluateAll(elements =>
    elements.map(row => ({ days: row.querySelectorAll('td').length, summaries: row.querySelectorAll('th').length })),
  );
  assert.ok(rows.length >= 4 && rows.every(row => row.days === 5 && row.summaries === 1), 'calendar must show weekdays plus one weekly summary');
  const week = page.locator('.month-week').filter({ hasText: 'Projekt Pattern' }).first();
  await expect(week.locator('dt')).toHaveText(['Kapazität:', 'Urlaub/Feiertage', 'Projekt Pattern', 'Gesamt gebucht', 'Verfügbar']);
  await expect(week.locator('dd')).toHaveText(['36 h', '4 h', '8 h', '8 h', '28 h']);
  await expect(week.locator('.month-over')).toHaveCount(0);
  assert.equal(await week.locator('.month-project-total').first().evaluate(el => el.style.getPropertyValue('--project-color').trim()), '#32cd32');
  assert.equal(await prompts.getAttribute('open'), null);
  await prompts.locator(':scope > summary').click();
  await expect(prompts.locator('details')).toHaveAttribute('open', '');
  for (const id of ['month-prompt', 'month-system-prompt']) {
    await expect.poll(() => page.locator(`#${id}`).evaluate(el => el.scrollHeight <= el.clientHeight + 1), { message: `${id} clips text` }).toBe(true);
  }
  assert.deepEqual(await page.evaluate(() => {
    const states = [];
    const first = window.beginActivity();
    const second = window.beginActivity();
    first();
    first();
    states.push(document.getElementById('activity').getAttribute('aria-busy'));
    second();
    states.push(document.getElementById('activity').getAttribute('aria-busy'));
    return states;
  }), ['true', 'false'], 'concurrent activity completion must be idempotent');
  const prompt = 'Plane das vorhandene Projekt.\n' + 'Eine eigene Zeile zur Tagesplanung.\n'.repeat(24);
  const savedPrompt = page.waitForResponse(r => r.url().endsWith('/month/prompt') && r.request().method() === 'POST');
  await page.locator('#month-prompt').fill(prompt);
  await page.locator('#month-system-prompt').focus();
  assert.equal((await savedPrompt).status(), 204);
  await expect(activity).toHaveAttribute('aria-busy', 'false');
  assert.ok(await page.locator('#month-prompt').evaluate(el => el.scrollHeight <= el.clientHeight + 1));
  await page.reload();
  await prompts.locator(':scope > summary').click();
  await expect(page.locator('#month-prompt')).toHaveValue(prompt.trim());

  if (scenario === 'month') {
    let release;
    const gate = new Promise(resolve => { release = resolve; });
    const raw = 'MODEL-ONLY </textarea><script>window.modelInjected=true</script>';
    const failed = async route => {
      await gate;
      await route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ error: 'Ungültige KI-Antwort.', modelResponse: raw }) });
    };
    await page.route('**/month/generate', failed);
    try {
      await generate.click();
      await expect(activity).toHaveAttribute('aria-busy', 'true');
      await expect(generate).toBeDisabled();
      await expect(page.locator('#month-prompt')).toHaveAttribute('readonly', '');
      assert.notEqual(await page.locator('.activity-spinner').evaluate(el => getComputedStyle(el).animationName), 'none');
      await page.emulateMedia({ reducedMotion: 'reduce' });
      assert.equal(await page.locator('.activity-spinner').evaluate(el => getComputedStyle(el).animationName), 'none');
    } finally {
      release();
    }
    await expect(page.locator('#month-ai-status')).toHaveText('Ungültige KI-Antwort.');
    await expect(activity).toHaveAttribute('aria-busy', 'false');
    await expect(generate).toBeEnabled();
    await expect(page.locator('#month-response')).toHaveValue(raw);
    await expect(page.locator('#month-response')).toHaveAttribute('readonly', '');
    assert.equal(await page.evaluate(() => window.modelInjected), undefined);
    await page.unroute('**/month/generate', failed);
    await page.emulateMedia({ reducedMotion: 'no-preference' });
  }

  const original = await page.request.get('/api/v1/data', { headers: { Authorization: 'Bearer browser-fixture-write' } });
  assert.equal(original.status(), 200);
  const entriesBefore = (await original.json()).entries;
  await generate.click();
  await expect(page).toHaveURL(/preview=/);
  await expect(page.locator('.month-preview h2')).toHaveText('Planungsübersicht');
  await expect(page.locator('.month-explanation li')).toHaveCount(2);
  await expect(page.locator('.month-preview input[type="checkbox"]')).toHaveCount(0);
  await prompts.locator(':scope > summary').click();
  const rawPlan = JSON.parse(await page.locator('#month-response').inputValue());
  assert.equal(rawPlan.entries.length, scenario === 'month' ? 4 : 3);
  for (const width of [390, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const id of ['month-prompt', 'month-system-prompt', 'month-response']) {
      await expect.poll(() => page.locator(`#${id}`).evaluate(el => el.scrollHeight <= el.clientHeight + 1)).toBe(true);
    }
  }
  const preview = await page.request.get('/api/v1/data', { headers: { Authorization: 'Bearer browser-fixture-write' } });
  assert.deepEqual((await preview.json()).entries, entriesBefore, 'preview must not write bookings');
  const save = page.locator('#month-save button');
  if (scenario === 'month-unallocated') {
    await expect(save).toBeDisabled();
    await expect(page.locator('.month-unallocated')).toContainText('Kein passender Block im Projektzeitraum.');
    await expect(page.locator('.month-unallocated')).toContainText(/FYW \d+/);
    await expect(page.locator('.month-unallocated')).toContainText('2 h');
    await page.getByRole('link', { name: 'Vorschau verwerfen' }).click();
    await expect(page.locator('.month-preview')).toHaveCount(0);
    return;
  }
  await save.click();
  await expect(page).not.toHaveURL(/preview=/);
  await expect(page.locator('.month-calendar caption')).toContainText('Gespeicherte Tagesverteilung');
  await page.reload();
  await expect(page.locator('.month-event.estimated')).toHaveCount(0);
  const current = await page.request.get('/api/v1/data', { headers: { Authorization: 'Bearer browser-fixture-write' } });
  const saved = (await current.json()).entries;
  for (const entry of rawPlan.entries) {
    assert.ok(saved.some(actual => actual.date === entry.date && actual.projectId === entry.projectId && actual.hours === entry.hours));
  }
  await page.locator('.privbtn').click();
  await expect(page.locator('.privnote')).toBeVisible();
  await expect(generate).toBeDisabled();
  await prompts.locator(':scope > summary').click();
  await expect(page.locator('#month-prompt')).toBeDisabled();
  await expect(page.locator('#month-system-prompt')).toBeDisabled();
  await expect(page.locator('#month-response-field')).toBeHidden();
}

test(scenario, { timeout: 100_000 }, async () => {
  const browser = await chromium.launch();
  try {
    const context = await browser.newContext({ baseURL, viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    if (scenario.startsWith('dashboard-')) await dashboard(page);
    else await monthly(page);
    assert.deepEqual(errors, [], 'uncaught browser JavaScript errors');
    await context.close();
  } finally {
    await browser.close();
  }
});
