// The Hugging Face dataset's public counters (downloads over the last 30
// days, all time, likes), read once a day by the Worker's cron and sent to
// PostHog as one hf_dataset_stats event. The dataset card itself can't run
// scripts; its views are counted by the badges (src/badge.ts).

import { PHEvent } from "./posthog";

export const HF_DATASET = "taktekhq/lebanese-legislation";
const HF_API = `https://huggingface.co/api/datasets/${HF_DATASET}?expand[]=downloads&expand[]=downloadsAllTime&expand[]=likes`;

interface HFCounts {
  downloads?: number;
  downloadsAllTime?: number;
  likes?: number;
}

export async function hfStatsEvents(): Promise<PHEvent[]> {
  const res = await fetch(HF_API, { headers: { "User-Agent": "lebaneselegislation-worker (+https://lebaneselegislation.com)" } });
  if (!res.ok) throw new Error(`hugging face api answered ${res.status}`);
  const c = (await res.json()) as HFCounts;
  return [{
    event: "hf_dataset_stats",
    distinctId: "lebaneselegislation-hf-stats",
    properties: { dataset: HF_DATASET, downloads_30d: c.downloads ?? 0, downloads_all_time: c.downloadsAllTime ?? 0, likes: c.likes ?? 0 },
  }];
}

// When the dataset last changed on Hugging Face (every deploy/hf.sh upload
// is one commit), for the "last updated" badge. Cached at the edge for an
// hour: uploads happen once a night at most.
const HF_UPDATED_API = `https://huggingface.co/api/datasets/${HF_DATASET}?expand[]=lastModified`;
const HF_UPDATED_CACHE_KEY = "https://lebaneselegislation.com/__hf_last_modified__/v1";
const HF_UPDATED_TTL_SECONDS = 60 * 60;

export async function hfLastModified(): Promise<string> {
  const cache = (caches as unknown as WorkersCacheStorage).default;
  const hit = await cache.match(HF_UPDATED_CACHE_KEY);
  if (hit) return hit.text();
  const res = await fetch(HF_UPDATED_API, { headers: { "User-Agent": "lebaneselegislation-worker (+https://lebaneselegislation.com)" } });
  if (!res.ok) throw new Error(`hugging face api answered ${res.status}`);
  const { lastModified } = (await res.json()) as { lastModified?: string };
  if (!lastModified || Number.isNaN(Date.parse(lastModified))) throw new Error("hugging face api gave no lastModified");
  await cache.put(HF_UPDATED_CACHE_KEY, new Response(lastModified, {
    headers: { "Content-Type": "text/plain", "Cache-Control": `public, max-age=${HF_UPDATED_TTL_SECONDS}` },
  }));
  return lastModified;
}
