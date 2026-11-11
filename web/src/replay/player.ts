import type { Chains } from '../lib/chains'
import type { Chain } from '../lib/wire'

// STEP_MS is the replay pace: about one step a second, slow enough to narrate (plan Phase 5).
export const STEP_MS = 1000

// Play is what the caption shows: the chain being replayed, the step on screen (0-based) and
// whether the presenter paused it. null means nothing is being replayed.
export type Play = { chain: Chain; index: number; paused: boolean } | null

// Player replays one causal chain at STEP_MS a step. R (replay) starts the last chain from its
// first step, Space (togglePause) pauses or resumes, → (step) shows the next step at once and
// keeps the pause state, Esc (clear) hides the caption. On each step the player re-reads the
// chain from the store, and grew picks up a chain that was still being written when playback
// caught up with it, so steps that arrive while it plays are replayed too. It stops on the
// last step it has and leaves it on screen.
export class Player {
  play: Play = null
  private timer: ReturnType<typeof setTimeout> | undefined
  private readonly chains: Chains
  private readonly changed: () => void
  private readonly stepMs: number

  constructor(chains: Chains, changed: () => void, stepMs = STEP_MS) {
    this.chains = chains
    this.changed = changed
    this.stepMs = stepMs
  }

  replay(): void {
    const chain = this.chains.last()
    if (!chain) return
    this.set({ chain, index: 0, paused: false })
  }

  togglePause(): void {
    if (this.play) this.set({ ...this.play, paused: !this.play.paused })
  }

  // step shows the next step; with nothing playing it opens the last chain, paused, on step 1.
  step(): void {
    if (!this.play) {
      const chain = this.chains.last()
      if (chain) this.set({ chain, index: 0, paused: true })
      return
    }
    this.set(this.advanced(this.play))
  }

  clear(): void {
    this.set(null)
  }

  // grew is called on every store change: when the store holds a longer copy of the chain on
  // screen and no tick is pending (playback caught up, or it is paused), it takes the longer
  // copy, which re-arms the pace timer unless paused. A pending tick re-reads the store itself.
  grew(): void {
    if (!this.play || this.timer !== undefined) return
    const chain = this.chains.get(this.play.chain.id)
    if (chain && chain.steps.length > this.play.chain.steps.length) this.set({ ...this.play, chain })
  }

  dispose(): void {
    clearTimeout(this.timer)
  }

  private advanced(p: NonNullable<Play>): NonNullable<Play> {
    const chain = this.chains.get(p.chain.id) ?? p.chain
    return { ...p, chain, index: Math.min(p.index + 1, chain.steps.length - 1) }
  }

  // set shows a new state and re-arms the pace timer: one tick per STEP_MS while playing and
  // not on the last step, counted from the latest change so → never doubles a step.
  private set(next: Play): void {
    this.play = next
    clearTimeout(this.timer)
    this.timer = undefined
    if (next && !next.paused && next.index < next.chain.steps.length - 1) {
      this.timer = setTimeout(() => this.tick(), this.stepMs)
    }
    this.changed()
  }

  private tick(): void {
    if (this.play && !this.play.paused) this.set(this.advanced(this.play))
  }
}
