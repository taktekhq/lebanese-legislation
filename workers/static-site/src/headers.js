// Security headers shared by static pages and container responses.

export const CSP = "default-src 'self'; worker-src 'self' blob:; script-src 'self' 'sha256-WZRJfWvsnNCPcxzZwvyhovnZGqhZaC+8gPGPRbx6wTk=' https://www.googletagmanager.com https://static.cloudflareinsights.com; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: https://*.google-analytics.com https://*.googletagmanager.com https://analytics.google.com https://*.analytics.google.com https://*.g.doubleclick.net https://www.google.com; connect-src 'self' https://*.google-analytics.com https://analytics.google.com https://*.analytics.google.com https://*.g.doubleclick.net https://www.google.com https://*.googletagmanager.com https://cloudflareinsights.com; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'";

export const SECURITY_HEADERS = {
  "Strict-Transport-Security": "max-age=31536000; includeSubDomains",
  "X-Content-Type-Options": "nosniff",
  "X-Frame-Options": "DENY",
  "Referrer-Policy": "strict-origin-when-cross-origin",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()",
  "Cross-Origin-Opener-Policy": "same-origin",
  "Cross-Origin-Resource-Policy": "same-site",
  "Content-Security-Policy": CSP,
};

// Files other sites show. The MCP Registry entry (server.json) points at these
// as the server's icons, and directory sites (mcptry, Glama) and MCP clients
// load them from their own pages. same-site would leave a blank square there.
export const EMBEDDABLE = new Set(["/assets/mark.png", "/assets/favicon.svg"]);
