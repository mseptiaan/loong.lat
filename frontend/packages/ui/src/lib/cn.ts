import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// Ukuran teks kustom kita harus dikenali sebagai `font-size`, kalau tidak
// `text-body` dan `text-fg` dianggap bentrok (sama-sama dikira warna).
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      'font-size': [{ text: ['caption', 'small', 'body', 'title', 'heading', 'display', 'metric', 'metric-sm'] }],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
