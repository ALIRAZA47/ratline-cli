import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// Nothing here reaches the network at runtime.
//
// base defaults to '/' — a domain root — because that is what Vercel, Netlify and a
// plain nginx vhost serve, and because a relative base is quietly broken for this app.
// The router is a BrowserRouter and routes go two levels deep, so a refresh at
// /reference/exit-codes resolves './assets/index.js' against /reference/, requests
// /reference/assets/index.js, and the SPA fallback answers with index.html — handing the
// browser HTML where it expected JavaScript, for a blank page and a console error that
// does not say why.
//
// Set VITE_BASE to deploy under a subpath instead, e.g. a GitHub Pages project site:
//   VITE_BASE=/ratline-cli/ npm run build
export default defineConfig({
  base: process.env.VITE_BASE || '/',
  plugins: [react(), tailwindcss()],
  // Honour PORT so a supervisor that assigns one — a preview harness, a container
  // — gets a server on the port it is about to open. Vite otherwise takes 5173 and
  // silently walks upward when it is busy, which leaves the caller pointed at
  // nothing.
  server: { port: Number(process.env.PORT) || 5173 },
  build: {
    target: 'es2022',
    chunkSizeWarningLimit: 900,
  },
});
