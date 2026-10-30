export function allowedPath(path) {
  return path === '/mcp' || path === '/mcp/' || path === '/healthz';
}

export async function ready(container, env, wait) {
  if (!env.DATABASE_URL) throw new Error('DATABASE_URL secret is required');
  if (!container.running) {
    container.start({
      image: container.images.base,
      instance: 'lite',
      enableInternet: true,
      env: {
        DATABASE_URL: env.DATABASE_URL,
        PUBLIC_URL: env.PUBLIC_URL || '',
        LISTEN_ADDR: '0.0.0.0:8080',
        APP_MODE: 'mcp',
      },
    });
  }
  await container.setInactivityTimeout(60_000);
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      const response = await container.getTcpPort(8080).fetch('http://container/healthz', {
        signal: AbortSignal.timeout(1000),
      });
      await response.body?.cancel();
      if (response.ok) return;
    } catch { /* Retry while the process connects to its database. */ }
    await wait(200);
  }
  throw new Error('Container did not become ready');
}
