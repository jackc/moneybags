import { test, expect } from '@playwright/test';

test('OAuth consent ignores extension parameters during preparation and approval', async ({
  page,
  baseURL
}) => {
  const signup = await page.request.post('/api/actions/register', {
    headers: { Origin: baseURL },
    data: {
      username: `oauth_${crypto.randomUUID().slice(0, 12)}`,
      password: 'A reliable test password 123!',
      family_name: 'OAuth household',
      time_zone: 'America/Chicago'
    }
  });
  expect(signup.ok()).toBe(true);

  const params = {
    response_type: 'code',
    client_id: 'https://chatgpt.com/oauth/test-callback/client.json',
    redirect_uri: 'https://chatgpt.com/connector/oauth/test-callback',
    scope: 'bags:read bags:write family:write',
    state: 'test-state-12345678',
    code_challenge: 'a'.repeat(43),
    code_challenge_method: 'S256',
    resource: `${baseURL}/mcp`
  };
  const requests = [];
  await page.route('**/api/actions/*oauth*', async (route) => {
    const body = route.request().postDataJSON();
    requests.push(body);
    if (Object.keys(body).some((key) => !(key in params))) {
      await route.fulfill({
        status: 400,
        json: {
          error: { code: 'validation_error', message: 'Invalid request fields or types' }
        }
      });
      return;
    }
    await route.fulfill({
      json: route.request().url().endsWith('/prepare_oauth_authorization')
        ? { client: { client_name: 'ChatGPT' }, scope: params.scope, audience: params.resource }
        : { code: 'test-code', redirect_uri: params.redirect_uri, state: params.state }
    });
  });
  await page.route(`${params.redirect_uri}*`, (route) =>
    route.fulfill({ contentType: 'text/html', body: 'Connected' })
  );

  const query = new URLSearchParams({
    ...params,
    ui_locales: 'en-US'
  });
  await page.goto(`/oauth/authorize?${query}`);
  await expect(page.getByText('ChatGPT', { exact: true })).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(0);
  await page.getByRole('button', { name: 'Allow connection' }).click();
  await expect(page).toHaveURL(`${params.redirect_uri}?code=test-code&state=${params.state}`);
  expect(requests).toEqual([params, params]);
});
