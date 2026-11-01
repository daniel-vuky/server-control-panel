import { ApiError, getJSON, isFieldError, postJSON } from './client'
import type {
  AlertEnabledRequest,
  AlertsConfig,
  AlertTestResult,
  AlertThresholds,
  EmailRequest,
  FieldError,
  TelegramRequest,
} from './types.gen'

/** AlertChannelName is a {channel} path segment of the alerts routes. */
export type AlertChannelName = 'telegram' | 'email'

const unexpected = () => new ApiError(200, null, 'Unexpected response from the server.')

/** checked is cfg when it has the shape of an AlertsConfig, else the Unexpected response error. */
function checked(cfg: AlertsConfig): AlertsConfig {
  if (
    typeof cfg !== 'object' ||
    cfg === null ||
    typeof cfg.telegram !== 'object' ||
    cfg.telegram === null ||
    typeof cfg.email !== 'object' ||
    cfg.email === null ||
    typeof cfg.thresholds !== 'object' ||
    cfg.thresholds === null
  ) {
    throw unexpected()
  }
  return {
    ...cfg,
    telegram: { ...cfg.telegram, recipients: cfg.telegram.recipients ?? [] },
    email: { ...cfg.email, recipients: cfg.email.recipients ?? [] },
  }
}

/** getAlerts is the channels (hints only, never the bot token) and the server-wide thresholds. */
export async function getAlerts(signal?: AbortSignal): Promise<AlertsConfig> {
  return checked(await getJSON<AlertsConfig>('/api/alerts', signal))
}

/**
 * saveTelegram stores the bot and chat. An empty token keeps the stored one.
 * 409 no_secret_key without PANEL_SECRET_KEY, 422 on token or chatId.
 */
export async function saveTelegram(body: TelegramRequest): Promise<AlertsConfig> {
  return checked(await postJSON<AlertsConfig>('/api/alerts/telegram', body))
}

/** saveEmail stores the recipients (1 to 10, separated by commas or new lines). 409 not_configured, 422 on recipients. */
export async function saveEmail(body: EmailRequest): Promise<AlertsConfig> {
  return checked(await postJSON<AlertsConfig>('/api/alerts/email', body))
}

/** setAlertChannel turns a set-up channel on or off; 409 not_configured otherwise. */
export async function setAlertChannel(
  channel: AlertChannelName,
  enabled: boolean,
): Promise<AlertsConfig> {
  const body: AlertEnabledRequest = { enabled }
  return checked(await postJSON<AlertsConfig>(`/api/alerts/${channel}/enabled`, body))
}

/** testAlertChannel sends a test message, whether the channel is on or off: {ok, message}, 409 not_configured. */
export async function testAlertChannel(channel: AlertChannelName): Promise<AlertTestResult> {
  const r = await postJSON<AlertTestResult>(`/api/alerts/${channel}/test`, {})
  if (typeof r !== 'object' || r === null || typeof r.ok !== 'boolean') throw unexpected()
  return r
}

/** saveAlertThresholds stores the server-wide thresholds; 422 names the first field in error. */
export async function saveAlertThresholds(body: AlertThresholds): Promise<AlertsConfig> {
  return checked(await postJSON<AlertsConfig>('/api/alerts/thresholds', body))
}

/** alertFieldError is the 422 body when it is about one of fields, else null. */
export function alertFieldError<F extends string>(
  err: unknown,
  fields: readonly F[],
): (FieldError & { field: F }) | null {
  if (
    err instanceof ApiError &&
    err.status === 422 &&
    isFieldError(err.body) &&
    (fields as readonly string[]).includes(err.body.field)
  ) {
    return err.body as FieldError & { field: F }
  }
  return null
}

/**
 * alertWords is the words of a failed alerts call: a 409 body carries them in
 * message (no_secret_key, not_configured), anything else is the error's own.
 */
export function alertWords(err: unknown): string {
  if (err instanceof ApiError && typeof err.body === 'object' && err.body !== null) {
    const { message } = err.body as { message?: unknown }
    if (typeof message === 'string' && message !== '') return message
  }
  return err instanceof Error && err.message ? err.message : 'Something went wrong.'
}
