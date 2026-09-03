import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

/**
 * The bundle is built into the Go package that embeds it, so `go build` picks up
 * whatever `npm run build` last produced. One artefact goes onto a server.
 *
 * Two settings are load-bearing rather than defaults:
 *
 *  - `base: '/'`. The router is a BrowserRouter and routes go two levels deep, so a
 *    relative base would resolve './assets/index.js' against /sites/ on a refresh,
 *    request /sites/assets/index.js, and get index.html back from the SPA fallback —
 *    HTML where the browser expected JavaScript, for a blank page and a console error
 *    that does not say why.
 *
 *  - `cssCodeSplit: false` and no inline anything. The panel serves a strict
 *    Content-Security-Policy with no 'unsafe-inline' for scripts or styles, because a
 *    root-equivalent interface is the wrong place to leave that door open. The bundle
 *    is built to comply rather than the policy relaxed to fit the bundle.
 */
export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  server: {
    port: Number(process.env.PORT) || 5174,
    // In development the API is the real panel, running on the loopback. The proxy
    // means the browser sees one origin, so cookies and the Origin check behave
    // exactly as they will in production.
    proxy: {
      '/api': {
        target: process.env.PANEL_ORIGIN || 'http://127.0.0.1:8420',
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: '../../internal/panel/web/dist',
    // Not emptied by vite, which would take the committed .gitkeep with it.
    //
    // That file is what makes `//go:embed all:dist` compile in a clone that has never
    // run this build — without it `go build ./...` fails with "pattern all:dist: no
    // matching files found", so a Go-only checkout could not build the repository at
    // all. The prebuild step removes dist/assets instead, which is the same cleaning
    // (index.html is overwritten) without deleting the marker.
    emptyOutDir: false,
    target: 'es2022',
    cssCodeSplit: false,
    // Nothing inlined: an inline <style> or a data: script would need
    // 'unsafe-inline' in the policy above, which is the whole thing being avoided.
    assetsInlineLimit: 0,
  },
});
