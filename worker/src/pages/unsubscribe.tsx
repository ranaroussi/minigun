import { html } from 'hono/html';
import { PAGE_STYLES } from './styles';

export type UnsubscribeData = {
  email: string;
  listName: string;
  token: string;
  turnstileSiteKey?: string;
  done?: boolean;
  error?: string;
};

export function UnsubscribePage(data: UnsubscribeData) {
  return html`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>Unsubscribe</title>
<meta name="viewport" content="width=device-width, initial-scale=1" />
${data.turnstileSiteKey
    ? html`<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>`
    : ''}
<style>${PAGE_STYLES}</style>
</head>
<body>
<div class="card">
${data.done
    ? data.listName
      ? html`<h1>You've been unsubscribed from ${data.listName}.</h1>
        ${data.email
          ? html`<p class="muted">${data.email} will no longer receive these emails.</p>`
          : html`<p class="muted">You will no longer receive these emails.</p>`}`
      : html`<h1>You're unsubscribed.</h1>
        ${data.email
          ? html`<p class="muted">${data.email} will no longer receive these emails.</p>`
          : html`<p class="muted">You will no longer receive these emails.</p>`}`
    : data.error
      ? html`<h1>That link is no longer valid.</h1>
        <p class="muted">${data.error}</p>`
      : html`<h1>Unsubscribe from ${data.listName}?</h1>
        <p>You're about to unsubscribe <span class="email">${data.email}</span> from this list.</p>
        <form method="POST" action="/u/${data.token}">
${data.turnstileSiteKey
            ? html`<div class="cf-turnstile" data-sitekey="${data.turnstileSiteKey}" data-action="unsubscribe"></div><br/>`
            : ''}
          <button type="submit">Confirm unsubscribe</button>
        </form>
        <p class="muted">If you didn't mean to click, you can safely close this page.</p>`}
</div>
</body>
</html>`;
}
