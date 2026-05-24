export class NotFoundError extends Error {
  constructor() {
    super('not found');
    this.name = 'NotFoundError';
  }
}

export class AlreadyExistsError extends Error {
  constructor() {
    super('already exists');
    this.name = 'AlreadyExistsError';
  }
}

export function isUniqueViolation(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err);
  return msg.includes('UNIQUE constraint failed') || msg.includes('constraint failed: UNIQUE');
}

export function nowISO(): string {
  return new Date().toISOString();
}

export type Company = {
  id: string;
  slug: string;
  name: string;
  created_at: string;
  updated_at: string;
};

export type CompanySummary = Company & { list_count: number };

export type List = {
  id: string;
  slug: string;
  name: string;
  description: string;
  weight: number;
  company_id: string;
  created_at: string;
  updated_at: string;
};

export type ListSummary = List & { subscribed_count: number };

export type ListDetails = ListSummary & {
  total_count: number;
  last_send_at?: string | null;
};

export type Contact = {
  id: string;
  email: string;
  params: string;
  created_at: string;
  updated_at: string;
};

export type Subscription = {
  id: number;
  list_id: string;
  contact_id: string;
  subscribed: boolean;
  subscribed_at?: string | null;
  updated_at: string;
  unsubscribed_at?: string | null;
};

export type ListContactRow = {
  subscription_id: number;
  contact_id: string;
  email: string;
  params: string;
  subscribed: boolean;
  subscribed_at?: string | null;
  unsubscribed_at?: string | null;
};

export type SendType = 'bulk' | 'single';
export type SendStatus = 'queued' | 'running' | 'completed' | 'failed' | 'cancelled' | 'paused';
export type BatchStatus = 'in_flight' | 'succeeded' | 'failed';
export type UnsubscribeMode = 'local' | 'mailgun' | 'external' | 'none';

export type Send = {
  id: string;
  type: SendType;
  list_id?: string | null;
  recipient_email?: string | null;
  subject: string;
  from_header: string;
  reply_to?: string | null;
  template_name?: string | null;
  body_md?: string | null;
  body_html?: string | null;
  body_text?: string | null;
  status: SendStatus;
  batch_size: number;
  throttle_ms: number;
  last_subscription_id: number;
  max_subscription_id?: number | null;
  total_recipients: number;
  unsubscribe_mode: UnsubscribeMode;
  unsubscribe_redirect_url?: string | null;
  unsubscribe_external_url?: string | null;
  notify_email?: string | null;
  last_error?: string | null;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type SendSummary = {
  id: string;
  type: SendType;
  list_id?: string | null;
  recipient_email?: string | null;
  subject: string;
  status: SendStatus;
  total_recipients: number;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type SendBatch = {
  id: string;
  send_id: string;
  batch_index: number;
  start_subscription_id: number;
  end_subscription_id: number;
  recipient_count: number;
  status: BatchStatus;
  mailgun_response?: string | null;
  created_at: string;
  updated_at: string;
};

export type Recipient = {
  subscription_id: number;
  contact_id: string;
  email: string;
  params: string;
};

export type ManageListState = {
  list: List;
  subscribed: boolean;
  subscribed_at?: string | null;
};

export type SubscriptionChange = {
  list_id: string;
  subscribed: boolean;
};

export type SubscriptionDelta = {
  list_id: string;
  list_name: string;
  was_subbed: boolean;
  now_subbed: boolean;
};

export type SendStats = {
  send_id: string;
  sent: number;
  delivered: number;
  opened: number;
  clicked: number;
  failed: number;
  complained: number;
  unsubscribed: number;
  first_fetched_at?: string | null;
  last_fetched_at?: string | null;
  next_fetch_at?: string | null;
  is_final: boolean;
  fetch_error?: string | null;
  created_at: string;
  updated_at: string;
};

export type DueStatsRow = {
  send_id: string;
  created_at: string;
  completed_at: string;
};
