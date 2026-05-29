import { describe, expect, it } from 'vitest';
import { canonicalizeClickURL } from '../src/store/events';

describe('canonicalizeClickURL', () => {
  it('lowercases scheme + host, strips query and fragment, preserves path case', () => {
    expect(canonicalizeClickURL('HTTPS://Example.COM/Path?utm=1#frag')).toBe(
      'https://example.com/Path',
    );
  });

  it('renders a bare host with a trailing slash (Go parity)', () => {
    expect(canonicalizeClickURL('https://Example.com')).toBe('https://example.com/');
  });

  it('trims surrounding whitespace', () => {
    expect(canonicalizeClickURL('  https://example.com/x  ')).toBe('https://example.com/x');
  });

  it('returns empty for blank input', () => {
    expect(canonicalizeClickURL('   ')).toBe('');
  });

  it('falls back to the trimmed input when unparseable', () => {
    expect(canonicalizeClickURL('  not a url  ')).toBe('not a url');
  });
});
