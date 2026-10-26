import { defineConfig } from 'vite';
export default defineConfig({ build:{outDir:'../server/internal/webui/dist', emptyOutDir:true}, server:{proxy:{'/api':'http://127.0.0.1:8787','/health':'http://127.0.0.1:8787'}} });
