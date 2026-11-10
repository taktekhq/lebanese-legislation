import test from 'node:test';
import assert from 'node:assert/strict';
import { register } from 'node:module';
import { agentFamily, agentInfo, asksMarkdown, markdownFirst, markdownAsk } from '../src/markdownask.js';
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

test('who asked: name, version, operator, purpose and client', () => {
  const pick = (ua, keys) => Object.fromEntries(keys.map(k => [k, agentInfo(ua)[k]]));
  const all = ['family', 'name', 'version', 'operator', 'purpose', 'client', 'client_version'];
  // The User-Agents below are the ones seen on the live site on 10 Nov 2026.
  assert.deepEqual(pick('Mozilla/5.0 (compatible; ExaSearchBot/1.0; +https://crawler.exa.ai/)', all),
    { family: 'exa', name: 'ExaSearchBot', version: '1.0', operator: 'exa', purpose: 'search-index', client: '', client_version: '' });
  assert.deepEqual(pick('Mozilla/5.0 (compatible; ExaUser/1.0; +https://exauser.crawler.exa.ai)', all),
    { family: 'exa', name: 'ExaUser', version: '1.0', operator: 'exa', purpose: 'user-fetch', client: '', client_version: '' });
  assert.deepEqual(pick('Claude-User (claude-code/2.1.321; +https://support.anthropic.com/)', all),
    { family: 'claude-user', name: 'Claude-User', version: '', operator: 'anthropic', purpose: 'user-fetch', client: 'claude-code', client_version: '2.1.321' });
  assert.deepEqual(pick('Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot', all),
    { family: 'chatgpt-user', name: 'ChatGPT-User', version: '1.0', operator: 'openai', purpose: 'user-fetch', client: '', client_version: '' });
  const three = ['family', 'name', 'purpose'];
  assert.deepEqual(pick('Mozilla/5.0 (compatible; GPTBot/1.2; +https://openai.com/gptbot)', three), { family: 'openai-crawler', name: 'GPTBot', purpose: 'training' });
  assert.deepEqual(pick('Mozilla/5.0 (compatible; OAI-SearchBot/1.3; +https://openai.com/searchbot)', three), { family: 'openai-crawler', name: 'OAI-SearchBot', purpose: 'search-index' });
  assert.deepEqual(pick('Mozilla/5.0 (compatible; Claude-SearchBot/1.0; +https://www.anthropic.com)', three), { family: 'anthropic-crawler', name: 'Claude-SearchBot', purpose: 'search-index' });
  assert.deepEqual(pick('Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)', three), { family: 'anthropic-crawler', name: 'ClaudeBot', purpose: 'training' });
  assert.deepEqual(pick('claude-code/2.1.319', all),
    { family: 'claude-code', name: 'claude-code', version: '2.1.319', operator: 'anthropic', purpose: 'user-fetch', client: '', client_version: '' });
  assert.deepEqual(pick('Mozilla/5.0 (compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)', three), { family: 'perplexity', name: 'PerplexityBot', purpose: 'search-index' });
  assert.deepEqual(pick('Mozilla/5.0 (compatible; Perplexity-User/1.0)', three), { family: 'perplexity', name: 'Perplexity-User', purpose: 'user-fetch' });
  // Agents not in the list keep the name they give themselves.
  const two = ['family', 'name', 'version'];
  assert.deepEqual(pick('Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ShapBot/0.1.0', two), { family: 'other', name: 'ShapBot', version: '0.1.0' });
  assert.deepEqual(pick('Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; KeenableBot/1.0; +https://keenable.ai/bot)', two), { family: 'other', name: 'KeenableBot', version: '1.0' });
  assert.deepEqual(pick('RebelativeIntakeBot/1.0 (+https://rebelative.com; reads MCP documentation to see who holds the key)', two), { family: 'other', name: 'RebelativeIntakeBot', version: '1.0' });
  assert.deepEqual(pick('curl/8.7.1', two), { family: 'library', name: 'curl', version: '8.7.1' });
  assert.deepEqual(pick('Mozilla/5.0 (Macintosh) Safari/605', two), { family: 'other', name: '', version: '' });
  assert.deepEqual(pick('', two), { family: 'other', name: '', version: '' });
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
  Object.defineProperty(request, 'cf', { value: { colo: 'IAD', asn: 396982, asOrganization: 'Google LLC', country: 'US', verifiedBotCategory: 'AI Assistant' } });
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
  assert.equal(e.properties.agent_name, 'Claude-User');
  assert.equal(e.properties.operator, 'anthropic');
  assert.equal(e.properties.purpose, 'user-fetch');
  assert.equal(e.properties.asn, 396982);
  assert.equal(e.properties.as_org, 'Google LLC');
  assert.equal(e.properties.verified_bot_category, 'AI Assistant');
  assert.equal(e.properties.country, 'US');
  assert.equal(e.properties.markdown_first, true);
  assert.equal(e.properties.route, '/law/');
  assert.equal(e.properties.$process_person_profile, false);
  assert.equal(e.distinct_id, 'lebleg-markdown-ask');
  assert.ok(!JSON.stringify(e).includes('244565'), 'no ids in the event');
});
