import type { Chain } from './wire'

// MAX_CHAINS matches the builder's bound (internal/replay maxChains): the page never keeps more
// chains than the server can still be extending.
export const MAX_CHAINS = 16

// Chains keeps the newest copy of each causal chain by id and remembers which chain changed
// last, the one R replays. A chain is resent whole as it grows (plan D17), so a copy replaces
// the previous one in place; the oldest chain is dropped past MAX_CHAINS.
export class Chains {
  private readonly byId = new Map<string, Chain>()
  private lastId: string | null = null

  add(c: Chain): void {
    this.byId.delete(c.id) // re-insert so Map order is "least recently changed first"
    this.byId.set(c.id, c)
    this.lastId = c.id
    while (this.byId.size > MAX_CHAINS) {
      const oldest = this.byId.keys().next().value
      if (oldest === undefined) break
      this.byId.delete(oldest)
    }
  }

  get(id: string): Chain | undefined {
    return this.byId.get(id)
  }

  last(): Chain | undefined {
    return this.lastId === null ? undefined : this.byId.get(this.lastId)
  }

  get size(): number {
    return this.byId.size
  }
}
