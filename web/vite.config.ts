import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { createHash } from "node:crypto";
import { defineConfig, type Plugin } from "vite";

const SHELL = [
  "/",
  "/index.html",
  "/manifest.webmanifest",
  "/icons/icon-192.png",
  "/icons/icon-512.png",
];

const NETWORK_ONLY = ["/api", "/media", "/mcp", "/healthz", "/readyz"];

function pwaShell(): Plugin {
  return {
    name: "gritual-pwa-shell",
    apply: "build",
    generateBundle(_opts, bundle) {
      const urls = new Set(SHELL);
      for (const name of Object.keys(bundle)) {
        if (name.startsWith("assets/")) {
          urls.add(`/${name}`);
        }
      }
      const precache = [...urls].sort();
      this.emitFile({
        type: "asset",
        fileName: "sw.js",
        source: renderServiceWorker(precache),
      });
    },
  };
}

function renderServiceWorker(precache: string[]): string {
  const cache = "gritual-shell-" + createHash("sha256").update(precache.join("\0")).digest("hex").slice(0, 12);
  return `/* precache shell + hashed /assets/* only; never cache ${NETWORK_ONLY.join(", ")} */
const CACHE = ${JSON.stringify(cache)};
const PRECACHE = ${JSON.stringify(precache)};

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) => cache.addAll(PRECACHE)).then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((k) => k.startsWith("gritual-shell-") && k !== CACHE)
          .map((k) => caches.delete(k)),
      ),
    ).then(() => self.clients.claim()),
  );
});

function networkOnly(path) {
  return (
    path === "/api" ||
    path.startsWith("/api/") ||
    path === "/media" ||
    path.startsWith("/media/") ||
    path === "/mcp" ||
    path.startsWith("/mcp/") ||
    path === "/healthz" ||
    path.startsWith("/healthz/") ||
    path === "/readyz" ||
    path.startsWith("/readyz/")
  );
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") {
    return;
  }
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) {
    return;
  }
  if (networkOnly(url.pathname)) {
    return;
  }
  event.respondWith(
    (async () => {
      if (req.mode === "navigate") {
        try {
          return await fetch(req);
        } catch {
          const shell = (await caches.match("/index.html")) || (await caches.match("/"));
          if (shell) {
            return shell;
          }
          return new Response("You’re offline", {
            status: 503,
            headers: { "Content-Type": "text/plain; charset=utf-8" },
          });
        }
      }
      const cached = await caches.match(req);
      if (cached) {
        return cached;
      }
      return fetch(req);
    })(),
  );
});
`;
}

export default defineConfig({
  base: "/",
  plugins: [react(), tailwindcss(), pwaShell()],
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8080", changeOrigin: false },
      "/media": { target: "http://127.0.0.1:8080", changeOrigin: false },
      "/healthz": { target: "http://127.0.0.1:8080", changeOrigin: false },
      "/readyz": { target: "http://127.0.0.1:8080", changeOrigin: false },
      "/mcp": { target: "http://127.0.0.1:8080", changeOrigin: false },
      // do not proxy /metrics — METRICS_ADDR is 127.0.0.1:9090 on the Go process
    },
  },
});
