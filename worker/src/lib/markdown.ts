import { marked } from 'marked';

marked.setOptions({ gfm: true, breaks: false });

export function markdownToHTML(src: string): string {
  return marked.parse(src, { async: false }) as string;
}

const SCRIPT_RE = /<script[\s\S]*?<\/script>/gi;
const STYLE_RE = /<style[\s\S]*?<\/style>/gi;
const BLOCK_TAG_RE = /<\/(p|div|h\d|li|tr|br|hr|blockquote)\s*>/gi;
const TAG_RE = /<[^>]+>/g;
const MANY_NEWLINES_RE = /\n{3,}/g;

function unescapeHTML(s: string): string {
  return s
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&#x27;/g, "'")
    .replace(/&nbsp;/g, ' ');
}

export function htmlToText(s: string): string {
  s = s.replace(SCRIPT_RE, '').replace(STYLE_RE, '');
  s = s.replace(BLOCK_TAG_RE, '\n');
  s = s.replace(TAG_RE, '');
  s = unescapeHTML(s);
  s = s.replace(MANY_NEWLINES_RE, '\n\n');
  return s.trim();
}

function escapeHTML(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

const VAR_RE = /\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:\|\s*"((?:[^"\\]|\\.)*)")?\s*\}\}/g;

export type Variable = { name: string; default: string };

export function rewriteVariables(body: string): { rewritten: string; vars: Variable[] } {
  const seen: Record<string, string> = {};
  const rewritten = body.replace(VAR_RE, (_match, name: string, def: string | undefined) => {
    const d = def ?? '';
    if (!(name in seen) || (seen[name] === '' && d !== '')) seen[name] = d;
    return `%recipient.${name}%`;
  });
  const vars = Object.entries(seen).map(([name, def]) => ({ name, default: def }));
  return { rewritten, vars };
}

export const DEFAULT_HTML_WRAPPER = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<title>{{subject}}</title>
</head>
<body style="font-family: -apple-system, system-ui, Segoe UI, Roboto, sans-serif; line-height:1.5; color:#222; max-width:640px; margin:24px auto; padding:0 16px;">
<span style="display:none !important;color:#fff;height:0;width:0;overflow:hidden;">{{preheader}}</span>
{{content}}
</body>
</html>`;

const UNSUB_PLACEHOLDER_RE = /\{\{\s*(unsubscribe|unsub_url)\s*(?:\|\s*"[^"]*")?\s*\}\}/;
export const UNSUB_FOOTER_HTML = '\n\n<p>&nbsp;<br><a href="{{unsubscribe}}">Unsubscribe</a></p>\n';
export const UNSUB_FOOTER_TEXT = '\n\nUnsubscribe:\n{{unsubscribe}}\n';

export function hasUnsubPlaceholder(s: string): boolean {
  return UNSUB_PLACEHOLDER_RE.test(s);
}

export function ensureUnsubFooterHTML(html: string): string {
  return hasUnsubPlaceholder(html) ? html : html + UNSUB_FOOTER_HTML;
}

export function ensureUnsubFooterText(text: string): string {
  return hasUnsubPlaceholder(text) ? text : text + UNSUB_FOOTER_TEXT;
}

export function applyWrapper(wrapper: string, content: string, subject: string, preheader: string): string {
  return wrapper
    .replaceAll('{{content}}', content)
    .replaceAll('{{subject}}', escapeHTML(subject))
    .replaceAll('{{preheader}}', escapeHTML(preheader));
}

export type BuiltBody = {
  html: string;
  text: string;
  vars: Variable[];
};

export function buildBody(
  markdownSrc: string,
  wrapperHTML: string,
  subject: string,
  preheader: string,
): BuiltBody {
  const wrapper = wrapperHTML || DEFAULT_HTML_WRAPPER;
  const operatorHasUnsub = hasUnsubPlaceholder(markdownSrc);

  // Rewrite placeholders on the markdown source first so the markdown engine
  // never sees the unrendered {{name | "default"}} form — otherwise the
  // double-quotes inside the placeholder get HTML-escaped to &quot; during
  // MD->HTML rendering and the post-render regex no longer matches.
  const { rewritten: rewrittenMD, vars: mdVars } = rewriteVariables(markdownSrc);

  const rendered = markdownToHTML(rewrittenMD);
  const htmlBody = operatorHasUnsub ? rendered : rendered + UNSUB_FOOTER_HTML;
  const wrapped = applyWrapper(wrapper, htmlBody, subject, preheader);
  // Second pass picks up placeholders that come from the wrapper or the
  // auto-injected unsub footer (e.g. {{unsubscribe}}), which never went
  // through the first markdown pass.
  const { rewritten: rewrittenHTML, vars: htmlVars } = rewriteVariables(wrapped);

  let text = htmlToText(markdownToHTML(rewrittenMD));
  if (!operatorHasUnsub) {
    text += rewriteVariables(UNSUB_FOOTER_TEXT).rewritten;
  }
  return { html: rewrittenHTML, text, vars: mergeVars(htmlVars, mdVars) };
}

function mergeVars(a: Variable[], b: Variable[]): Variable[] {
  const seen: Record<string, string> = {};
  for (const v of [...a, ...b]) {
    if (!(v.name in seen) || (seen[v.name] === '' && v.default !== '')) seen[v.name] = v.default;
  }
  return Object.entries(seen).map(([name, def]) => ({ name, default: def }));
}
