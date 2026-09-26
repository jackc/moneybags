import { test, expect } from '@playwright/test';

const password = 'A reliable test password 123!';
const username = () => `browser_${crypto.randomUUID().slice(0, 12)}`;

async function register(page, family = 'The Green household') {
  const name = username();
  await page.goto('/');
  await page.getByRole('button', { name: 'Create an account', exact: true }).click();
  await page.getByLabel('Your name').fill('Alex Green');
  await page.getByLabel('Username', { exact: true }).fill(name);
  await page.getByLabel('Password', { exact: false }).fill(password);
  await page.getByLabel('Family name').fill(family);
  await page.getByRole('button', { name: 'Create account', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Your bags', exact: true })).toBeVisible();
  return name;
}

async function createBag(page, name, amount = '') {
  await page.getByRole('button', { name: /^(Create your first bag|\+ New bag)$/ }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Bag name').fill(name);
  if (amount !== '') await dialog.getByLabel('Starting amount (USD)').fill(amount);
  await dialog.getByRole('button', { name: 'Create bag', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
}

async function entry(page, amount, notes, mode = 'expense', files = []) {
  await page
    .getByRole('button', {
      name: mode === 'expense' ? '− Record expense' : '+ Add money',
      exact: true
    })
    .click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Amount (USD)').fill(amount);
  await dialog.getByLabel('Notes').fill(notes);
  if (files.length) await dialog.getByLabel('Attach files').setInputFiles(files);
  await dialog
    .getByRole('button', { name: mode === 'expense' ? 'Record expense' : 'Add money', exact: true })
    .click();
  await expect(dialog).toHaveCount(0);
}

test('spending, zero notes, safe Markdown, multiple files, edits, archives, and deletion', async ({
  page
}) => {
  await register(page);
  await createBag(page, 'Groceries', '1000.00');
  await page
    .getByRole('link')
    .filter({ has: page.getByRole('heading', { name: 'Groceries', exact: true }) })
    .click();
  await expect(page.locator('.hero-balance')).toHaveText('$1,000.00');
  await entry(
    page,
    '87.32',
    'Saturday shopping\n\n| Item | Printed amount |\n| --- | ---: |\n| Coffee | $12.00 |\n\n<script>window.markdownExecuted=true</script>\n[unsafe](javascript:alert(1))',
    'expense',
    [
      {
        name: 'receipt-front.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('First independent attachment')
      },
      {
        name: 'receipt-back.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('Second independent attachment')
      }
    ]
  );
  await expect(page.locator('.hero-balance')).toHaveText('$912.68');
  await entry(page, '0.00', 'Remember the reusable bags');
  await expect(page.locator('.hero-balance')).toHaveText('$912.68');
  await expect(page.locator('.activity-row').first()).toContainText('Remember the reusable bags');
  await page.getByRole('link').filter({ hasText: 'Saturday shopping' }).click();
  await expect(page.locator('.entry-card .markdown table')).toBeVisible();
  await expect(page.locator('.markdown script')).toHaveCount(0);
  expect(await page.evaluate(() => window.markdownExecuted)).toBeUndefined();
  await expect(page.getByRole('link', { name: 'receipt-front.txt' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'receipt-back.txt' })).toBeVisible();
  const download = page.waitForEvent('download');
  await page.getByRole('link', { name: 'receipt-front.txt' }).click();
  expect((await download).suggestedFilename()).toBe('receipt-front.txt');
  await page.getByRole('button', { name: 'Edit entry', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Amount (USD)').fill('1000.29');
  await dialog
    .locator('li')
    .filter({ hasText: 'receipt-front.txt' })
    .getByRole('button', { name: 'Remove' })
    .click();
  await dialog.getByRole('button', { name: 'Save changes' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'receipt-front.txt' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'receipt-back.txt' })).toBeVisible();
  await expect(page.getByText('Version 2', { exact: false })).toBeVisible();
  await page.getByRole('link', { name: '← Groceries', exact: true }).click();
  await expect(page.locator('.hero-balance')).toHaveText('−$0.29');
  await expect(page.getByText('Negative balance · spending exceeds money added')).toBeVisible();
  await page.getByRole('button', { name: 'Archive', exact: true }).click();
  await expect(page.getByRole('button', { name: '− Record expense', exact: true })).toBeDisabled();
  await page.getByRole('link').filter({ hasText: 'Saturday shopping' }).click();
  await expect(page.getByRole('button', { name: 'Edit entry' })).toBeDisabled();
  await page.getByRole('button', { name: 'Delete entry' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete entry' }).click();
  await expect(page.locator('.hero-balance')).toHaveText('$1,000.00');
  await page.getByRole('button', { name: 'Unarchive', exact: true }).click();
  await expect(page.getByRole('button', { name: '− Record expense', exact: true })).toBeEnabled();
});

test('invited family members have equal access and account deletion preserves entries', async ({
  page,
  browser
}) => {
  await register(page, 'A shared household');
  await createBag(page, 'Weekends', '75');
  await page.getByRole('button', { name: 'Manage pins', exact: true }).click();
  await page.getByRole('button', { name: 'Pin Weekends', exact: true }).click();
  await expect(page.locator('.pin-caption')).toHaveText('Pinned');
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByRole('button', { name: '+ Invite someone' }).click();
  const url = await page.getByLabel('Share this single-use invitation').inputValue();
  const secondContext = await browser.newContext();
  const second = await secondContext.newPage();
  await second.route('**/api/settings', (route) =>
    route.fulfill({ json: { allow_registration: false } })
  );
  await second.goto(url);
  await second.getByLabel('Your name').fill('Sam Green');
  await second.getByLabel('Username', { exact: true }).fill(username());
  await second.getByLabel('Password', { exact: false }).fill(password);
  await second.getByRole('button', { name: 'Create account', exact: true }).click();
  await expect(second.getByRole('heading', { name: 'Your bags', exact: true })).toBeVisible();
  await expect(second.locator('.pin-caption')).toHaveCount(0);
  await second
    .getByRole('link')
    .filter({ has: second.getByRole('heading', { name: 'Weekends', exact: true }) })
    .click();
  await entry(second, '12.50', 'A family picnic');
  await page.getByRole('link', { name: 'Your bags' }).click();
  await page
    .getByRole('link')
    .filter({ has: page.getByRole('heading', { name: 'Weekends', exact: true }) })
    .click();
  await expect(page.locator('.hero-balance')).toHaveText('$62.50');
  await expect(page.locator('.activity-row').first()).toContainText('Sam Green');
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page
    .locator('.management-list li')
    .filter({ hasText: 'Sam Green' })
    .getByRole('button', { name: 'Delete account' })
    .click();
  await page.getByLabel('Type DELETE to confirm').fill('DELETE');
  await page.getByRole('dialog').getByRole('button', { name: 'Delete account' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await second.reload();
  await expect(second.getByRole('heading', { name: 'Open your bags' })).toBeVisible();
  await page.getByRole('link', { name: 'Your bags' }).click();
  await page
    .getByRole('link')
    .filter({ has: page.getByRole('heading', { name: 'Weekends', exact: true }) })
    .click();
  await expect(page.locator('.activity-row').first()).toContainText('Sam Green');
  await secondContext.close();
});

for (const settingsState of ['disabled', 'unavailable']) {
  test(`registration ${settingsState} hides signup and preserves login`, async ({
    page,
    context
  }) => {
    const name = await register(page);
    await context.clearCookies();
    await page.route('**/api/settings', (route) =>
      settingsState === 'disabled'
        ? route.fulfill({ json: { allow_registration: false } })
        : route.fulfill({ status: 503, json: { error: 'Unavailable' } })
    );
    for (const path of ['/', '/register']) {
      const settings = page.waitForResponse('**/api/settings');
      await page.goto(path);
      await settings;
      await expect(page.getByRole('heading', { name: 'Open your bags' })).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Create an account', exact: true })
      ).toHaveCount(0);
      await expect(page.getByLabel('Family name')).toHaveCount(0);
    }
    await page.getByLabel('Username', { exact: true }).fill(name);
    await page.getByLabel('Password', { exact: false }).fill(password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Your bags', exact: true })).toBeVisible();
  });
}

test('passkey enrollment, password logout, passkey login, and recent-auth removal', async ({
  page,
  context
}) => {
  const client = await context.newCDPSession(page);
  await client.send('WebAuthn.enable');
  await client.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true
    }
  });
  await register(page);
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByRole('button', { name: 'Account & security' }).click();
  await page.getByLabel('Passkey name').fill('Browser authenticator');
  await page.getByRole('button', { name: 'Add passkey', exact: true }).click();
  await expect(page.getByText('Browser authenticator', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await page.getByRole('button', { name: 'Sign in with a passkey', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Your bags', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByRole('button', { name: 'Account & security' }).click();
  await page
    .locator('.management-list li')
    .filter({ hasText: 'Browser authenticator' })
    .getByRole('button', { name: 'Remove' })
    .click();
  await page.getByRole('dialog').getByLabel('Password').fill(password);
  await page.getByRole('dialog').getByRole('button', { name: 'Remove passkey' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByText('Browser authenticator', { exact: true })).toHaveCount(0);
});

test('mobile layout, exact decimal validation, keyboard dialogs, and dashboard screenshot', async ({
  page
}, testInfo) => {
  await register(page);
  await createBag(page, 'Groceries', '870.18');
  await createBag(page, 'Weekends', '245.00');
  await createBag(page, 'A rainy day', '1250.00');
  await createBag(page, 'Little luxuries', '80.50');
  await page.getByRole('button', { name: 'Dismiss notification' }).click();
  await page.screenshot({ path: testInfo.outputPath('dashboard-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('heading', { name: 'Your bags', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Record expense in Groceries', exact: true }).click();
  await expect(
    page
      .getByRole('dialog')
      .getByRole('combobox', { name: 'Bag', exact: true })
      .locator('option:checked')
  ).toHaveText('Groceries');
  await page.getByRole('dialog').getByLabel('Amount (USD)').fill('1.001');
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Record expense', exact: true })
    .click();
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText(
    'at most two decimal places'
  );
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('dashboard-mobile.png'), fullPage: true });
});

test('password login, password change, and family settings', async ({ page }) => {
  const name = await register(page);
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await page.getByLabel('Username', { exact: true }).fill(name);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByLabel('Family name').fill('Our shared plan');
  await page.getByRole('textbox', { name: /^Time zone/ }).fill('America/Los_Angeles');
  await page.getByRole('button', { name: 'Save family settings' }).click();
  await expect(page.getByRole('status')).toHaveText('Family settings updated.');
  await page.getByRole('button', { name: 'Account & security' }).click();
  await page.getByLabel('Current password').fill(password);
  await page.getByLabel('New password').fill(password + ' changed');
  await page.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('Password changed.');
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await page.getByLabel('Username', { exact: true }).fill(name);
  await page.getByLabel('Password', { exact: true }).fill(password + ' changed');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByText('Our shared plan · SHARED SPENDING')).toBeVisible();
});

test('production bundle hydrates under the Go server security policy', async ({ page }) => {
  const violations = [];
  page.on('console', (message) => {
    if (/Content Security Policy|Refused to execute/i.test(message.text()))
      violations.push(message.text());
  });
  await page.goto(`http://127.0.0.1:${process.env.TEST_BACKEND_PORT || '4001'}/`);
  await expect(page.getByRole('heading', { name: 'Open your bags' })).toBeVisible();
  await page.getByRole('button', { name: 'Create an account', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Make room for a plan' })).toBeVisible();
  expect(violations).toEqual([]);
});

test('personal pins order one complete list across devices and expenses start in the chosen bag', async ({
  page,
  browser
}) => {
  const name = await register(page);
  await expect(page.getByRole('button', { name: 'Create your first bag' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Manage pins', exact: true })).toHaveCount(0);
  await createBag(page, 'Groceries', '100');
  await createBag(page, 'Car', '50');
  await createBag(page, 'Eating out', '25');
  const headings = page.locator('.home-bag h2');
  await expect(headings).toHaveText(['Car', 'Eating out', 'Groceries']);
  await expect(page.locator('.overview')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Add money', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Record expense', exact: true })).toHaveCount(0);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Manage pins', exact: true }).click();
  await page.route(
    '**/api/actions/set_bag_pin',
    (route) =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: { message: 'Could not save your pin.' } })
      }),
    { times: 1 }
  );
  await page.getByRole('button', { name: 'Pin Groceries', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Could not save your pin.');
  await expect(headings).toHaveText(['Car', 'Eating out', 'Groceries']);
  await page.getByRole('button', { name: 'Pin Groceries', exact: true }).click();
  await expect(headings).toHaveText(['Groceries', 'Car', 'Eating out']);
  await expect(page.getByRole('button', { name: 'Unpin Groceries', exact: true })).toBeFocused();
  // Pinning a second bag must sort by name, not the order of the pin clicks.
  await page.getByRole('button', { name: 'Pin Eating out', exact: true }).click();
  await expect(headings).toHaveText(['Eating out', 'Groceries', 'Car']);
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await page.reload();
  await expect(headings).toHaveText(['Eating out', 'Groceries', 'Car']);

  const otherContext = await browser.newContext({ baseURL: page.url() });
  try {
    const otherDevice = await otherContext.newPage();
    await otherDevice.goto('/');
    await otherDevice.getByLabel('Username', { exact: true }).fill(name);
    await otherDevice.getByLabel('Password', { exact: true }).fill(password);
    await otherDevice.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(otherDevice.locator('.home-bag h2')).toHaveText([
      'Eating out',
      'Groceries',
      'Car'
    ]);
  } finally {
    await otherContext.close();
  }

  await page.getByRole('button', { name: 'Record expense in Car', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(
    dialog.getByRole('combobox', { name: 'Bag', exact: true }).locator('option:checked')
  ).toHaveText('Car');
  await dialog.getByLabel('Amount (USD)').fill('12.50');
  await dialog.getByRole('button', { name: 'Record expense', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('article', { name: 'Car', exact: true })).toContainText('$37.50');
  await expect(page.getByRole('article', { name: 'Groceries', exact: true })).toContainText(
    '$100.00'
  );

  await page
    .getByRole('link')
    .filter({ has: page.getByRole('heading', { name: 'Groceries', exact: true }) })
    .click();
  await page.getByRole('button', { name: 'Archive', exact: true }).click();
  await page.getByRole('link', { name: 'Your bags', exact: true }).click();
  await expect(headings).toHaveText(['Eating out', 'Car']);
  await page.getByRole('button', { name: 'Archived bags (1)', exact: true }).click();
  await expect(headings).toHaveText(['Groceries']);
  await expect(page.getByRole('button', { name: /Record expense in/ })).toHaveCount(0);
  await page
    .getByRole('link')
    .filter({ has: page.getByRole('heading', { name: 'Groceries', exact: true }) })
    .click();
  await page.getByRole('button', { name: 'Unarchive', exact: true }).click();
  await page.getByRole('link', { name: 'Your bags', exact: true }).click();
  await expect(headings).toHaveText(['Eating out', 'Groceries', 'Car']);
  await page.getByRole('button', { name: 'Manage pins', exact: true }).click();
  await page.getByRole('button', { name: 'Unpin Groceries', exact: true }).click();
  await expect(headings).toHaveText(['Eating out', 'Car', 'Groceries']);
  await page.getByRole('button', { name: 'Unpin Eating out', exact: true }).click();
  await expect(headings).toHaveText(['Car', 'Eating out', 'Groceries']);
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(
    page.getByText('Use Manage pins to keep your everyday bags at the top.')
  ).toBeVisible();
  await page.setViewportSize({ width: 320, height: 740 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

for (const archived of [false, true]) {
  test(`delete ${archived ? 'archived' : 'active'} bag with confirmation and return home`, async ({
    page
  }) => {
    await register(page);
    await createBag(page, 'Keep this bag', '25');
    await createBag(page, 'Delete this bag', '100');
    await page.getByRole('button', { name: 'Manage pins', exact: true }).click();
    await page.getByRole('button', { name: 'Pin Delete this bag', exact: true }).click();
    await expect(page.locator('.pin-caption')).toHaveText('Pinned');
    await page
      .getByRole('link')
      .filter({ has: page.getByRole('heading', { name: 'Delete this bag', exact: true }) })
      .click();
    await entry(page, '10', 'Remove this receipt', 'expense', [
      { name: 'receipt.txt', mimeType: 'text/plain', buffer: Buffer.from('receipt') }
    ]);
    if (archived) {
      await page.getByRole('button', { name: 'Archive', exact: true }).click();
      await expect(page.getByRole('button', { name: 'Unarchive', exact: true })).toBeVisible();
    }
    await page.getByRole('button', { name: 'Delete bag', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('Delete Delete this bag?');
    await expect(dialog).toContainText('for everyone in your family');
    await dialog.getByRole('button', { name: 'Keep bag', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator('.hero-balance')).toHaveText('$90.00');
    await page.getByRole('button', { name: 'Delete bag', exact: true }).click();
    await dialog.getByRole('button', { name: 'Delete bag', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Your bags', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Delete this bag', exact: true })).toHaveCount(
      0
    );
    await expect(page.getByRole('heading', { name: 'Keep this bag', exact: true })).toBeVisible();
    await expect(page.locator('.home-bag-amount')).toHaveText('$25.00');
    await page.reload();
    await expect(page.locator('.home-bag')).toHaveCount(1);
    await expect(page.getByRole('button', { name: /Archived bags/ })).toHaveCount(0);
    await createBag(page, 'Delete this bag');
    await page
      .getByRole('link')
      .filter({ has: page.getByRole('heading', { name: 'Delete this bag', exact: true }) })
      .click();
    await page.getByRole('button', { name: 'Delete bag', exact: true }).click();
    await dialog.getByRole('button', { name: 'Delete bag', exact: true }).click();
    await expect(page.locator('.home-bag')).toHaveCount(1);
  });
}
