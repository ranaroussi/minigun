import { html } from 'hono/html';
import { ManageListState, SubscriptionDelta } from '../store/types';
import { PAGE_STYLES } from './styles';

export type ManageData = {
  email: string;
  companyName: string;
  token: string;
  lists: ManageListState[];
  done?: boolean;
  deltas?: SubscriptionDelta[];
  error?: string;
};

export function ManagePage(data: ManageData) {
  return html`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>Manage preferences</title>
<meta name="viewport" content="width=device-width, initial-scale=1" />
<style>${PAGE_STYLES}</style>
</head>
<body>
<div class="card">
${data.error
    ? html`<h1>That link is no longer valid.</h1>
      <p class="muted">${data.error}</p>`
    : data.done
      ? html`<h1>Your preferences have been saved.</h1>
        <p class="muted">Changes for <span class="email">${data.email}</span>:</p>
${data.deltas && data.deltas.length > 0
          ? data.deltas.map(
              (d) => html`<div class="delta ${d.now_subbed ? 'sub' : 'unsub'}">
                ${d.now_subbed ? 'Subscribed to' : 'Unsubscribed from'} <strong>${d.list_name}</strong>
              </div>`,
            )
          : html`<p class="muted">No changes were made.</p>`}`
      : html`<h1>Manage your email preferences</h1>
        <h2>${data.companyName}</h2>
        <p>You're managing subscriptions for <span class="email">${data.email}</span>.</p>
        <form method="POST" action="/manage/${data.token}">
${data.lists.map(
            (s) => html`<div class="row">
              <input type="checkbox" id="list-${s.list.id}" name="list" value="${s.list.id}" ${s.subscribed ? 'checked' : ''} />
              <label for="list-${s.list.id}">
                <div class="name">${s.list.name}</div>
${s.list.description
                  ? html`<div class="desc">${s.list.description}</div>`
                  : ''}
              </label>
            </div>`,
          )}
          <button type="submit">Save preferences</button>
        </form>
        <p class="muted">Uncheck a list to unsubscribe. Check it to subscribe. Then click <em>Save preferences</em>.</p>`}
</div>
</body>
</html>`;
}
