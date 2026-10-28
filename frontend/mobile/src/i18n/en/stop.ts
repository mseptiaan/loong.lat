/** drv.stop.*: halaman stop dan SwipeToConfirm. */
export const enDrvStop = {
  // SwipeToConfirm
  'drv.stop.swipeBusy': 'Processing…',
  'drv.stop.swipeConfirmHint': 'Tap once more to confirm',
  'drv.stop.swipeAria': '{label}. Swipe right, tap twice, or press Enter.',

  // kepala dan peringatan
  'drv.stop.back': 'Back to route',
  'drv.stop.heading': 'Stop {seq} of {total}',
  'drv.stop.notStartedNotice': 'Start the route before working on stops.',
  'drv.stop.notActiveNotice': "This stop isn't active yet. Finish stop {seq} first.",
  'drv.stop.notFoundTitle': 'Stop not found',
  'drv.stop.notFoundHint': 'This job may have been completed or reassigned.',

  // lokasi dan kontak
  'drv.stop.scheduledPickup': 'Pickup · scheduled {time|time}',
  'drv.stop.scheduledDropoff': 'Drop-off · scheduled {time|time}',
  'drv.stop.openNavigation': 'Open navigation',
  'drv.stop.senderContact': 'Sender contact',
  'drv.stop.recipientContact': 'Recipient contact',
  'drv.stop.call': 'Call',
  'drv.stop.whatsappMessage': "Hi {contact}, I'm the loong.lat driver for {code}. I'm close to {place}.",

  // barang
  'drv.stop.itemsPickup': 'Items to pick up',
  'drv.stop.itemsDropoff': 'Items to drop off',
  'drv.stop.itemsCheck': 'Check the quantity and condition before taking them.',
  'drv.stop.fragile': 'Fragile, handle with care',

  // bukti
  'drv.stop.photoPreview': 'Proof photo preview',
  'drv.stop.photoRemove': 'Remove photo',
  'drv.stop.pickupPhotoTitle': 'Item photo',
  'drv.stop.pickupPhotoHint': 'Optional. A photo of the items at pickup helps if there is a damage claim.',
  'drv.stop.pickupPhotoButton': 'Take item photo',
  'drv.stop.proofTitle': 'Proof of delivery',
  'drv.stop.proofPhotoLabel': 'Delivery proof photo',
  'drv.stop.proofPhotoHint': 'A photo of the items at the destination, or with the recipient.',
  'drv.stop.proofPhotoButton': 'Take proof photo',
  'drv.stop.otpLabel': 'Recipient OTP code',
  'drv.stop.otpHint': 'Ask the recipient for the 4-digit code.',
  'drv.stop.demoFill': '<strong>Demo mode:</strong> recipient code {code}. Tap to fill it in.',
  'drv.stop.useOtp': 'Use the OTP code only',
  'drv.stop.usePhoto': 'Recipient has no code? Use a proof photo',
  'drv.stop.photoReadError': 'This photo could not be read. Take it again.',
  'drv.stop.demoSamplePhoto': '<strong>Demo mode:</strong> use the sample recipient photo.',
  'drv.stop.proofNeedPhoto': 'Take a delivery proof photo first.',
  'drv.stop.proofNeedOtp': "Enter the recipient's 4-digit OTP code.",

  // stop sudah ditutup
  'drv.stop.closedDoneTitle': 'Stop completed',
  'drv.stop.closedIssueTitle': 'Stop marked as a problem',
  'drv.stop.closedDoneAt': 'Completed at {time}.',
  'drv.stop.closedIssueFallback': 'There was a problem at the location.',

  // laporan masalah
  'drv.stop.reportIssue': 'Report a problem at this stop',
  'drv.stop.issueTitle': 'Report a problem',
  'drv.stop.issueHint': 'This stop is marked as a problem and the route moves on to the next stop. The admin and the customer are notified.',
  'drv.stop.issueSend': 'Send report',
  'drv.stop.issueKind': 'Problem type',
  'drv.stop.issueKindError': 'Choose the type of problem.',
  'drv.stop.issueNote': 'Note',
  'drv.stop.issueNoteError': 'Briefly describe the problem.',
  'drv.stop.issueNotePlaceholder': 'e.g. Called 3 times, no answer.',

  // setelah stop ditutup (toast)
  'drv.stop.donePickup': 'Pickup completed.',
  'drv.stop.doneDropoff': 'Drop-off completed.',
  'drv.stop.issueSent': 'Report sent. The admin and the customer have been notified.',
  'drv.stop.doneNext': '{message} Continue to {name}.',

  // aksi bawah
  'drv.stop.swipeArrive': 'Swipe to arrive at the location',
  'drv.stop.swipePickedUp': 'Swipe when the items are picked up',
  'drv.stop.swipeReceived': 'Swipe when the items are received',
} as const
