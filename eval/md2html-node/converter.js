/**
 * Markdown to HTML converter
 * Supports: headings, bold, italic, code blocks, unordered lists, paragraphs
 */

function convert(markdown) {
  if (!markdown) return '';

  const lines = markdown.split('\n');
  const html = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Handle code blocks (triple backticks)
    if (line.trim().startsWith('```')) {
      const codeLines = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('```')) {
        codeLines.push(escapeHtml(lines[i]));
        i++;
      }
      i++; // skip closing ```
      html.push(`<pre><code>${codeLines.join('\n')}</code></pre>`);
      continue;
    }

    // Handle headings
    const headingMatch = line.match(/^(#{1,3})\s+(.+)$/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      const content = parseInline(headingMatch[2].trim());
      html.push(`<h${level}>${content}</h${level}>`);
      i++;
      continue;
    }

    // Handle unordered lists
    const listMatch = line.match(/^[-*+]\s+(.+)$/);
    if (listMatch) {
      const listItems = [];
      while (i < lines.length && lines[i].match(/^[-*+]\s+(.+)$/)) {
        const itemMatch = lines[i].match(/^[-*+]\s+(.+)$/);
        listItems.push(`<li>${parseInline(itemMatch[1])}</li>`);
        i++;
      }
      html.push(`<ul>${listItems.join('')}</ul>`);
      continue;
    }

    // Handle empty lines (skip)
    if (line.trim() === '') {
      i++;
      continue;
    }

    // Handle paragraphs
    const content = parseInline(line);
    html.push(`<p>${content}</p>`);
    i++;
  }

  return html.join('\n');
}

/**
 * Parse inline markdown elements: bold, italic
 */
function parseInline(text) {
  // Escape HTML first
  text = escapeHtml(text);

  // Bold: **text** or __text__
  text = text.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  text = text.replace(/__(.+?)__/g, '<strong>$1</strong>');

  // Italic: *text* or _text_
  // Be careful not to match bold patterns
  text = text.replace(/(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)/g, '<em>$1</em>');
  text = text.replace(/(?<!_)_(?!_)(.+?)(?<!_)_(?!_)/g, '<em>$1</em>');

  return text;
}

/**
 * Escape HTML special characters
 */
function escapeHtml(text) {
  const map = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#039;'
  };
  return text.replace(/[&<>"']/g, (char) => map[char]);
}

module.exports = { convert };
