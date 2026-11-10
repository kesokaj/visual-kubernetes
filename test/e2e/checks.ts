import type { Page, Request } from '@playwright/test'

// Shared checks for the page's non-negotiables. Each one is validated in its must-fail state
// by web.spec.ts before the real page is held to it.

export type SmallText = { text: string; px: number }

// smallText lists every rendered text run whose on-screen size is under minPx (PRD R10). The
// size is the computed font-size times the element's screen scale, so text inside a zoomed
// SVG group counts at the size the audience sees.
export async function smallText(page: Page, minPx = 12): Promise<SmallText[]> {
  return page.evaluate((min) => {
    const out: { text: string; px: number }[] = []
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = n.textContent?.trim() ?? ''
      const el = n.parentElement
      if (!text || !el || el.getClientRects().length === 0) continue
      const style = getComputedStyle(el)
      if (style.visibility === 'hidden' || style.display === 'none') continue
      let px = parseFloat(style.fontSize)
      if (el instanceof SVGGraphicsElement) {
        const m = el.getScreenCTM()
        if (m) px *= Math.hypot(m.a, m.b)
      }
      if (px < min - 0.01) out.push({ text: text.slice(0, 40), px: Math.round(px * 100) / 100 })
    }
    return out
  }, minPx)
}

// foreignRequests returns the URLs not on origin (PRD R9). data: and blob: URLs never leave
// the browser and are allowed.
export function foreignRequests(urls: readonly string[], origin: string): string[] {
  return urls.filter((u) => {
    const scheme = u.slice(0, u.indexOf(':') + 1)
    if (scheme === 'data:' || scheme === 'blob:') return false
    try {
      return new URL(u).origin !== origin
    } catch {
      return true
    }
  })
}

// recordRequests collects every request URL the page makes from now on.
export function recordRequests(page: Page): string[] {
  const urls: string[] = []
  page.on('request', (r: Request) => urls.push(r.url()))
  return urls
}

// recordCspViolations collects requests the Content-Security-Policy blocked before they left:
// an attempted cross-origin load is a failure too, even though it never shows as a request.
export async function recordCspViolations(page: Page): Promise<string[]> {
  const blocked: string[] = []
  await page.exposeFunction('__vkCsp', (uri: string) => blocked.push(uri))
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      ;(window as unknown as { __vkCsp: (u: string) => void }).__vkCsp(e.blockedURI)
    })
  })
  return blocked
}

export const WEB_APP = 'app/shop/Deployment/web'
