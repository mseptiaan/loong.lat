/** drv.app.*: kerangka, masuk, NotFound, dan teks yang dipakai bersama beberapa layar. */
export const enDrvApp = {
  // kerangka (DriverLayout)
  'drv.app.titleApp': 'Driver',
  'drv.app.navLabel': 'Main',
  'drv.app.navHome': 'Home',
  'drv.app.navHistory': 'History',
  'drv.app.navEarnings': 'Earnings',
  'drv.app.navProfile': 'Profile',
  'drv.app.titleRoute': 'Route',
  'drv.app.titleStop': 'Stop',
  'drv.app.offline': "You're offline. New actions are sent once you're connected again.",

  // umum (dipakai lebih dari satu layar)
  'drv.app.backHome': 'Back to home',
  'drv.app.arrived': 'Arrived',
  'drv.app.minutesLeft': '{minutes|duration} left',
  'drv.app.nextPickup': 'Next · Pickup',
  'drv.app.nextDropoff': 'Next · Drop-off',
  'drv.app.landmark': 'Landmark: {note}',
  'drv.app.toastArrived': 'Arrival recorded. The customer has been notified.',
  'drv.app.stopsDone_one': '{done} of {count} stop completed',
  'drv.app.stopsDone_other': '{done} of {count} stops completed',

  // masuk
  'drv.app.loginDocTitle': 'Sign in',
  'drv.app.loginTitle': 'Sign in as a driver',
  'drv.app.loginIntro': 'Use the phone number and PIN from your admin.',
  'drv.app.loginPhone': 'Phone number',
  'drv.app.loginPhoneError': 'Enter a valid phone number, e.g. 0812-3456-7801.',
  'drv.app.loginPin': 'PIN',
  'drv.app.loginPinHint': '6 digits',
  'drv.app.loginPinError': 'The PIN is 6 digits.',
  'drv.app.loginSubmit': 'Sign in',
  'drv.app.loginHelp': 'Driver accounts are created by an admin. Forgot your PIN? Contact your dispatcher.',
  'drv.app.loginDemo': '<strong>Trying the demo?</strong> Fills in the {label} account automatically.',

  // halaman tidak ditemukan
  'drv.app.notFoundTitle': 'Page not found',
  'drv.app.notFoundHint': "The page you opened doesn't exist or has been moved.",
} as const
