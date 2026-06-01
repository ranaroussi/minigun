import { describe, expect, it } from 'vitest';
import { buildBody } from '../src/lib/markdown';

// The bulk and single send routes both render the Markdown body through a
// caller-supplied wrapper template; these lock that contract so a regression
// (e.g. dropping the wrapper for bulk) is caught.
describe('buildBody wrapper template', () => {
  it('wraps the rendered markdown into the supplied template', () => {
    const wrapper =
      '<html><body><header id="brand">Acme</header>{{content}}</body></html>';
    const { html } = buildBody('Hello **world**', wrapper, 'Subj', '', true);

    expect(html).toContain('<header id="brand">Acme</header>');
    expect(html).toContain('<strong>world</strong>');
    expect(html).not.toContain('{{content}}');
  });

  it('substitutes {{subject}} and {{preheader}} in the wrapper', () => {
    const wrapper =
      '<html><head><title>{{subject}}</title></head><body><span>{{preheader}}</span>{{content}}</body></html>';
    const { html } = buildBody('Body', wrapper, 'Weekly digest', 'Sneak peek', true);

    expect(html).toContain('<title>Weekly digest</title>');
    expect(html).toContain('<span>Sneak peek</span>');
    expect(html).not.toContain('{{subject}}');
    expect(html).not.toContain('{{preheader}}');
  });

  it('skips the auto-footer when the wrapper supplies an unsubscribe link', () => {
    const wrapper =
      '<html><body>{{content}}<footer><a href="{{ unsubscribe }}">unsub</a></footer></body></html>';
    const { html, text } = buildBody('Hello', wrapper, 'Subj', '', true);

    expect((html.match(/%recipient\.unsubscribe%/g) ?? []).length).toBe(1);
    expect(html).not.toContain('Unsubscribe</a></p>');
    expect(text).not.toContain('Unsubscribe:');
  });

  it('auto-injects the footer when neither body nor wrapper has unsub', () => {
    const wrapper = '<html><body>{{content}}</body></html>';
    const { html } = buildBody('Hello', wrapper, 'Subj', '', true);

    expect(html).toContain('Unsubscribe</a></p>');
  });
});
