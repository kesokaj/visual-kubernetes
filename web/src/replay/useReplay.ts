import { useEffect, useReducer, useState } from 'react'
import type { Graph } from '../lib/graph'
import { Player, type Play } from './player'

// useReplay owns the page's one Player: it re-renders the page on every step, tells the player
// when the graph changes (a chain may have grown) and stops the pace timer when the page goes
// away.
export function useReplay(graph: Graph): { player: Player; play: Play } {
  const [, render] = useReducer((n: number) => n + 1, 0)
  const [player] = useState(() => new Player(graph.chains, render))
  useEffect(() => {
    const unsubscribe = graph.subscribe(() => player.grew())
    return () => {
      unsubscribe()
      player.dispose()
    }
  }, [graph, player])
  return { player, play: player.play }
}
