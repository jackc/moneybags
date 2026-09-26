import { test, expect } from '@playwright/test';

// Service workers are registered by the production build, not Vite's dev server.
test.use({ baseURL: `http://localhost:${process.env.TEST_BACKEND_PORT || '4001'}` });

test('production app provides an installable manifest and phone icons', async ({
  page,
  context
}) => {
  await page.goto('/');
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute(
    'href',
    '/manifest.webmanifest'
  );
  await expect(page.locator('meta[name="apple-mobile-web-app-capable"]')).toHaveAttribute(
    'content',
    'yes'
  );
  const manifestResponse = await page.request.get('/manifest.webmanifest');
  expect(manifestResponse.headers()['content-type']).toContain('manifest+json');
  const manifest = await manifestResponse.json();
  expect(manifest.display).toBe('standalone');
  expect(manifest.start_url).toBe('/');
  expect(manifest.scope).toBe('/');
  for (const icon of [...manifest.icons, { src: '/apple-touch-icon.png', sizes: '180x180' }]) {
    const dimensions = await page.evaluate(async (src) => {
      const image = new Image();
      image.src = src;
      await image.decode();
      return `${image.naturalWidth}x${image.naturalHeight}`;
    }, icon.src);
    expect(dimensions).toBe(icon.sizes);
  }
  await page.evaluate(() => navigator.serviceWorker.ready);
  const client = await context.newCDPSession(page);
  const { installabilityErrors } = await client.send('Page.getInstallabilityErrors');
  await client.detach();
  // Playwright's isolated contexts are incognito, where Chrome disables installs.
  // All manifest/site requirements must still pass.
  expect(installabilityErrors.filter((error) => error.errorId !== 'in-incognito')).toEqual([]);
});

test('production service worker shows offline recovery and leaves private requests on the network', async ({
  page,
  context
}) => {
  await page.goto('/');
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload();
  await page.waitForFunction(() => navigator.serviceWorker.controller !== null);

  const cachedPaths = await page.evaluate(async () => {
    const paths = [];
    for (const key of await caches.keys()) {
      if (!key.startsWith('moneybags-')) continue;
      const cache = await caches.open(key);
      paths.push(...(await cache.keys()).map((request) => new URL(request.url).pathname));
    }
    return paths;
  });
  expect(cachedPaths).toContain('/offline.html');
  expect(cachedPaths.some((path) => /^\/(api|oauth|mcp|\.well-known)(\/|$)/.test(path))).toBe(
    false
  );

  await context.setOffline(true);
  await page.reload();
  await expect(page.getByRole('heading', { name: "You're offline" })).toBeVisible();
  for (const path of [
    '/api/attachments/private',
    '/oauth/authorize',
    '/mcp',
    '/.well-known/oauth-authorization-server'
  ]) {
    expect(
      await page.evaluate(async (path) => {
        try {
          await fetch(path);
          return 'response';
        } catch {
          return 'network error';
        }
      }, path)
    ).toBe('network error');
  }
  await context.setOffline(false);
  await page.getByRole('link', { name: 'Try again' }).click();
  await expect(page.getByRole('button', { name: 'Create an account', exact: true })).toBeVisible();
});
