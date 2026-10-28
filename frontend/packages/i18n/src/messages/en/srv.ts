import { enSrvAi } from './srv/ai'
import { enSrvApi } from './srv/api'
import { enSrvEngine } from './srv/engine'
import { enSrvOps } from './srv/ops'
import { enSrvProof } from './srv/proof'
import { enSrvRpc } from './srv/rpc'

/** srv.*: teks netral-bahasa dari backend tiruan. Dipecah per pemilik file (rpc, engine, ops, proof, api, ai). */
export const enSrv = { ...enSrvRpc, ...enSrvEngine, ...enSrvOps, ...enSrvProof, ...enSrvApi, ...enSrvAi } as const
