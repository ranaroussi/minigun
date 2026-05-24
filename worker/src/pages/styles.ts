export const PAGE_STYLES = `
  :root { color-scheme: light dark; }
  body { font-family: -apple-system, system-ui, Segoe UI, Roboto, sans-serif; max-width: 640px; margin: 64px auto; padding: 0 16px; line-height: 1.5; color: #222; }
  @media (prefers-color-scheme: dark) { body { background: #111; color: #eee; } .card { background: #1c1c1c; border-color: #333; } button { background: #fff; color: #111; } a { color: #8ab4ff; } .row { border-color: #333; } }
  .card { border: 1px solid #ddd; border-radius: 8px; padding: 24px; }
  h1 { font-size: 1.25rem; margin: 0 0 4px; }
  h2 { font-size: 1rem; margin: 0 0 16px; color: #555; font-weight: 500; }
  @media (prefers-color-scheme: dark) { h2 { color: #aaa; } }
  p { margin: 0 0 16px; }
  .email { font-weight: 600; }
  .row { display: flex; align-items: flex-start; gap: 12px; padding: 12px 0; border-top: 1px solid #eee; }
  .row:first-of-type { border-top: 0; }
  .row input[type=checkbox] { margin-top: 4px; transform: scale(1.2); }
  .row label { flex: 1; cursor: pointer; }
  .row .name { font-weight: 600; }
  .row .desc { color: #777; font-size: 0.9rem; margin-top: 2px; }
  @media (prefers-color-scheme: dark) { .row .desc { color: #999; } }
  button { background: #111; color: #fff; border: 0; border-radius: 6px; padding: 12px 18px; font-size: 1rem; cursor: pointer; margin-top: 16px; }
  button:disabled { opacity: 0.5; cursor: not-allowed; }
  .muted { color: #777; font-size: 0.875rem; }
  .delta { padding: 4px 0; }
  .delta.sub { color: #060; }
  .delta.unsub { color: #800; }
  @media (prefers-color-scheme: dark) { .delta.sub { color: #6c6; } .delta.unsub { color: #f88; } }
`;
