/** LogLevel is the level word a log line is shown with. */
export type LogLevel = 'ERROR' | 'WARN' | 'INFO' | 'DEBUG' | 'TRACE' | 'FATAL'

/** LEVEL_COLOR is each level's colour, from design 02. */
export const LEVEL_COLOR: Record<LogLevel, string> = {
  INFO: '#7FB8FF',
  WARN: '#F2CF6B',
  ERROR: '#FF8C8F',
  FATAL: '#FF8C8F',
  DEBUG: '#949CA8',
  TRACE: '#949CA8',
}

/** The words that name a level, and the level each one means. */
const WORDS: Record<string, LogLevel> = {
  error: 'ERROR',
  err: 'ERROR',
  warn: 'WARN',
  warning: 'WARN',
  info: 'INFO',
  debug: 'DEBUG',
  trace: 'TRACE',
  fatal: 'FATAL',
}

/** pino's numeric levels. */
const NUMBERS: Record<number, LogLevel> = {
  10: 'TRACE',
  20: 'DEBUG',
  30: 'INFO',
  40: 'WARN',
  50: 'ERROR',
  60: 'FATAL',
}

/** A level word must start within this many characters of the line. */
const NEAR_START = 40

/**
 * A level word on its own: at the start, after a space or "level=" (logfmt),
 * maybe in brackets, maybe followed by ":", then a space or the end. So
 * "/info" and "information" are not levels.
 */
const WORD = /(?:^|[\s(]|level=)\[?(error|err|warning|warn|info|debug|trace|fatal)\]?:?(?=\s|$)/i

/**
 * detectLevel is the level of a log line: a level word near the start of the
 * line (ERROR/ERR, WARN/WARNING, INFO, DEBUG, TRACE, FATAL, any case), or the
 * level field of a JSON line. A line without one has no level (null).
 */
export function detectLevel(text: string): LogLevel | null {
  const trimmed = text.trimStart()
  if (trimmed.startsWith('{')) {
    const level = jsonLevel(trimmed)
    if (level !== undefined) return level
  }
  const m = WORD.exec(text)
  if (!m) return null
  // The match may start with a space, "level=" or "[": the word itself is its last letters.
  if (m.index + m[0].lastIndexOf(m[1]) >= NEAR_START) return null
  return WORDS[m[1].toLowerCase()]
}

/**
 * jsonLevel is a JSON line's level field as a level, null when the field is
 * there but names no level, undefined when the line is not a JSON object with one.
 */
function jsonLevel(text: string): LogLevel | null | undefined {
  let v: unknown
  try {
    v = JSON.parse(text)
  } catch {
    return undefined
  }
  if (typeof v !== 'object' || v === null || !('level' in v)) return undefined
  const level = (v as { level: unknown }).level
  if (typeof level === 'number') return NUMBERS[level] ?? null
  if (typeof level === 'string') return WORDS[level.toLowerCase()] ?? null
  return null
}
