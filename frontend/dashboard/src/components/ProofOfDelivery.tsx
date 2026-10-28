import { PROOF_CHECK_LABEL, PROOF_RESULT_LABEL, type DeliveryProof, type Plan, type ProofCheckResult, type RouteStop } from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { Chip, Dialog, StatusLabel, type Tone } from '@loong/ui'
import { ChevronRight, ShieldCheck, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { t } from '../i18n'

const RESULT_TONE: Record<ProofCheckResult, Tone> = { ok: 'success', unclear: 'warning', missing: 'danger' }

/** Ringkasan AI atas foto: kalimat ringkas, tiga pemeriksaan, keyakinan, dan catatan bahwa AI bisa keliru. */
function AiSummary({ proof }: { proof: DeliveryProof }) {
  const a = proof.analysis
  if (!a) return null
  return (
    <div className="space-y-3 rounded-input bg-sunken p-3.5">
      <div className="flex items-center justify-between gap-2">
        <p className="flex items-center gap-1.5 text-small font-semibold">
          <Sparkles className="size-4 text-accent" aria-hidden />
          {t('dash.plans.proofAiTitle')}
        </p>
        <Chip className="bg-surface">{t('dash.ai.confidence', { pct: Math.round(a.confidence * 100) })}</Chip>
      </div>
      <p className="text-body">{renderMsg(a.summary)}</p>
      <ul aria-label={t('dash.plans.proofAiChecks')} className="divide-y divide-line">
        {a.checks.map((c) => (
          <li key={c.key} className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 py-2 first:pt-0 last:pb-0">
            <div className="min-w-0">
              <p className="text-small font-semibold">{PROOF_CHECK_LABEL[c.key]}</p>
              <p className="text-small text-muted">{renderMsg(c.note)}</p>
            </div>
            <StatusLabel tone={RESULT_TONE[c.result]} className="text-small text-muted">
              {PROOF_RESULT_LABEL[c.result]}
            </StatusLabel>
          </li>
        ))}
      </ul>
      <p className="text-caption text-subtle">{t('dash.plans.proofAiNote', { model: a.model })}</p>
    </div>
  )
}

/**
 * Bukti antar di bawah stop-nya pada urutan stop: baris ringkas (siapa, jam, kutipan ringkasan AI) yang membuka rincian
 * lengkap. Ditaruh di baris stop agar jelas bukti ini milik titik antar yang mana; dialog mengulang titik, pengiriman,
 * dan penerimanya. Kosong untuk stop yang belum selesai atau tanpa bukti.
 */
export function StopProof({ plan, stop }: { plan: Plan; stop: RouteStop }) {
  const [open, setOpen] = useState(false)
  const proof = stop.proof
  if (stop.kind !== 'antar' || stop.status !== 'selesai' || !proof) return null

  const shipmentIndex = plan.shipments.findIndex((s) => s.id === stop.shipmentId)
  const shipment = plan.shipments[shipmentIndex]
  const name = shipment?.dropoff.contactName ?? stop.waypoint.contactName
  const place = stop.waypoint.name

  if (proof.kind !== 'foto' || !proof.photo) {
    return (
      <p className="mt-1.5 flex items-start gap-1.5 text-small text-muted">
        <ShieldCheck className="mt-0.5 size-4 shrink-0 text-success-fg" aria-hidden />
        {t('dash.plans.proofOtpLine', { name, time: proof.at })}
      </p>
    )
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="mt-2 flex w-full items-center gap-3 rounded-input bg-sunken p-2.5 text-left transition-colors hover:bg-line focus-visible:outline-2 focus-visible:outline-accent"
      >
        <img src={proof.photo} alt="" className="h-20 w-[3.0625rem] shrink-0 rounded-[0.5rem] border border-line object-cover" />
        <span className="min-w-0 flex-1 space-y-1">
          <span className="block text-small font-semibold">{t('dash.plans.proofTitle')}</span>
          <span className="block text-small text-muted">{t('dash.plans.proofPhotoLine', { name, time: proof.at })}</span>
          {proof.analysis && (
            <span className="flex items-start gap-1.5 text-small">
              <Sparkles className="mt-0.5 size-3.5 shrink-0 text-accent" aria-hidden />
              <span className="line-clamp-2">{renderMsg(proof.analysis.summary)}</span>
            </span>
          )}
          <span className="inline-flex items-center gap-0.5 text-small font-medium text-accent">
            {t('dash.plans.proofDetails')}
            <ChevronRight className="size-4" aria-hidden />
          </span>
        </span>
      </button>

      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t('dash.plans.proofTitle')}
        description={
          <>
            <span className="block font-semibold text-fg">{t('ui.stopList.dropoff', { place })}</span>
            {shipment && <span className="block">{t('dash.plans.shipmentHeading', { n: shipmentIndex + 1, from: shipment.pickup.name, to: shipment.dropoff.name })}</span>}
            <span className="block">
              {t('dash.plans.proofRecipient', { name })} · {t('dash.plans.proofPhotoTaken', { time: proof.at })}
            </span>
          </>
        }
        className="max-w-xl"
      >
        <div className="grid gap-4 pb-2 sm:grid-cols-[11rem_minmax(0,1fr)]">
          <img src={proof.photo} alt={t('dash.plans.proofPhotoAlt', { name, place })} className="mx-auto max-h-[50vh] w-44 rounded-card border border-line object-cover sm:w-full" />
          <AiSummary proof={proof} />
        </div>
      </Dialog>
    </>
  )
}
