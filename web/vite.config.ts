import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the Go server (make run) answers /api; Vite serves the app with hot reload.
// The demo (--mode demo) is served from /demo/ on photostead.com and answers /api in the browser;
// its sample library comes from the site (site/public/demo, made by `npm run library` there).
export default defineConfig(({ mode, command }) => {
  if (mode === 'demo') {
    return {
      plugins: [react()],
      base: '/demo/',
      publicDir: command === 'serve' ? '../site/public/demo' : false,
      server: { port: 5174 },
      build: { target: 'es2022', outDir: 'dist-demo', sourcemap: false },
    };
  }
  return {
    plugins: [react()],
    server: { port: 5173, proxy: { '/api': 'http://127.0.0.1:4800' } },
    build: { target: 'es2022', sourcemap: true },
  };
});
