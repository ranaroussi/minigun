const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;

export function isValidSlug(s: string): boolean {
  return SLUG_RE.test(s);
}

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function isValidEmail(s: string): boolean {
  return EMAIL_RE.test(s);
}

export function nowISO(): string {
  return new Date().toISOString();
}
