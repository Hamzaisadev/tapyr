import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The UI is served by tapyr run under /ui/. In development, `npm run dev`
// proxies the status API to a running tapyr run (TAPYR_URL, default
// http://127.0.0.1:9790).
export default defineConfig({
  base: "/ui/",
  plugins: [react()],
  build: {
    outDir: "dist/app",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    chunkSizeWarningLimit: 800,
  },
  server: {
    proxy: { "/api": process.env.TAPYR_URL ?? "http://127.0.0.1:9790" },
  },
  test: {
    environment: "jsdom",
  },
});
