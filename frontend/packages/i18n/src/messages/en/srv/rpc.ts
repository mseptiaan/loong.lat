/** Galat transport dari klien/host RPC (rpc/client.ts, rpc/host.ts). */
export const enSrvRpc = {
  'srv.rpc.unreachable': 'The mock backend ({origin}) could not be loaded. Check that the address opens and the dev server is running.',
  'srv.rpc.serverUnreachable': 'Can’t reach the server ({origin}). Check your internet connection and try again.',
  'srv.rpc.timeout': 'The mock backend is not responding. Try again.',
  'srv.rpc.closed': 'The connection to the mock backend was closed.',
  'srv.rpc.internal': 'Something went wrong in the mock backend: {detail}',
  'srv.rpc.unknownMethod': 'Unknown method: {method}',
  'srv.rpc.notMethod': 'Not a method: {method}',
  'srv.rpc.badRequest': 'The request could not be read. Check the details and try again.',
  'srv.rpc.tooManyRequests': 'Too many attempts. Try again in {seconds} seconds.',
  'srv.rpc.idempotencyKeyRequired': 'This action needs an Idempotency-Key header.',
  'srv.rpc.idempotencyKeyReused': 'This Idempotency-Key was already used for a different request.',
  'srv.rpc.idempotencyInFlight': 'The same request is still being processed. Wait a moment.',
} as const
