import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  base: "/",
  plugins: [react(), tailwindcss()],
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
