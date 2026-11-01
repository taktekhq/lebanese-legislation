# MCP Worker analytics

The MCP server and the dataset badges send GA4 Measurement Protocol events
(`src/ga.ts`) and PostHog events (`src/posthog.ts`) after each response.

GA4: `client_id` is a hash of IP + User-Agent + the UTC day, so one client
counts as one user per day and can't be followed across days. No cookies and
no stored IP. Location comes from Cloudflare's `request.cf`. MCP and dataset
events go to their own GA4 property (see `wrangler.jsonc`), never to the
website's property, so machine traffic does not show up as site visitors.

PostHog: the person is a hash of IP + User-Agent (salted with the
`POSTHOG_ID_SALT` Worker secret when set), and events keep the client name,
tool arguments and responses in PostHog's `$mcp_*` shape.

Never give each request a fresh random ID: GA4 and PostHog then count every
MCP call as a new user (30 to 31 Oct 2026, MCP "users" went from about 200
to over 1,000 a day while calls stayed flat).

Run tests with Node 22 or newer:

```sh
npm ci
npm test
```

Missing analytics keys disable sending. Test suites stub outbound requests.
Configure the GA stream IDs/secrets and PostHog project token through the
Worker environment. Never commit server credentials.

Production deploys follow `deploy/deploy-runner.sh worker <commit>` from the
repository root, using a commit on `origin/main`, the provenance check, the
pipeline lock and the active D1 binding. Do not deploy from a dirty checkout.

## Calculated current monetary amounts

`get_article`, `get_law_texts` and article hits from `search_legislation`
include `calculated_current` when the canonical snapshot exports an eligible
Civil Procedure article (law 244565). Use its `text_ar` for current monetary
answers and cite `authority.url`. Describe it as calculated under decree
13909/2024, effective 2024-09-19. It is not a verbatim quotation.

The object carries `kind: statutory_monetary_adjustment`, `text_ar`, exact
`source_text_ar`, `source_sha256`, `is_published_wording: false`, `multiplier: 50`
and `authority` (law 330819, article 1, canonical URL and effective date).
Published `text_ar`, original/centre wording, versions and source URLs keep
their existing meaning. Search snippets remain published wording; snapshots
that index calculated text also allow lookup by the updated amounts.

The Worker validates the exported JSON, authority, law scope, eligibility,
exact current source and SHA-256. It suppresses stale, malformed, repealed,
annulled, pending or future records. Older snapshots without the optional
column still work and return no calculated claim. Arithmetic and reviewed
Arabic spellings live in the shared Go exporter, never in Worker code.
