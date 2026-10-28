/**
 * srv.proof.*: ringkasan AI tiruan atas foto bukti antar (disimpan di `RouteStop.proof.analysis`, dirender di layar).
 * Hanya menjelaskan isi foto; tidak mengklaim lokasi atau identitas.
 */
export const enSrvProof = {
  'srv.proof.summary.seated':
    'An older man in a white sleeveless shirt sits on a low bench in front of a concrete-block wall, holding a small dark rectangular item in both hands and looking at it. He is the only person in the frame.',
  'srv.proof.summary.holding': 'A person is holding up a boxed parcel in front of them, facing the camera. The whole box is visible in the frame.',
  'srv.proof.summary.atDoor': 'A boxed parcel sits on the floor next to a closed door. No person is in the frame.',
  'srv.proof.recipient.visible': 'One person is clearly visible and facing the camera.',
  'srv.proof.recipient.none': 'No person is visible, so the handover to the recipient cannot be confirmed.',
  'srv.proof.parcel.held': 'A boxed parcel is held up and fully in frame.',
  'srv.proof.parcel.atDoor': 'A boxed parcel is clearly visible next to the door.',
  'srv.proof.parcel.unclearItem': 'He holds a small dark item that could be the parcel or a handset; no label is readable.',
  'srv.proof.quality.good': 'Sharp and well lit; nothing is cut off.',
} as const
