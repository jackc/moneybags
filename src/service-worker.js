/// <reference types="@sveltejs/kit" />
/// <reference no-default-lib="true" />
/// <reference lib="esnext" />
/// <reference lib="webworker" />

import { build, files, version } from '$service-worker';

const CACHE = `moneybags-${version}`;
const ASSETS = new Set([...build, ...files]);

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll([...ASSETS])));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((key) => key.startsWith('moneybags-') && key !== CACHE)
            .map((key) => caches.delete(key))
        )
      )
  );
});

self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin) return;

  // Cache only public build assets. Account data, attachments, OAuth and MCP
  // always use the network, including when opened directly as a navigation.
  if (/^\/(api|oauth|mcp|\.well-known)(\/|$)/.test(url.pathname)) return;

  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request).catch(async () => {
        const cache = await caches.open(CACHE);
        return (await cache.match('/offline.html')) || Response.error();
      })
    );
  } else if (ASSETS.has(url.pathname)) {
    event.respondWith(
      caches.open(CACHE).then(async (cache) => (await cache.match(url.pathname)) || fetch(request))
    );
  }
});
