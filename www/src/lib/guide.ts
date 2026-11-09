import type { MarkdownInstance } from 'astro';

type GuideFrontmatter = { title: string; description: string; group: string; order: number; keywords?: string };
const modules = import.meta.glob<MarkdownInstance<GuideFrontmatter>>('../../../docs/guide/*.md', { eager: true });
export const guidePages = Object.entries(modules).map(([path, content]) => ({
  slug: path.split('/').at(-1)!.replace(/\.md$/, ''),
  ...content.frontmatter,
  content,
})).sort((a, b) => a.order - b.order);
export const guideGroups = [...new Set(guidePages.map(page => page.group))];
export const guideURL = (slug: string) => `${import.meta.env.BASE_URL}docs/${slug === 'index' ? '' : `${slug}/`}`;
export function guideHTML(html: string) {
  // The same Markdown links work in the repository and under a custom website base.
  return html.replace(/(src|href)="\.\.\/\.\.\/www\/public\/([^\"]+)"/g, (_, attribute: string, asset: string) => `${attribute}="${import.meta.env.BASE_URL}${asset}"`)
    .replaceAll('href="https://lee-xeo.github.io/Claude-Cage/', `href="${import.meta.env.BASE_URL}`).replace(/href="([^"#]+)\.md(#[^"]*)?"/g, (original, name: string, hash = '') => {
    const page = guidePages.find(page => page.slug === name.replace(/^\.\//, ''));
    return page ? `href="${guideURL(page.slug)}${hash}"` : original;
  });
}
