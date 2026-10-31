import { ApiError, getJSON, isFieldError, postJSON } from './client'
import type {
  DriveConnectRequest,
  DriveConnectResponse,
  DriveFolder,
  DriveFolderRequest,
  DriveStatus,
  DriveTestResult,
  FieldError,
} from './types.gen'

/** The Testing-mode note of the token_expired words (spec §2). */
export const TESTING_NOTE =
  'If the Google Cloud app is still in Testing, publish it: tokens issued in Testing expire after 7 days.'

/** DOCS_URL is the Google Drive setup guide on GitHub. */
export const DOCS_URL =
  'https://github.com/daniel-vuky/server-control-panel/blob/main/docs/google-drive.md'

/** The Drive error words of spec §2, by code. */
const WORDS: Record<string, string> = {
  token_expired: `The Google Drive token has expired. Reconnect Google Drive. ${TESTING_NOTE}`,
  no_access: 'The panel no longer has access to this folder.',
  folder_missing: 'The destination folder is missing or in the trash. Choose or create a folder.',
  quota: 'Google Drive is full.',
  unreachable: 'Google Drive is unreachable.',
  not_configured: 'Google Drive is not set up on this server.',
  not_connected: 'Connect Google Drive first',
  busy: 'A connection test is already running.',
}

/** driveWords gives the words of a Drive code; an unknown code gets fallback. */
export function driveWords(code: string, fallback = 'Something went wrong.'): string {
  return WORDS[code] ?? fallback
}

/**
 * connectWords gives the words of the code a failed connect came back with
 * (?drive=error&code=…). There no_access is Google refusing the app itself (a
 * wrong client id or secret, or an unknown redirect URI), not a folder; the
 * other codes keep their words. internal/drive's ConnectWords says the same.
 */
export function connectWords(code: string): string {
  if (code === 'no_access') {
    return 'Google refused the connection. Check GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET and the redirect URI (docs/google-drive.md).'
  }
  return driveWords(code, '')
}

/** DriveIssue is a Drive refusal: the code (when there is one) and its words. */
export type DriveIssue = { code: string; message: string }

/**
 * driveIssue reduces a thrown error to its code and words. A Drive route
 * answers {error: code, message: words}; any other error has no code.
 */
export function driveIssue(err: unknown): DriveIssue {
  if (err instanceof ApiError && typeof err.body === 'object' && err.body !== null) {
    const { error, message } = err.body as { error?: unknown; message?: unknown }
    if (typeof error === 'string' && typeof message === 'string' && message !== '') {
      return { code: error, message }
    }
  }
  return {
    code: '',
    message: err instanceof Error && err.message ? err.message : 'Something went wrong.',
  }
}

/** fieldError is the 422 body when it is about field, else null. */
export function fieldError(err: unknown, field: string): FieldError | null {
  if (
    err instanceof ApiError &&
    err.status === 422 &&
    isFieldError(err.body) &&
    err.body.field === field
  ) {
    return err.body
  }
  return null
}

/** fieldIssue is the words of a 422 field error on field, or null for any other error. */
export function fieldIssue(err: unknown, field: string): string | null {
  return fieldError(err, field)?.error ?? null
}

export function getDrive(signal?: AbortSignal): Promise<DriveStatus> {
  return getJSON<DriveStatus>('/api/drive', signal)
}

/** connectDrive stores a consent state and answers the Google consent URL to navigate to. */
export function connectDrive(returnTo: string): Promise<DriveConnectResponse> {
  const body: DriveConnectRequest = { returnTo }
  return postJSON<DriveConnectResponse>('/api/drive/connect', body)
}

export async function listDriveFolders(signal?: AbortSignal): Promise<DriveFolder[]> {
  const folders = await getJSON<DriveFolder[]>('/api/drive/folders', signal)
  if (!Array.isArray(folders)) throw new ApiError(200, null, 'Unexpected response from the server.')
  return folders
}

/** selectDriveFolder picks one of the panel's folders; createDriveFolder makes one by name and picks it. */
export function selectDriveFolder(id: string): Promise<DriveStatus> {
  const body: DriveFolderRequest = { id }
  return postJSON<DriveStatus>('/api/drive/folder', body)
}

export function createDriveFolder(name: string): Promise<DriveStatus> {
  const body: DriveFolderRequest = { name }
  return postJSON<DriveStatus>('/api/drive/folder', body)
}

/** testDrive uploads a 1 KB file to the folder and deletes it; a failure is {ok:false, code, message}. */
export function testDrive(): Promise<DriveTestResult> {
  return postJSON<DriveTestResult>('/api/drive/test', {})
}

export function disconnectDrive(): Promise<DriveStatus> {
  return postJSON<DriveStatus>('/api/drive/disconnect', {})
}
