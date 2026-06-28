import { get, post, put, del } from './client'
import type {
  BiliCookieAccount,
  QRCodeSession,
  QRCodePollResult,
  QRCodeSaveRequest,
  QRCodeSaveResponse,
} from './types'

export function createQRCodeSession(): Promise<QRCodeSession> {
  return post('/api/bili/login/qrcode')
}

export function pollQRCodeSession(sessionId: string): Promise<QRCodePollResult> {
  return get(`/api/bili/login/qrcode/${encodeURIComponent(sessionId)}`)
}

export function saveQRCodeSession(
  sessionId: string,
  channelId: string,
  usage: QRCodeSaveRequest['usage'],
): Promise<QRCodeSaveResponse> {
  return post(`/api/bili/login/qrcode/${encodeURIComponent(sessionId)}/save`, {
    channel_id: channelId,
    usage,
  })
}

export function cancelQRCodeSession(sessionId: string): Promise<void> {
  return del(`/api/bili/login/qrcode/${encodeURIComponent(sessionId)}`)
}

export function listBiliAccounts(): Promise<BiliCookieAccount[]> {
  return get('/api/bili/accounts')
}

export function saveQRCodeToAccount(sessionId: string, nickname?: string): Promise<BiliCookieAccount> {
  return post(`/api/bili/login/qrcode/${encodeURIComponent(sessionId)}/save-account`, { nickname: nickname || '' })
}

export function updateBiliAccount(id: number, data: Partial<Pick<BiliCookieAccount, 'nickname' | 'is_default_download' | 'is_default_publish'>>): Promise<BiliCookieAccount> {
  return put(`/api/bili/accounts/${id}`, data)
}

export function deleteBiliAccount(id: number): Promise<void> {
  return del(`/api/bili/accounts/${id}`)
}
