import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import https from 'node:https';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const api = env.VITE_API_PROXY || 'http://127.0.0.1:8080';
  const supabase = env.VITE_SUPABASE_PROXY || 'https://deepseek-alpha.com';
  const ip = env.VITE_SUPABASE_DIAL_IP || '';

  // Development escape hatch for a machine whose DNS cannot resolve the
  // deployment: dial the SG IP, keep the deployment's Host and TLS SNI.
  const supabaseProxy = ip
    ? {
        target: supabase,
        changeOrigin: false,
        secure: false,
        headers: { Host: 'deepseek-alpha.com' },
        agent: new https.Agent({ keepAlive: true }),
      }
    : { target: supabase, changeOrigin: true, secure: false };

  return {
    plugins: [react()],
    server: {
      host: '127.0.0.1',
      port: 5175,
      strictPort: true,
      proxy: {
        '/v1': { target: api, changeOrigin: true },
        '/sb': supabaseProxy,
      },
    },
  };
});
