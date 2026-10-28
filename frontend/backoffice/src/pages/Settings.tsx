import { errorMessage, useApi, useMutation, useSettings as useSharedSettings } from '@loong/data'
import type { AiFeatures, DispatchSettings } from '@loong/domain'
import { LocaleSwitcher } from '@loong/shell'
import { Card, CardHeader, PageHeader, Segmented, Switch, toast } from '@loong/ui'
import { t } from '../i18n'
import type { enBoSettings } from '../i18n/en/settings'
import { useSettings, type Density } from '../lib/settings'

// label disimpan sebagai kunci; t() dipanggil saat render agar mengikuti bahasa
const AI_FEATURES = [
  { key: 'vision', label: 'bo.settings.aiVisionLabel', description: 'bo.settings.aiVisionDescription' },
  { key: 'voice', label: 'bo.settings.aiVoiceLabel', description: 'bo.settings.aiVoiceDescription' },
  { key: 'intent', label: 'bo.settings.aiIntentLabel', description: 'bo.settings.aiIntentDescription' },
  { key: 'copilot', label: 'bo.settings.aiCopilotLabel', description: 'bo.settings.aiCopilotDescription' },
] as const satisfies readonly { key: keyof AiFeatures; label: keyof typeof enBoSettings; description: keyof typeof enBoSettings }[]

/**
 * Pengaturan operator. Parameter algoritma rute sengaja tidak ada di sini (di luar lingkup Backoffice);
 * tarif diatur per kendaraan di halaman Kendaraan.
 */
export function Settings() {
  const api = useApi()
  const [settings, update] = useSettings()
  const shared = useSharedSettings()
  const ai = shared.data?.ai
  const autoDispatch = shared.data?.dispatch.auto ?? true
  const toggleDispatch = useMutation({
    mutationFn: (patch: Partial<DispatchSettings>) => api.settings.update({ dispatch: patch }),
    onSuccess: (s) => toast.success(s.dispatch.auto ? t('bo.settings.toastAutoOn') : t('bo.settings.toastAutoOff')),
    onError: (err) => toast.error(errorMessage(err)),
  })
  const toggleAi = useMutation({
    mutationFn: (patch: Partial<AiFeatures>) => api.settings.update({ ai: patch }),
    onSuccess: () => toast.success(t('bo.settings.toastAiSaved')),
    onError: (err) => toast.error(errorMessage(err)),
  })

  return (
    <div className="max-w-2xl space-y-4">
      <PageHeader title={t('bo.settings.title')} description={t('bo.settings.description')} />

      <Card className="space-y-4 p-card">
        <CardHeader title={t('bo.settings.displayTitle')} description={t('bo.settings.displayDescription')} />
        <Segmented<Density>
          aria-label={t('bo.settings.densityLabel')}
          value={settings.density}
          onValueChange={(density) => update({ density })}
          options={[
            { value: 'compact', label: t('bo.settings.densityCompact') },
            { value: 'comfortable', label: t('bo.settings.densityComfortable') },
          ]}
        />
        <p className="text-small text-muted">{t('bo.settings.densityHint')}</p>
      </Card>

      <Card className="space-y-4 p-card">
        <CardHeader title={t('bo.settings.languageTitle')} description={t('bo.settings.languageDescription')} />
        <LocaleSwitcher />
      </Card>

      <Card className="space-y-4 p-card">
        <CardHeader title={t('bo.settings.dispatchTitle')} description={t('bo.settings.dispatchDescription')} />
        <Switch
          checked={autoDispatch}
          onCheckedChange={(v) => toggleDispatch.mutate({ auto: v })}
          label={t('bo.settings.dispatchAutoLabel')}
          description={t('bo.settings.dispatchAutoDescription')}
        />
        <p className="text-small text-muted">{t('bo.settings.dispatchManualNote')}</p>
      </Card>

      <Card className="space-y-4 p-card">
        <CardHeader title={t('bo.settings.aiTitle')} description={t('bo.settings.aiDescription')} />
        {AI_FEATURES.map((f) => (
          <Switch
            key={f.key}
            checked={ai?.[f.key] ?? true}
            onCheckedChange={(v) => toggleAi.mutate({ [f.key]: v })}
            label={t(f.label)}
            description={t(f.description)}
          />
        ))}
      </Card>

      <Card className="space-y-4 p-card">
        <CardHeader title={t('bo.settings.notifyTitle')} description={t('bo.settings.notifyDescription')} />
        <Switch
          checked={settings.notifyUnassigned}
          onCheckedChange={(v) => update({ notifyUnassigned: v })}
          label={t('bo.settings.notifyUnassignedLabel')}
          description={t('bo.settings.notifyUnassignedDescription')}
        />
        <Switch
          checked={settings.notifyAnomaly}
          onCheckedChange={(v) => update({ notifyAnomaly: v })}
          label={t('bo.settings.notifyAnomalyLabel')}
          description={t('bo.settings.notifyAnomalyDescription')}
        />
        <Switch checked={settings.notifyDaily} onCheckedChange={(v) => update({ notifyDaily: v })} label={t('bo.settings.notifyDailyLabel')} description={t('bo.settings.notifyDailyDescription')} />
      </Card>
    </div>
  )
}
