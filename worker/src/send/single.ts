import { Env, publicURL } from '../env';
import { sendMessageWithRetry } from '../lib/mailgun';
import { sign as signToken } from '../lib/token';
import { getSend, updateSendStatus } from '../store/sends';

export async function runSingle(env: Env, sendID: string): Promise<void> {
  const snd = await getSend(env.DB, sendID);
  if (!snd.recipient_email) {
    await updateSendStatus(env.DB, sendID, 'failed', 'single send missing recipient_email');
    return;
  }
  await updateSendStatus(env.DB, sendID, 'running', null);
  try {
    let html = snd.body_html ?? '';
    let text = snd.body_text ?? '';
    let listUnsub: string | undefined;
    let listUnsubPost: string | undefined;
    if (snd.last_subscription_id && snd.last_subscription_id > 0) {
      const tok = await signToken(env.MINIGUN_HMAC_SECRET, snd.id, snd.last_subscription_id);
      const unsubURL = `${publicURL(env)}/u/${tok}`;
      html = html
        .replaceAll('%recipient.unsubscribe%', unsubURL)
        .replaceAll('%recipient.unsub_url%', unsubURL);
      text = text
        .replaceAll('%recipient.unsubscribe%', unsubURL)
        .replaceAll('%recipient.unsub_url%', unsubURL);
      listUnsub = `<${unsubURL}>`;
      listUnsubPost = 'List-Unsubscribe=One-Click';
    }
    await sendMessageWithRetry(env, {
      domain: snd.sending_domain,
      from: snd.from_header,
      to: [snd.recipient_email],
      subject: snd.subject,
      html,
      text,
      replyTo: snd.reply_to ?? undefined,
      tag: snd.id,
      trackingOpens: true,
      trackingClicks: true,
      trackingUnsubscribeOn: false,
      testMode: !!snd.test_mode,
      listUnsubscribe: listUnsub,
      listUnsubscribePost: listUnsubPost,
      // v:minigun_send_id is a redundant safety net for the events archive.
      // The o:tag above is the primary anchor (it's how the events-pull
      // cron filters Mailgun's events API by send_id). The user variable
      // makes the send_id available inside every event's user_variables
      // blob without re-parsing the tag — useful for richer queries later.
      customVars: {
        minigun_send_id: snd.id,
      },
    });
    await updateSendStatus(env.DB, sendID, 'completed', null);
  } catch (err) {
    await updateSendStatus(env.DB, sendID, 'failed', (err as Error).message);
  }
}
