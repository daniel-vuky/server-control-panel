import { useEffect, useState } from 'react'

/** useNow is the current time, refreshed every periodMs while the component is mounted. */
export function useNow(periodMs: number): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), periodMs)
    return () => clearInterval(t)
  }, [periodMs])
  return now
}
