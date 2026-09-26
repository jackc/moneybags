import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

const backend = process.env.BACKEND_URL || `http://127.0.0.1:${process.env.BACKEND_PORT || 4000}`;
export default defineConfig({
  plugins: [sveltekit()],
  // Serve bundled fonts as same-origin files under the production CSP.
  build: { assetsInlineLimit: 0 },
  server: {
    host: '127.0.0.1',
    port: Number(process.env.VITE_PORT || 5173),
    strictPort: true,
    proxy: Object.fromEntries(
      ['/api', '/mcp', '/oauth', '/.well-known'].map((path) => [path, { target: backend }])
    )
  }
});
