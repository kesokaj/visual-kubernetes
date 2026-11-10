// nodeConditions reads status.conditions (type, status, reason, message) out of the YAML that
// /api/objects returns (sigs.k8s.io/yaml: keys sorted, two-space indent, list items at their
// parent's indent). It is a targeted reader for this one shape, not a YAML parser: anything it
// does not recognise yields no conditions and the panel falls back to the entity status.
export type Condition = { type: string; status: string; reason: string; message: string }

export function nodeConditions(yaml: string): Condition[] {
  const lines = yaml.split('\n')
  const start = conditionsLine(lines)
  if (start === null) return []
  const out: Record<string, string>[] = []
  let cur: Record<string, string> | null = null
  let keyIndent = -1
  let last = ''
  for (let i = start.at + 1; i < lines.length; i++) {
    const line = lines[i] ?? ''
    const text = line.trimStart()
    if (!text) continue
    const indent = line.length - text.length
    if (text.startsWith('- ') && indent >= start.indent && indent <= start.indent + 2) {
      cur = {}
      out.push(cur)
      keyIndent = indent + 2
      last = put(cur, text.slice(2), '')
    } else if (indent <= start.indent) {
      break
    } else if (cur && indent === keyIndent) {
      last = put(cur, text, last)
    } else if (cur && indent > keyIndent && last) {
      cur[last] = `${cur[last] ?? ''} ${unquote(text)}`.trim() // a folded long value
    }
  }
  return out
    .filter((c) => c.type)
    .map((c) => ({ type: c.type ?? '', status: c.status ?? '', reason: c.reason ?? '', message: c.message ?? '' }))
}

// conditionsLine finds `conditions:` directly inside the top-level `status:` block.
function conditionsLine(lines: string[]): { at: number; indent: number } | null {
  const s = lines.findIndex((l) => /^status:\s*$/.test(l))
  if (s < 0) return null
  for (let i = s + 1; i < lines.length; i++) {
    const line = lines[i] ?? ''
    if (/^\S/.test(line)) return null
    const m = /^( +)conditions:\s*$/.exec(line)
    if (m?.[1] !== undefined) return { at: i, indent: m[1].length }
  }
  return null
}

function put(into: Record<string, string>, text: string, last: string): string {
  const m = /^([A-Za-z][\w-]*):(?:\s+(.*))?$/.exec(text)
  if (!m?.[1]) return last
  into[m[1]] = unquote(m[2] ?? '')
  return m[1]
}

function unquote(v: string): string {
  const t = v.trim()
  if (t.length >= 2 && (t[0] === '"' || t[0] === "'") && t[t.length - 1] === t[0]) return t.slice(1, -1)
  return t
}
