import type { Graph } from '../lib/graph'
import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import {
  findServicesForEntity,
  serviceReadOnlyReason,
  type ServiceType,
} from './configApi'
import { useServiceType } from './useServiceType'

type Props = {
  readonly graph: Graph
  readonly entity: Entity
  readonly role: string
  readonly onTypeChanged: (serviceId: string, nextType: string) => void
}

export function ServiceTypeSection({ graph, entity, role, onTypeChanged }: Props) {
  const services = findServicesForEntity(graph, entity, role)
  if (services.length === 0) return null

  return (
    <Section title="Service exposure · type" name="demo-service-type">
      <p className="demo-note">
        Switch ClusterIP ↔ LoadBalancer for live demos (LoadBalancer exposes the demo proxy).
      </p>
      <div className="demo-card-stack">
        {services.map((svc) => {
          const readOnly = serviceReadOnlyReason(svc)
          if (readOnly) {
            return (
              <div
                key={svc.id}
                className="demo-card"
                data-service-card={svc.id}
                data-testid="service-type-readonly"
              >
                <div className="demo-card-head">
                  <b>{svc.name}</b>
                  <span className="demo-badge closed">{svc.status || 'ClusterIP'}</span>
                </div>
                <p className="demo-note">{readOnly}</p>
              </div>
            )
          }
          return <ServiceTypeCard key={svc.id} service={svc} onTypeChanged={onTypeChanged} />
        })}
      </div>
    </Section>
  )
}

function ServiceTypeCard({
  service,
  onTypeChanged,
}: {
  readonly service: Entity
  readonly onTypeChanged: (serviceId: string, nextType: string) => void
}) {
  const st = useServiceType(service.id, onTypeChanged)
  const payload = st.payload
  const blocked = Boolean(payload?.headless) || (payload !== null && payload.allowedTypes.length === 0)

  return (
    <div className="demo-card" data-service-card={service.id}>
      <div className="demo-card-head">
        <b>{service.name}</b>
        <span className="demo-badge closed" data-testid={`service-current-type-${service.name}`}>
          {payload?.type ?? (service.status || 'Loading')}
        </span>
      </div>

      {st.loading && !payload && (
        <p className="demo-note" data-testid="service-type-loading">
          Loading Service configuration…
        </p>
      )}

      {st.conflict && (
        <div className="demo-conflict" role="alert" data-testid="service-type-conflict">
          <span>{st.error ?? 'Service changed on the cluster.'}</span>
          <button
            type="button"
            className="demo-btn"
            data-testid="service-type-reload"
            onClick={st.reload}
          >
            Reload
          </button>
        </div>
      )}

      {st.error && !st.conflict && (
        <p className="demo-err" role="alert" data-testid="service-type-error">
          {st.error}
        </p>
      )}

      {st.notice && (
        <p className="demo-ok" role="status" data-testid="service-type-saved">
          {st.notice}
        </p>
      )}

      {payload && (
        <>
          <div className="demo-card-sub">
            <span className="demo-exp">
              {payload.headless ? 'Headless (clusterIP: None)' : `ClusterIP ${payload.clusterIP || 'assigned'}`}
            </span>
          </div>
          {blocked ? (
            <p className="demo-note" data-testid="service-type-blocked">
              This Service cannot switch exposure type.
            </p>
          ) : (
            <div className="demo-segmented-row">
              <div className="demo-segmented" role="group" aria-label={`Service type for ${service.name}`}>
                {payload.allowedTypes.map((opt: ServiceType) => {
                  const active = st.draftType === opt
                  return (
                    <button
                      key={opt}
                      type="button"
                      className={`chip${active ? ' active' : ''}`}
                      aria-pressed={active}
                      disabled={st.busy}
                      data-testid={`service-type-option-${opt}`}
                      onClick={() => st.selectType(opt)}
                    >
                      {opt}
                    </button>
                  )
                })}
              </div>
              <button
                type="button"
                className="demo-btn primary"
                disabled={st.busy || !st.dirty}
                data-testid="service-type-save"
                onClick={() => void st.save()}
              >
                {st.saving ? 'Saving…' : 'Apply'}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
