const W = 160
const H = 32
/** TOP is the room kept under the stroke at the maximum, so the line is not clipped. */
const TOP = 2

/** run is a stretch of consecutive samples: the index of the first and the values. */
type Run = { start: number; values: number[] }

function runsOf(values: (number | null)[]): Run[] {
  const runs: Run[] = []
  let cur: Run | null = null
  values.forEach((v, i) => {
    if (v === null || !Number.isFinite(v)) {
      cur = null
      return
    }
    if (cur === null) {
      cur = { start: i, values: [] }
      runs.push(cur)
    }
    cur.values.push(v)
  })
  return runs
}

/**
 * Sparkline is the small area chart of a summary tile (design 01): 160 x 32
 * units stretched to the width, no axes or labels. Values are drawn from 0 to
 * `max` (above it they are clamped), a null breaks the line (each run is its
 * own area and line), and a lone value is a flat segment one bucket wide. It
 * is hidden from assistive tech: the tile's words carry the meaning.
 */
export default function Sparkline({
  values,
  max,
  color,
  fill,
}: {
  values: (number | null)[]
  max: number
  color: string
  fill: string
}) {
  const n = values.length
  const step = n > 1 ? W / (n - 1) : W
  const x = (i: number) => i * step
  const y = (v: number) => {
    const share = max > 0 ? Math.min(Math.max(v / max, 0), 1) : 0
    return H - share * (H - TOP)
  }
  const pt = (px: number, py: number) => `${px.toFixed(1)},${py.toFixed(1)}`

  return (
    <svg
      aria-hidden="true"
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      className="block h-8 w-full"
    >
      {runsOf(values).map((run) => {
        let pts: string[]
        let first: number
        let last: number
        if (run.values.length === 1) {
          // A lone sample: step forward a bucket, or back at the right edge.
          const at = x(run.start)
          const [a, b] = at + step <= W + 1e-6 ? [at, at + step] : [at - step, at]
          const py = y(run.values[0])
          pts = [pt(a, py), pt(b, py)]
          first = a
          last = b
        } else {
          pts = run.values.map((v, k) => pt(x(run.start + k), y(v)))
          first = x(run.start)
          last = x(run.start + run.values.length - 1)
        }
        const line = pts.join(' ')
        return (
          <g key={run.start}>
            <polygon points={`${pt(first, H)} ${line} ${pt(last, H)}`} fill={fill} />
            <polyline
              points={line}
              fill="none"
              stroke={color}
              strokeWidth={1.5}
              vectorEffect="non-scaling-stroke"
            />
          </g>
        )
      })}
    </svg>
  )
}
