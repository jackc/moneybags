import { defineConfig, devices } from '@playwright/test';

const backendPort = process.env.TEST_BACKEND_PORT || '4001';
const frontendPort = process.env.TEST_VITE_PORT || '5174';
const origin = `http://localhost:${frontendPort}`;
export default defineConfig({
  testDir: './tests/browser',
  fullyParallel: false,
  workers: 1,
  timeout: 45000,
  expect: { timeout: 10000 },
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: origin,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE }
      : {}
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'npm run build && go run . server',
      url: `http://127.0.0.1:${backendPort}/health`,
      timeout: 120000,
      reuseExistingServer: false,
      env: {
        ...process.env,
        DATABASE_BACKEND: 'jed',
        JED_DATA_DIR: '.dev/browser-jed',
        ATTACHMENTS_DIR: '.dev/browser-attachments',
        PORT: backendPort,
        MCP_CANONICAL_URL: origin,
        WEBAUTHN_ORIGIN: origin,
        WEBAUTHN_RP_ID: 'localhost',
        ASSETS_DIR: 'build/assets',
        GOCACHE: process.env.GOCACHE || '/tmp/moneybags-go-cache'
      }
    },
    {
      command: 'npm run dev',
      url: origin,
      timeout: 60000,
      reuseExistingServer: false,
      env: {
        ...process.env,
        BACKEND_URL: `http://127.0.0.1:${backendPort}`,
        VITE_PORT: frontendPort
      }
    }
  ]
});
