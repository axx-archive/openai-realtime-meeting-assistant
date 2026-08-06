export type ScoutInline = {
  kind: 'text' | 'strong' | 'emphasis' | 'code' | 'link' | 'mention';
  text: string;
  url?: string;
  scout?: boolean;
};

export type ScoutBlock = {
  kind: 'paragraph' | 'heading' | 'bullet' | 'number' | 'quote' | 'code' | 'rule';
  inlines: ScoutInline[];
  marker?: string;
  level?: number;
  depth?: number;
  checked?: boolean;
};

const inlineToken = /(\[[^\]]+\]\(https?:\/\/[^\s)]+\)|\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|(?<!\w)\*[^*\n]+\*|(?<!\w)_[^_\n]+_|https?:\/\/[^\s<>"']+|@[\p{L}\p{N}](?:[\p{L}\p{N}._-]*[\p{L}\p{N}_-])?)/giu;

function cleanLiteral(value: string): string {
  return value
    .replace(/\\([\\`*_{}\[\]()#+.!-])/g, '$1')
    .replace(/\*\*|__|~~|`/g, '')
    .replace(/(^|\s)#{1,6}(?=\s)/g, '$1')
    .replace(/[ \t]+/g, ' ');
}

export function parseScoutInline(value: string): ScoutInline[] {
  const result: ScoutInline[] = [];
  let cursor = 0;
  for (const match of value.matchAll(inlineToken)) {
    const start = match.index ?? 0;
    if (start > cursor) {
      const text = cleanLiteral(value.slice(cursor, start));
      if (text) result.push({ kind: 'text', text });
    }

    const token = match[0];
    const markdownLink = /^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/iu.exec(token);
    if (markdownLink) {
      result.push({ kind: 'link', text: cleanLiteral(markdownLink[1]), url: markdownLink[2] });
    } else if ((token.startsWith('**') && token.endsWith('**')) || (token.startsWith('__') && token.endsWith('__'))) {
      result.push({ kind: 'strong', text: cleanLiteral(token.slice(2, -2)) });
    } else if (token.startsWith('`') && token.endsWith('`')) {
      result.push({ kind: 'code', text: token.slice(1, -1) });
    } else if ((token.startsWith('*') && token.endsWith('*')) || (token.startsWith('_') && token.endsWith('_'))) {
      result.push({ kind: 'emphasis', text: cleanLiteral(token.slice(1, -1)) });
    } else if (/^https?:\/\//iu.test(token)) {
      const trailing = /[.,;:!?]+$/u.exec(token)?.[0] ?? '';
      const url = trailing ? token.slice(0, -trailing.length) : token;
      result.push({ kind: 'link', text: url, url });
      if (trailing) result.push({ kind: 'text', text: trailing });
    } else if (token.startsWith('@')) {
      const name = token.slice(1);
      result.push({ kind: 'mention', text: name, scout: name.toLowerCase() === 'scout' });
    }
    cursor = start + token.length;
  }

  if (cursor < value.length) {
    const text = cleanLiteral(value.slice(cursor));
    if (text) result.push({ kind: 'text', text });
  }
  return result;
}

function normalizeMarkdown(value: string): string {
  return value
    .replace(/\r\n?/g, '\n')
    // Memory excerpts sometimes place headings after an em dash on one line.
    // Promote those tokens to real blocks so raw hashes never reach the UI.
    .replace(/[ \t]+(#{1,6})[ \t]+/g, '\n$1 ')
    .trim();
}

export function parseScoutMarkdown(value: string): ScoutBlock[] {
  const lines = normalizeMarkdown(value).split('\n');
  const blocks: ScoutBlock[] = [];
  let inFence = false;
  let code: string[] = [];

  const push = (kind: ScoutBlock['kind'], text: string, extra: Partial<ScoutBlock> = {}) => {
    const cleaned = text.trim();
    if (!cleaned) return;
    blocks.push({ kind, inlines: parseScoutInline(cleaned), ...extra });
  };

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (/^```/u.test(line)) {
      if (inFence) {
        push('code', code.join('\n'));
        code = [];
      }
      inFence = !inFence;
      continue;
    }
    if (inFence) {
      code.push(rawLine);
      continue;
    }
    if (!line) continue;

    if (/^(?:-{3,}|\*{3,}|_{3,})$/u.test(line)) {
      blocks.push({ kind: 'rule', inlines: [] });
      continue;
    }

    const heading = /^(#{1,6})\s+(.+)$/u.exec(line);
    if (heading) {
      push('heading', heading[2], { level: heading[1].length });
      continue;
    }
    const bullet = /^(\s*)[-*+]\s+(?:\[([ xX])\]\s+)?(.+)$/u.exec(rawLine);
    if (bullet) {
      const checked = bullet[2] ? bullet[2].toLowerCase() === 'x' : undefined;
      push('bullet', bullet[3], {
        marker: checked === undefined ? '•' : checked ? '✓' : '○',
        depth: Math.min(3, Math.floor(bullet[1].replace(/\t/gu, '  ').length / 2)),
        checked,
      });
      continue;
    }
    const numbered = /^(\s*)(\d+)[.)]\s+(.+)$/u.exec(rawLine);
    if (numbered) {
      push('number', numbered[3], {
        marker: `${numbered[2]}.`,
        depth: Math.min(3, Math.floor(numbered[1].replace(/\t/gu, '  ').length / 2)),
      });
      continue;
    }
    const quote = /^>\s?(.+)$/u.exec(line);
    if (quote) {
      push('quote', quote[1]);
      continue;
    }
    push('paragraph', line);
  }

  if (code.length > 0) push('code', code.join('\n'));
  return blocks;
}

export function truncateScoutBlocks(blocks: ScoutBlock[], maxCharacters: number): { blocks: ScoutBlock[]; truncated: boolean } {
  const total = blocks.reduce((sum, block) => sum + block.inlines.reduce((inlineSum, inline) => inlineSum + inline.text.length, 0), 0);
  if (total <= maxCharacters) return { blocks, truncated: false };

  let remaining = Math.max(1, maxCharacters);
  const result: ScoutBlock[] = [];
  for (const block of blocks) {
    const inlines: ScoutInline[] = [];
    for (const inline of block.inlines) {
      if (remaining <= 0) break;
      if (inline.text.length <= remaining) {
        inlines.push(inline);
        remaining -= inline.text.length;
        continue;
      }
      const slice = inline.text.slice(0, remaining).replace(/\s+\S*$/u, '').trimEnd();
      inlines.push({ ...inline, text: `${slice || inline.text.slice(0, remaining).trimEnd()}…` });
      remaining = 0;
    }
    if (inlines.length > 0) result.push({ ...block, inlines });
    if (remaining <= 0) break;
  }

  const lastBlock = result[result.length - 1];
  const lastInline = lastBlock?.inlines[lastBlock.inlines.length - 1];
  if (lastInline && !lastInline.text.endsWith('…')) lastInline.text = `${lastInline.text.trimEnd()}…`;
  return { blocks: result, truncated: true };
}
