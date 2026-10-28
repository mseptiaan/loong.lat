import { renderMsg } from '@loong/i18n'
import { getPlace } from '@loong/gazetteer'
import { computeOptionSet, makeItem, makeVehicles } from '@loong/mock-api'
import type { Shipment, Waypoint } from '@loong/domain'
import {
  Avatar,
  Button,
  Card,
  CardHeader,
  Checkbox,
  Chip,
  ConfirmDialog,
  Dialog,
  EmptyState,
  ErrorState,
  Field,
  FareBreakdown,
  IconButton,
  Input,
  Kbd,
  Logo,
  Notice,
  NumberStepper,
  PageHeader,
  PlanStatusLabel,
  RouteMap,
  RouteOptionCard,
  Section,
  Segmented,
  Select,
  Sheet,
  Skeleton,
  Spinner,
  Stat,
  StopList,
  Switch,
  TBody,
  TD,
  TH,
  THead,
  TR,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  TableWrap,
  Textarea,
  toast,
} from '@loong/ui'
import { Inbox, Plus, Search, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

function wp(id: string): Waypoint {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const ship = (id: string, a: string, b: string, items: string[]): Shipment => ({ id, pickup: wp(a), dropoff: wp(b), items: items.map((k) => makeItem(k)) })

/** Lembar kit komponen untuk QA desain (dev saja): /_kit */
export function KitPage() {
  const [dialog, setDialog] = useState(false)
  const [sheet, setSheet] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [seg, setSeg] = useState<'sekarang' | 'nanti'>('sekarang')
  const [sel, setSel] = useState('')
  const [count, setCount] = useState(2)
  const [sw, setSw] = useState(true)
  const [chk, setChk] = useState(false)
  const [picked, setPicked] = useState(0)
  const [traceKey, setTraceKey] = useState('a')

  const set = useMemo(
    () =>
      computeOptionSet({
        planId: 'kit',
        shipments: [ship('a', 'kemang-village', 'plaza-senayan', ['kue']), ship('b', 'kemang-raya', 'senayan-city', ['sepatu']), ship('c', 'blok-m-square', 'pacific-place', ['dokumen']), ship('d', 'kemang-village', 'kota-kasablanka', ['laptop'])],
        schedule: { mode: 'sekarang' },
        priority: { preset: 'seimbang' },
        vehicles: makeVehicles(),
        nowMs: Date.parse('2026-10-20T03:00:00Z'),
      }),
    [],
  )
  const option = set.options[picked] ?? set.options[0]!

  return (
    <main className="mx-auto max-w-5xl space-y-10 p-6 pb-24">
      <PageHeader title="Kit komponen" description="Lembar QA desain: semua komponen bersama dalam keadaan nyata." actions={<Logo />} />

      <Section title="Tombol">
        <Card className="flex flex-wrap items-center gap-3 p-card">
          <Button variant="primary">Buat pengiriman</Button>
          <Button variant="secondary">Simpan draft</Button>
          <Button variant="ghost">Batal</Button>
          <Button variant="tint">Salin kode</Button>
          <Button variant="danger">Batalkan pesanan</Button>
          <Button variant="link">Lihat rincian</Button>
          <Button variant="primary" loading>
            Menghitung
          </Button>
          <Button variant="primary" iconRight={<Plus className="size-4" />}>
            Lanjut
          </Button>
          <Button size="sm">Kecil</Button>
          <Button size="lg" variant="primary">
            Besar
          </Button>
          <IconButton label="Hapus" icon={<Trash2 className="size-4" />} />
          <IconButton label="Cari" icon={<Search className="size-4" />} variant="secondary" />
        </Card>
      </Section>

      <Section title="Formulir">
        <Card className="grid gap-5 p-card sm:grid-cols-2">
          <Field label="Nama penerima" hint="Sesuai yang tertera di pintu atau bel.">
            <Input placeholder="mis. Ibu Ratna" />
          </Field>
          <Field label="Nomor HP" error="Nomor HP belum lengkap. Isi minimal 9 digit.">
            <Input defaultValue="0812" inputMode="tel" />
          </Field>
          <Field label="Cari lokasi" optional>
            <Input leading={<Search className="size-4" />} placeholder="Mal, stasiun, kampus…" />
          </Field>
          <Field label="Kota">
            <Select
              value={sel}
              onValueChange={setSel}
              options={['Jakarta Pusat', 'Jakarta Selatan', 'Jakarta Barat', 'Jakarta Timur', 'Jakarta Utara', 'Tangerang', 'Depok', 'Bekasi'].map((c) => ({ value: c, label: c }))}
            />
          </Field>
          <Field label="Kapan dikirim?">
            <Segmented aria-label="Jadwal" value={seg} onValueChange={setSeg} options={[{ value: 'sekarang', label: 'Sekarang' }, { value: 'nanti', label: 'Jadwalkan' }]} />
          </Field>
          <Field label="Jumlah barang">
            <NumberStepper label="jumlah barang" value={count} onChange={setCount} />
          </Field>
          <Field label="Catatan untuk driver" className="sm:col-span-2" optional>
            <Textarea placeholder="Titip ke satpam bila tidak ada orang" />
          </Field>
          <Checkbox checked={chk} onCheckedChange={setChk} label="Barang mudah pecah" description="Driver akan membawanya dengan ekstra hati-hati." />
          <Switch checked={sw} onCheckedChange={setSw} label="Notifikasi driver tiba" />
        </Card>
      </Section>

      <Section title="Keadaan: memuat, kosong, galat, pemberitahuan">
        <div className="grid gap-4 md:grid-cols-2">
          <Card className="space-y-3 p-card">
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-4/5" />
            <Spinner label="Menghitung rute terbaik…" />
          </Card>
          <Card>
            <EmptyState icon={<Inbox />} title="Belum ada pengiriman" description="Buat pengiriman pertama Anda. Hanya butuh sekitar 3 menit." action={<Button variant="primary">Buat pengiriman</Button>} compact />
          </Card>
          <Card>
            <ErrorState message="Layanan sedang bermasalah. Coba lagi sebentar lagi." onRetry={() => toast.success('Mencoba lagi…')} compact />
          </Card>
          <Card className="space-y-3 p-card">
            <Notice tone="info" title="Opsi dihitung ulang">Barang berubah, jadi opsi sebelumnya tidak berlaku.</Notice>
            <Notice tone="warning">Jam sibuk: waktu tempuh bisa bertambah sekitar 35%.</Notice>
            <Notice tone="danger" title="Pembayaran gagal">Saldo tidak cukup.</Notice>
            <Notice tone="success">Pesanan dikonfirmasi.</Notice>
          </Card>
        </div>
      </Section>

      <Section title="Status, metrik, tab">
        <div className="grid gap-4 md:grid-cols-4">
          <Stat emphasis="primary" label="Hemat bulan ini" value="Rp 128.500" hint="dari 9 pengiriman" spark={[3, 5, 4, 7, 6, 9, 8, 12]} className="md:col-span-2" />
          <Stat label="Pengiriman aktif" value="2" />
          <Stat label="Rata-rata hemat" value="21%" />
        </div>
        <Card className="flex flex-wrap items-center gap-x-6 gap-y-2 p-card">
          {(['draft', 'opsi_siap', 'menunggu_driver', 'ditugaskan', 'berjalan', 'selesai', 'dibatalkan'] as const).map((s) => (
            <PlanStatusLabel key={s} status={s} />
          ))}
          <Chip>Disarankan</Chip>
          <Avatar name="Dina Pramesti" />
          <span className="text-small text-muted">
            Cari cepat <Kbd>⌘K</Kbd>
          </span>
        </Card>
        <Tabs defaultValue="semua">
          <TabsList>
            <TabsTrigger value="semua">Semua</TabsTrigger>
            <TabsTrigger value="aktif">Aktif</TabsTrigger>
            <TabsTrigger value="selesai">Selesai</TabsTrigger>
          </TabsList>
          <TabsContent value="semua" className="pt-3 text-body text-muted">Semua pengiriman Anda.</TabsContent>
        </Tabs>
      </Section>

      <Section title="Tabel padat">
        <Card className="overflow-hidden">
          <TableWrap className="max-h-64">
            <THead>
              <TR>
                <TH>Plan</TH>
                <TH>Rute</TH>
                <TH>Status</TH>
                <TH numeric>Biaya</TH>
              </TR>
            </THead>
            <TBody>
              {[
                ['PL-2041', 'Kemang Village → Plaza Senayan', 'berjalan', 'Rp 46.500'],
                ['PL-2040', 'Blok M Square → Pacific Place +2', 'menunggu_driver', 'Rp 58.000'],
                ['PL-2039', 'Stasiun Depok → Margo City', 'selesai', 'Rp 27.500'],
              ].map(([code, route, status, cost]) => (
                <TR key={code} interactive tabIndex={0}>
                  <TD className="font-medium">{code}</TD>
                  <TD className="text-muted">{route}</TD>
                  <TD><PlanStatusLabel status={status as 'berjalan'} /></TD>
                  <TD numeric>{cost}</TD>
                </TR>
              ))}
            </TBody>
          </TableWrap>
        </Card>
      </Section>

      <Section title="Dialog, panel samping, konfirmasi">
        <Card className="flex flex-wrap gap-3 p-card">
          <Button onClick={() => setDialog(true)}>Buka dialog</Button>
          <Button onClick={() => setSheet(true)}>Buka panel samping</Button>
          <Button variant="secondary" onClick={() => setConfirm(true)}>Hapus kendaraan…</Button>
          <Button onClick={() => toast.success('Draft tersimpan')}>Toast</Button>
        </Card>
        <Dialog open={dialog} onOpenChange={setDialog} title="Batalkan pesanan?" description="Driver yang sudah ditugaskan akan diberi tahu." footer={<><Button variant="secondary" onClick={() => setDialog(false)}>Kembali</Button><Button variant="danger" onClick={() => setDialog(false)}>Batalkan pesanan</Button></>} />
        <Sheet open={sheet} onOpenChange={setSheet} title="Tambah kendaraan" description="Kapasitas menentukan barang yang boleh masuk." footer={<><Button variant="secondary" onClick={() => setSheet(false)}>Batal</Button><Button variant="primary" onClick={() => setSheet(false)}>Simpan kendaraan</Button></>}>
          <Field label="Plat nomor" hint="Format: B 1234 ABC"><Input /></Field>
        </Sheet>
        <ConfirmDialog open={confirm} onOpenChange={setConfirm} title="Hapus kendaraan B 3412 KJR?" description="Tindakan ini tidak bisa dibatalkan." confirmLabel="Hapus kendaraan" tone="danger" typeToConfirm="B 3412 KJR" onConfirm={() => setConfirm(false)} />
      </Section>

      <Section title="Rute: peta, opsi, daftar stop, rincian harga" description="Opsi dihitung mesin tiruan; ganti opsi untuk melihat rute tergambar ulang.">
        <div className="grid gap-4 lg:grid-cols-[22rem_1fr]">
          <div className="space-y-3" role="radiogroup" aria-label="Opsi rute">
            {set.options.map((o, i) => (
              <RouteOptionCard
                key={o.id}
                option={o}
                baseline={set.baseline}
                selected={picked === i}
                name="kit-opsi"
                onSelect={() => {
                  setPicked(i)
                  setTraceKey(o.id)
                }}
              />
            ))}
          </div>
          <div className="space-y-4">
            <div className="h-96 overflow-hidden rounded-card border border-line">
              <RouteMap
                routes={set.options.flatMap((o, i) => o.routes.map((r) => ({ id: r.id, route: r, emphasis: i === picked ? ('primary' as const) : ('muted' as const) })))}
                onSelectRoute={(id) => {
                  const idx = set.options.findIndex((o) => o.routes.some((r) => r.id === id))
                  if (idx >= 0) {
                    setPicked(idx)
                    setTraceKey(set.options[idx]!.id)
                  }
                }}
                traceKey={traceKey}
                fitKey={set.inputHash}
              />
            </div>
            <Card className="p-card">
              <CardHeader title="Urutan stop" description={option.reasons[0] ? renderMsg(option.reasons[0]) : undefined} />
              <StopList stops={option.routes[0]!.stops} group className="mt-4" />
            </Card>
            <Card className="p-card">
              <CardHeader title="Rincian biaya" />
              <FareBreakdown routes={option.routes} className="mt-3" />
            </Card>
          </div>
        </div>
      </Section>
    </main>
  )
}
