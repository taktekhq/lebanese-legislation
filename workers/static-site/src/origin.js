import { Container, getContainer } from "@cloudflare/containers";
import { SECURITY_HEADERS } from "./headers.js";

export class Origin extends Container {
  defaultPort = 8080;
  // The image's own start script waits 60 s for Postgres; a cold start on
  // Cloudflare needs longer (the container exited after about 63 s each time).
  // Same script, with a 10 minute wait and progress lines.
  entrypoint = ["/bin/sh", "-c", "exec > /tmp/start.log 2>&1\nset -u\nreport() {\n  echo \"start: $(date -u +%T) $1\"; ls -la \"$PGDATA\" | head -5; df -h / /tmp; free -m; id\n  exec perl -MIO::Socket::INET -e '$s=IO::Socket::INET->new(LocalPort=>8080,Listen=>5,ReuseAddr=>1) or die $!; while($c=$s->accept){local $/; open F,\"/tmp/start.log\"; $b=<F>; close F; print $c \"HTTP/1.0 503 Service Unavailable\\r\\nContent-Type: text/plain\\r\\nContent-Length: \".length($b).\"\\r\\n\\r\\n\".$b; close $c}'\n}\nexport LEBLEG_VECTOR_DSN=\"${LEBLEG_VECTOR_DSN:-host=127.0.0.1 user=lebleg_web dbname=lebleg_search}\"\necho \"start: $(date -u +%T) postgres starting\"\ninstall -d -o postgres -g postgres -m 2775 /var/run/postgresql\ngosu postgres postgres -D \"$PGDATA\" -c listen_addresses=127.0.0.1 -c shared_buffers=1GB -c max_connections=50 -c default_transaction_read_only=on &\nPG=$!\ni=0\nuntil pg_isready -h 127.0.0.1 -U lebleg_web -d lebleg -q; do\n  kill -0 \"$PG\" 2>/dev/null || report \"postgres exited\"\n  i=$((i+1)); [ \"$i\" -lt 600 ] || report \"postgres not ready after 300 s\"\n  [ $((i % 15)) -eq 0 ] && echo \"start: waiting for postgres, $i s\"\n  sleep 1\ndone\necho \"start: $(date -u +%T) postgres ready after $i s\"\ninstall -d -o 65532 -g 65532 /tmp/lebleg-events\ngosu 65532:65532 /usr/local/bin/lebleg-web -addr 0.0.0.0:8080 -events /tmp/lebleg-events -scans /scans -db \"host=127.0.0.1 user=lebleg_web dbname=lebleg\" &\nwait $!\nreport \"web server exited with $?\""];
  sleepAfter = "72h";

  constructor(ctx, env) {
    super(ctx, env);
    this.envVars = Object.fromEntries([
      "GEMINI_API_KEY", "POSTHOG_KEY", "LEBLEG_SEARCH_PROVIDER",
      "LEBLEG_VECTORIZE_INDEX", "CLOUDFLARE_ACCOUNT_ID", "LEBLEG_VECTORIZE_TOKEN",
      "LEBLEG_SEARCH_MODEL", "LEBLEG_SEARCH_SELECT_MODEL",
      "LEBLEG_SEARCH_DAILY_LIMIT", "LEBLEG_SEARCH_SHARES_DATA",
    ].filter((key) => env[key] !== undefined).map((key) => [key, env[key]]));
  }

  // A cold start opens a 10 GB database and warms the server's caches before it
  // listens, which takes longer than the library's default 20 seconds.
  async fetch(request) {
    // Also when the container is already running: it may still be opening
    // Postgres, and the library's own wait gives up after about 20 to 48 s.
    await this.startAndWaitForPorts({
      ports: [this.defaultPort],
      cancellationOptions: { instanceGetTimeoutMS: 120_000, portReadyTimeoutMS: 660_000 },
    });
    return super.fetch(request);
  }
}

export async function originFetch(request, env) {
  const url = new URL(request.url);
  if (url.pathname.startsWith("/ph/")) {
    const host = url.pathname.startsWith("/ph/static/")
      ? "https://eu-assets.i.posthog.com" : "https://eu.i.posthog.com";
    return fetch(new Request(host + url.pathname.slice(3) + url.search, request));
  }

  const ruling = /^\/ruling-files\/([1-9][0-9]{0,8})\.pdf$/.exec(url.pathname);
  if (ruling) {
    const id = ruling[1];
    const shard = String(Math.floor(Number(id) / 1000)).padStart(4, "0");
    const obj = await env.SITE.get(`ruling-files/${shard}/${id}.pdf`);
    if (obj) {
      return new Response(request.method === "HEAD" ? null : obj.body, {
        status: 200,
        headers: {
          ...SECURITY_HEADERS,
          "Content-Type": "application/pdf",
          "Cache-Control": "public, max-age=86400",
          "X-Robots-Tag": "noindex",
        },
      });
    }
  }

  // A request with the secret test header reaches the container whatever the mode,
  // so the container can be checked before traffic moves to it.
  const testing = env.ORIGIN_TEST_KEY && request.headers.get("x-origin-test") === env.ORIGIN_TEST_KEY;
  if (testing && new URL(request.url).pathname === "/_origin/restart") {
    // Stops the container so the next request starts it with the current settings.
    await getContainer(env.ORIGIN, "main").destroy();
    return new Response("container stopped\n", { headers: SECURITY_HEADERS });
  }
  if (env.ORIGIN_MODE === "hetzner" && !testing) return fetch(request);
  try {
    const res = await getContainer(env.ORIGIN, "main").fetch(request);
    const headers = new Headers(res.headers);
    for (const [key, value] of Object.entries(SECURITY_HEADERS)) {
      if (!headers.has(key)) headers.set(key, value);
    }
    return new Response(res.body, { status: res.status, statusText: res.statusText, headers });
  } catch (err) {
    console.error("container origin:", err && err.message);
    return new Response(testing ? "container error: " + (err && err.stack || err) : "Service unavailable", {
      status: 503,
      headers: { ...SECURITY_HEADERS, "Retry-After": "30" },
    });
  }
}
