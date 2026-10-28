import type { Translation } from '@loong/i18n'
import type { enDashAuth } from '../en/auth'

export const idDashAuth: Translation<typeof enDashAuth> = {
  'dash.auth.email': 'Email',
  'dash.auth.password': 'Kata sandi',
  'dash.auth.fullName': 'Nama lengkap',
  'dash.auth.phone': 'Nomor HP',
  'dash.auth.backToSignIn': 'Kembali ke masuk',

  'dash.auth.loginTitle': 'Masuk',
  'dash.auth.loginLead': 'Lanjutkan pengiriman Anda.',
  'dash.auth.forgotLink': 'Lupa sandi?',
  'dash.auth.loginSubmit': 'Masuk',
  'dash.auth.noAccount': 'Belum punya akun? <link>Daftar gratis</link>',
  'dash.auth.demo': '<b>Mencoba demo?</b> Isi otomatis dengan akun {label}.',
  'dash.auth.errPassword': 'Isi kata sandi Anda.',

  'dash.auth.registerTitle': 'Buat akun',
  'dash.auth.registerLead': 'Cukup 4 isian. Anda bisa melengkapi profil nanti.',
  'dash.auth.phoneHint': 'Dipakai driver untuk menghubungi Anda.',
  'dash.auth.passwordHint': 'Minimal 8 karakter.',
  'dash.auth.strengthShort': 'Terlalu pendek',
  'dash.auth.strengthFair': 'Cukup',
  'dash.auth.strengthGood': 'Baik',
  'dash.auth.strengthStrong': 'Kuat',
  'dash.auth.registerSubmit': 'Buat akun',
  'dash.auth.terms': 'Dengan membuat akun, Anda menyetujui <terms>Syarat Layanan</terms> dan <privacy>Kebijakan Privasi</privacy>.',
  'dash.auth.haveAccount': 'Sudah punya akun? <link>Masuk</link>',
  'dash.auth.errName': 'Isi nama Anda.',
  'dash.auth.errPasswordShort': 'Kata sandi minimal 8 karakter.',

  'dash.auth.forgotTitle': 'Lupa kata sandi?',
  'dash.auth.forgotLead': 'Masukkan email akun Anda. Kami kirim tautan untuk mengaturnya ulang.',
  'dash.auth.forgotSubmit': 'Kirim tautan',
  'dash.auth.sentTitle': 'Cek email Anda',
  'dash.auth.sentText': 'Kami mengirim tautan untuk mengatur ulang kata sandi ke <b>{email}</b>. Tautan berlaku 30 menit.',
  'dash.auth.sentDemo': 'Ini mode demo: tidak ada email yang benar-benar dikirim.',
  'dash.auth.resendIn': 'Kirim ulang dalam {left} dtk',
  'dash.auth.resend': 'Kirim ulang email',
}
