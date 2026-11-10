import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';
import { agentFamily, asksMarkdown, markdownFirst, markdownAsk } from '../src/markdownask.js';
register('./workers-loader.mjs', import.meta.url);

test('agent families', () => {
  assert.equal(agentFamily('Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot'), 'chatgpt-user');
  assert.equal(agentFamily('Mozilla/5.0 (compatible; GPTBot/1.2; +https://openai.com/gptbot)'), 'openai-crawler');
  assert.equal(agentFamily('Claude-User'), 'claude-user');
  assert.equal(agentFamily('Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)'), 'anthropic-crawler');
  assert.equal(agentFamily('claude-code/2.1.319'), 'claude-code');
  assert.equal(agentFamily('Mozilla/5.0 (compatible; Perplexity-User/1.0)'), 'perplexity');
  assert.equal(agentFamily('curl/8.7.1'), 'library');
  assert.equal(agentFamily('Mozilla/5.0 (Macintosh) Safari/605'), 'other');
});

test('asks for Markdown and whether it comes first', () => {
  assert.equal(asksMarkdown('text/html,application/xhtml+xml'), false);
  assert.equal(asksMarkdown('text/markdown, text/html;q=0.9'), true);
  assert.equal(markdownFirst('text/markdown, text/html;q=0.9'), true);
  assert.equal(markdownFirst('text/html, text/markdown;q=0.5'), false);
  assert.equal(markdownFirst('text/html, text/markdown'), false);
  assert.equal(markdownFirst('text/markdown'), true);
  assert.equal(markdownFirst('text/markdown;q=0, text/html'), false);
  assert.equal(markdownFirst('text/x-markdown, */*;q=0.1'), true);
});

async function ask(t, headers) {
  const events = [], pending = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => { events.push(JSON.parse(options.body)); return new Response('ok'); });
  const request = new Request('https://example.com/en/law/244565/', { headers });
  Object.defineProperty(request, 'cf', { value: { colo: 'IAD' } });
  markdownAsk(request, { POSTHOG_KEY: 'k', ORIGIN_TEST_KEY: 'probe' }, { waitUntil: p => pending.push(p) });
  await Promise.all(pending);
  return events;
}

test('one anonymous event per Markdown request, none otherwise', async t => {
  assert.deepEqual(await ask(t, { Accept: 'text/html', 'User-Agent': 'ChatGPT-User/1.0' }), []);
  assert.deepEqual(await ask(t, { Accept: 'text/markdown', 'x-origin-test': 'probe' }), []);
  const [e] = await ask(t, { Accept: 'text/markdown, text/html;q=0.9', 'User-Agent': 'Claude-User' });
  assert.equal(e.event, 'markdown_requested');
  assert.equal(e.properties.agent, 'claude-user');
  assert.equal(e.properties.markdown_first, true);
  assert.equal(e.properties.route, '/law/');
  assert.equal(e.properties.$process_person_profile, false);
  assert.equal(e.distinct_id, 'lebleg-markdown-ask');
  assert.ok(!JSON.stringify(e).includes('244565'), 'no ids in the event');
});
