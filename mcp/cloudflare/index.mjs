import { DurableObject } from 'cloudflare:workers';
import { allowedPath, ready } from './runtime.mjs';

export class MCPContainer extends DurableObject {
  starting;
  async fetch(request) {
    this.starting ??= ready(this.ctx.container, this.env, ms => scheduler.wait(ms))
      .finally(() => { this.starting = undefined; });
    try {
      await this.starting;
      const url = new URL(request.url);
      url.protocol = 'http:';
      url.host = 'container';
      const forwarded = new Request(url, request);
      forwarded.headers.delete('host');
      return await this.ctx.container.getTcpPort(8080).fetch(forwarded);
    } catch {
      return new Response('MCP service unavailable', { status: 503 });
    }
  }
}

export default {
  fetch(request, env) {
    if (!allowedPath(new URL(request.url).pathname)) {
      return new Response('Not found', { status: 404 });
    }
    return env.MCP_CONTAINER.getByName('mcp').fetch(request);
  },
};
