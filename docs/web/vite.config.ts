import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// The site is served from a subpath on GitHub Pages style hosting as often as
// from a root, so base is relative. Nothing here reaches the network at runtime.
export default defineConfig({
  base: './',
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
