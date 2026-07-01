import { Env, selfCall } from '../env';
import { sendMessageWithRetry } from '../lib/mailgun';
import {
  createBatch,
  markBatchStatus,
  nextBatchIndex,
} from '../store/batches';
import {
  advanceSendCursor,
  getSend,
  hasInFlightBatch,
  setSendAudience,
  updateSendStatus,
} from '../store/sends';
import { countSubscribed, maxSubscriptionID, nextRecipientBatch } from '../store/subscriptions';
import { buildRecipientVars } from './render';

export type StepResult =
  | { state: 'busy' }
  | { state: 'completed' }
  | { state: 'sent'; batch_id: string; recipients: number; last_subscription_id: number };

export async function step(env: Env, sendID: string): Promise<StepResult> {
  if (await hasInFlightBatch(env.DB, sendID)) return { state: 'busy' };

  const snd = await getSend(env.DB, sendID);
  if (snd.status !== 'running') {
    await updateSendStatus(env.DB, sendID, 'running', null);
  }
  if (!snd.list_id) {
    await updateSendStatus(env.DB, sendID, 'failed', 'bulk send missing list_id');
    return { state: 'completed' };
  }
  // A scheduled send reaches dispatch with no frozen audience: resolve it now
  // (current max subscription id + count) so every contact subscribed up to
  // go-time is included, then persist so a crash/resume keeps the same
  // dispatch-time snapshot.
  if (snd.max_subscription_id == null) {
    const resolvedMax = await maxSubscriptionID(env.DB, snd.list_id);
    const total = await countSubscribed(env.DB, snd.list_id, resolvedMax);
    await setSendAudience(env.DB, sendID, resolvedMax, total);
    snd.max_subscription_id = resolvedMax;
    snd.total_recipients = total;
  }

  const recipients = await nextRecipientBatch(
    env.DB,
    snd.list_id,
    snd.last_subscription_id,
    snd.max_subscription_id,
    snd.batch_size,
  );
  if (recipients.length === 0) {
    await updateSendStatus(env.DB, sendID, 'completed', null);
    return { state: 'completed' };
  }

  const startID = recipients[0]!.subscription_id;
  const endID = recipients[recipients.length - 1]!.subscription_id;

  const recipVars: Record<string, Record<string, unknown>> = {};
  const subIDs: string[] = [];
  const emails: string[] = [];
  for (const r of recipients) {
    emails.push(r.email);
    subIDs.push(String(r.subscription_id));
    recipVars[r.email] = await buildRecipientVars(env, snd, r);
  }

  // Create the batch row (status 'in_flight') only now — after the CPU-heavy
  // recipient-variable build above — so an invocation killed mid-build (e.g.
  // exceededCpu) can't leave an orphaned 'in_flight' batch. An orphan is
  // doubly harmful: step() short-circuits to 'busy' while one exists, and
  // sweepStuckSends excludes sends that have one, so the send wedges with no
  // watchdog able to recover it. Narrowing the window to just the Mailgun
  // call + status write keeps that failure mode as small as possible.
  const batchIndex = await nextBatchIndex(env.DB, sendID);
  const batch = await createBatch(env.DB, sendID, batchIndex, startID, endID, recipients.length);

  try {
    const resp = await sendMessageWithRetry(env, {
      domain: snd.sending_domain,
      from: snd.from_header,
      to: emails,
      subject: snd.subject,
      html: snd.body_html ?? '',
      text: snd.body_text ?? '',
      replyTo: snd.reply_to ?? undefined,
      tag: snd.id,
      trackingOpens: true,
      trackingClicks: true,
      trackingUnsubscribeOn: false,
      listUnsubscribe: '<%recipient.unsub_url%>',
      listUnsubscribePost: 'List-Unsubscribe=One-Click',
      recipientVariables: recipVars,
      customVars: {
        minigun_send_id: snd.id,
        minigun_subscription_ids: subIDs.join(','),
        minigun_batch_id: batch.id,
      },
      testMode: !!snd.test_mode,
    });
    await markBatchStatus(env.DB, batch.id, 'succeeded', JSON.stringify(resp));
    await advanceSendCursor(env.DB, sendID, endID);
    return {
      state: 'sent',
      batch_id: batch.id,
      recipients: recipients.length,
      last_subscription_id: endID,
    };
  } catch (err) {
    const msg = (err as Error).message;
    await markBatchStatus(env.DB, batch.id, 'failed', msg);
    await updateSendStatus(env.DB, sendID, 'failed', msg);
    throw err;
  }
}

export function scheduleNextStep(
  env: Env,
  ctx: ExecutionContext,
  sendID: string,
  throttleMs: number,
): void {
  ctx.waitUntil(
    (async () => {
      if (throttleMs > 0) await new Promise((r) => setTimeout(r, throttleMs));
      try {
        const resp = await selfCall(env, `/send/${sendID}/next`);
        if (!resp.ok) {
          console.error('self-call non-ok', sendID, resp.status, await resp.text());
        }
      } catch (err) {
        console.error('self-call failed', sendID, err);
      }
    })(),
  );
}
