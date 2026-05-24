import { Env } from '../env';
import { sendMessageWithRetry } from '../lib/mailgun';
import { getSend, updateSendStatus } from '../store/sends';

export async function runSingle(env: Env, sendID: string): Promise<void> {
  const snd = await getSend(env.DB, sendID);
  if (!snd.recipient_email) {
    await updateSendStatus(env.DB, sendID, 'failed', 'single send missing recipient_email');
    return;
  }
  await updateSendStatus(env.DB, sendID, 'running', null);
  try {
    await sendMessageWithRetry(env, {
      from: snd.from_header,
      to: [snd.recipient_email],
      subject: snd.subject,
      html: snd.body_html ?? '',
      text: snd.body_text ?? '',
      replyTo: snd.reply_to ?? undefined,
      tag: snd.id,
      trackingOpens: true,
      trackingClicks: true,
      trackingUnsubscribeOn: false,
    });
    await updateSendStatus(env.DB, sendID, 'completed', null);
  } catch (err) {
    await updateSendStatus(env.DB, sendID, 'failed', (err as Error).message);
  }
}
