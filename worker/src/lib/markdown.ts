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
<body>
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

const WRAPPER_CONTENT_RE   = /\{\{\s*content\s*\}\}|<!--\s*content\s*-->/g;
const WRAPPER_SUBJECT_RE   = /\{\{\s*subject\s*\}\}|<!--\s*subject\s*-->/g;
const WRAPPER_PREHEADER_RE = /\{\{\s*preheader\s*\}\}|<!--\s*preheader\s*-->/g;

export function applyWrapper(wrapper: string, content: string, subject: string, preheader: string): string {
  // Use function replacements so `$` characters inside content (e.g. money
  // formatting, JS code snippets) aren't interpreted as capture-group refs.
  const sub = escapeHTML(subject);
  const pre = escapeHTML(preheader);
  return wrapper
    .replace(WRAPPER_CONTENT_RE, () => content)
    .replace(WRAPPER_SUBJECT_RE, () => sub)
    .replace(WRAPPER_PREHEADER_RE, () => pre);
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
  autoInjectUnsub: boolean = true,
): BuiltBody {
  const wrapper = wrapperHTML || DEFAULT_HTML_WRAPPER;
  const operatorHasUnsub = hasUnsubPlaceholder(markdownSrc);
  const addAutoFooter = autoInjectUnsub && !operatorHasUnsub;

  const { rewritten: rewrittenMD, vars: mdVars } = rewriteVariables(markdownSrc);

  const rendered = markdownToHTML(rewrittenMD);
  const htmlBody = addAutoFooter ? rendered + UNSUB_FOOTER_HTML : rendered;
  const wrapped = applyWrapper(wrapper, htmlBody, subject, preheader);
  const { rewritten: rewrittenHTML, vars: htmlVars } = rewriteVariables(wrapped);

  let text = htmlToText(markdownToHTML(rewrittenMD));
  if (addAutoFooter) {
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
