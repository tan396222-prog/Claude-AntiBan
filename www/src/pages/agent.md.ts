import guide from '../../../docs/guide/agent.md?raw';

export function GET() {
  const text = guide.replace(/^---\n[\s\S]*?\n---\n/, '').replace(/\]\(([a-z-]+)\.md(#[^)]*)?\)/g, (_, slug: string, hash = '') => `](https://lee-xeo.github.io/Claude-Cage/docs/${slug}/${hash})`);
  return new Response(text, { headers: { 'Content-Type': 'text/markdown; charset=utf-8' } });
}
