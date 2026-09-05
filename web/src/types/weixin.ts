export type WeixinAccount = {
  id: string
  label: string
  userId: string
  enabled: boolean
  connected: boolean
  currentSessionId?: string
  currentSession?: {
    id: string
    title: string
    status: 'idle' | 'queued' | 'paused' | 'running' | 'failed' | 'canceled'
    runtime: 'easyagent' | 'codex'
    progress?: string
    updatedAt: string
  }
  deliveryStatus: 'idle' | 'processing' | 'sending' | 'delivered' | 'pending'
  lastSeenAt?: string
  lastMessageAt?: string
  createdAt: string
}

export type WeixinState = {
  enabled: boolean
  accounts: WeixinAccount[]
}

export type WeixinLogin = {
  id: string
  label: string
  qrContent?: string
  status: 'wait' | 'scaned' | 'need_verifycode' | 'confirmed' | 'expired' | 'already_bound' | 'failed' | string
  message: string
  createdAt: string
  updatedAt: string
}
