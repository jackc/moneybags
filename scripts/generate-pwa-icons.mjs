// Regenerate the phone icons from the existing vector logo:
// node scripts/generate-pwa-icons.mjs
import { readFile, writeFile } from 'node:fs/promises';
import { chromium } from '@playwright/test';

const staticDir = new URL('../static/', import.meta.url);
const svg = await readFile(new URL('favicon.svg', staticDir), 'utf8');
const browser = await chromium.launch({
  channel: 'chromium',
  ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
    ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE }
    : {})
});
try {
  const page = await browser.newPage();
  for (const [name, size, scale] of [
    ['apple-touch-icon.png', 180, 1],
    ['icon-192.png', 192, 1],
    ['icon-512.png', 512, 1],
    ['icon-512-maskable.png', 512, 0.8]
  ]) {
    const data = await page.evaluate(
      async ({ svg, size, scale }) => {
        const image = new Image();
        image.src = `data:image/svg+xml,${encodeURIComponent(svg)}`;
        await image.decode();
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = size;
        const context = canvas.getContext('2d');
        context.fillStyle = '#174b39';
        context.fillRect(0, 0, size, size);
        const inset = (size * (1 - scale)) / 2;
        context.drawImage(image, inset, inset, size * scale, size * scale);
        return canvas.toDataURL('image/png').split(',')[1];
      },
      { svg, size, scale }
    );
    await writeFile(new URL(name, staticDir), Buffer.from(data, 'base64'));
  }
} finally {
  await browser.close();
}
