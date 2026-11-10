// Small display formats for the panel: rates, CPU, memory and Event times.

export function bytesRate(bps: number): string {
  return `${bytes(bps)}/s`
}

export function bytes(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function cpu(milli: number): string {
  return milli >= 1000 ? `${(milli / 1000).toFixed(2)} cores` : `${Math.round(milli)}m`
}

export function memory(n: number): string {
  const mib = n / (1024 * 1024)
  return mib >= 1024 ? `${(mib / 1024).toFixed(1)} GiB` : `${Math.round(mib)} MiB`
}

// clock shows an RFC 3339 time as local HH:MM:SS, or the raw text when it does not parse.
export function clock(at: string): string {
  const t = Date.parse(at)
  return Number.isNaN(t) ? at : new Date(t).toLocaleTimeString([], { hour12: false })
}
