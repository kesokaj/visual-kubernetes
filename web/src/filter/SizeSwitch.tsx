import { metricAvailable, SIZE_METRICS, type Metrics } from '../lib/telemetry'
import { SIZE_LABEL, type SizeChoice } from './state'

// SizeSwitch is the pill's size-metric switch (PRD C3). Auto keeps the server's fallback
// (network, else CPU, else pods; PRD S2) and names what it picked; a metric the cluster does not
// report is disabled unless it is the one already pinned, so the presenter can always switch back.
export function SizeSwitch({
  size,
  metrics,
  onSelect,
}: {
  size: SizeChoice
  metrics: Metrics
  onSelect: (size: SizeChoice) => void
}) {
  const choices: readonly SizeChoice[] = ['auto', ...SIZE_METRICS]
  return (
    <div className="f-sec" data-testid="size-switch">
      <div className="f-sec-head">
        <span className="f-sec-title">Size by</span>
        {size === 'auto' && <span className="f-hint">auto: {SIZE_LABEL[metrics.sizeBy]}</span>}
      </div>
      <div className="f-mode f-size" role="group" aria-label="Size apps by">
        {choices.map((c) => {
          const on = c === size
          const off = c !== 'auto' && !on && !metricAvailable(metrics, c)
          const label = c === 'auto' ? 'Auto' : SIZE_LABEL[c]
          return (
            <button
              key={c}
              type="button"
              className={on ? 'f-mode-btn on' : 'f-mode-btn'}
              aria-pressed={on}
              data-size={c}
              disabled={off}
              title={off ? `This cluster reports no ${label.toLowerCase()} data` : undefined}
              onClick={() => onSelect(c)}
            >
              {label}
            </button>
          )
        })}
      </div>
    </div>
  )
}
