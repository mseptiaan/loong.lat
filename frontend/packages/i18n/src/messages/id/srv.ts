import type { Translation } from '../../types'
import type { enSrv } from '../en/srv'
import { idSrvAi } from './srv/ai'
import { idSrvApi } from './srv/api'
import { idSrvEngine } from './srv/engine'
import { idSrvOps } from './srv/ops'
import { idSrvProof } from './srv/proof'
import { idSrvRpc } from './srv/rpc'

export const idSrv: Translation<typeof enSrv> = { ...idSrvRpc, ...idSrvEngine, ...idSrvOps, ...idSrvProof, ...idSrvApi, ...idSrvAi }
