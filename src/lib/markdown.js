import MarkdownIt from 'markdown-it';

// HTML is treated as text. markdown-it rejects javascript:, vbscript:, file:,
// and unsafe data links; no source text is inserted into HTML attributes here.
const renderer = new MarkdownIt({ html: false, linkify: false, breaks: true });
export function renderNotes(source = '') {
  return renderer.render(source);
}
export function noteSummary(source = '') {
  return (
    source
      .split('\n')
      .find((line) => line.trim())
      ?.replace(/^[#>*\s-]+/, '')
      .slice(0, 110) || ''
  );
}
