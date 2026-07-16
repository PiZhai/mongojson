import type { StewardSignedApprovalProof } from '../../../types/tooling'

export type WebAuthnApprovalAuthority = {
  name: string
  algorithm: string
  key_id: string
  credential_id?: string
  rp_id?: string
  allowed_origins?: string[]
}

export type WebAuthnApprovalRequest = {
  authority: WebAuthnApprovalAuthority
  subject: string
  planHash: string
  capability: string
  controlGeneration: number
  grantedBy: string
  reason: string
  origin?: string
  now?: Date
  credentials?: Pick<CredentialsContainer, 'get'>
  crypto?: Crypto
}

export type WebAuthnPolicyAuthority = {
  name: string
  algorithm: 'webauthn-es256'
  public_key: string
  credential_id: string
  rp_id: string
  allowed_origins: string[]
  enabled: true
}

type RegistrationOptions = {
  name: string
  rpID?: string
  origin?: string
  userName?: string
  credentials?: Pick<CredentialsContainer, 'create'>
  crypto?: Crypto
}

const APPROVAL_VERSION = 'steward-approval-proof/v1'
const APPROVAL_TTL_MS = 5 * 60 * 1000

function browserCrypto() {
  if (!globalThis.crypto) throw new Error('当前环境不支持 Web Crypto')
  return globalThis.crypto
}

function browserCredentials() {
  if (!globalThis.navigator?.credentials) throw new Error('当前浏览器不支持 WebAuthn')
  return globalThis.navigator.credentials
}

export function encodeBase64URL(input: ArrayBuffer | Uint8Array) {
  const bytes = input instanceof Uint8Array ? input : new Uint8Array(input)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
}

export function decodeBase64URL(value: string) {
  if (!value || !/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('credential_id 不是规范 base64url')
  const padded = value.replaceAll('-', '+').replaceAll('_', '/') + '='.repeat((4 - value.length % 4) % 4)
  const binary = atob(padded)
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  if (encodeBase64URL(bytes) !== value) throw new Error('credential_id 不是规范 base64url')
  return bytes
}

function encodeBase64(input: ArrayBuffer) {
  const bytes = new Uint8Array(input)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

export function authorityMatchesOrigin(authority: WebAuthnApprovalAuthority, origin: string) {
  if (authority.algorithm !== 'webauthn-es256' || !authority.credential_id || !authority.rp_id) return false
  let canonicalOrigin: string
  try {
    canonicalOrigin = new URL(origin).origin
  } catch {
    return false
  }
  return authority.allowed_origins?.includes(canonicalOrigin) ?? false
}

export async function approvalProofChallenge(claims: StewardSignedApprovalProof['claims'], cryptoProvider = browserCrypto()) {
  const challengeClaims = {
    version: claims.version,
    proof_id: claims.proof_id,
    subject: claims.subject,
    plan_hash: claims.plan_hash,
    capability: claims.capability,
    control_generation: claims.control_generation,
    granted_by: claims.granted_by,
    reason: claims.reason,
    issued_at_unix_ms: new Date(claims.issued_at).getTime(),
    expires_at_unix_ms: new Date(claims.expires_at).getTime(),
  }
  if (!Number.isFinite(challengeClaims.issued_at_unix_ms) || !Number.isFinite(challengeClaims.expires_at_unix_ms)) {
    throw new Error('审批时间无效')
  }
  const payload = new TextEncoder().encode(JSON.stringify(challengeClaims))
  return new Uint8Array(await cryptoProvider.subtle.digest('SHA-256', payload))
}

export async function issueWebAuthnApprovalProof(request: WebAuthnApprovalRequest): Promise<StewardSignedApprovalProof> {
  const origin = new URL(request.origin ?? globalThis.location?.origin ?? '').origin
  if (!authorityMatchesOrigin(request.authority, origin)) throw new Error('当前 origin 不在该 WebAuthn 审批机构的允许列表中')
  const cryptoProvider = request.crypto ?? browserCrypto()
  const proofID = cryptoProvider.getRandomValues(new Uint8Array(32))
  const issuedAt = request.now ? new Date(request.now) : new Date()
  const claims: StewardSignedApprovalProof['claims'] = {
    version: APPROVAL_VERSION,
    proof_id: Array.from(proofID, (byte) => byte.toString(16).padStart(2, '0')).join(''),
    subject: request.subject.trim(),
    plan_hash: request.planHash.trim().toLowerCase(),
    capability: request.capability.trim().toLowerCase(),
    control_generation: request.controlGeneration,
    granted_by: request.grantedBy.trim(),
    reason: request.reason.trim(),
    issued_at: issuedAt.toISOString(),
    expires_at: new Date(issuedAt.getTime() + APPROVAL_TTL_MS).toISOString(),
  }
  const challenge = await approvalProofChallenge(claims, cryptoProvider)
  const expectedCredentialID = request.authority.credential_id as string
  const credential = await (request.credentials ?? browserCredentials()).get({
    publicKey: {
      challenge,
      rpId: request.authority.rp_id,
      allowCredentials: [{ id: decodeBase64URL(expectedCredentialID), type: 'public-key' }],
      timeout: 120_000,
      userVerification: 'required',
    },
  })
  if (!credential) throw new Error('WebAuthn 审批已取消')
  const publicKeyCredential = credential as PublicKeyCredential
  if (credential.type !== 'public-key' || encodeBase64URL(publicKeyCredential.rawId) !== expectedCredentialID || credential.id !== expectedCredentialID) {
    throw new Error('WebAuthn 返回了不匹配的 credential')
  }
  const response = publicKeyCredential.response as AuthenticatorAssertionResponse
  if (!response.clientDataJSON || !response.authenticatorData || !response.signature) {
    throw new Error('WebAuthn assertion 响应不完整')
  }
  return {
    claims,
    key_id: request.authority.key_id,
    algorithm: 'webauthn-es256',
    webauthn: {
      credential_id: expectedCredentialID,
      client_data_json: encodeBase64URL(response.clientDataJSON),
      authenticator_data: encodeBase64URL(response.authenticatorData),
      signature: encodeBase64URL(response.signature),
    },
  }
}

export async function createWebAuthnPolicyAuthority(options: RegistrationOptions): Promise<WebAuthnPolicyAuthority> {
  const origin = new URL(options.origin ?? globalThis.location?.origin ?? '').origin
  const rpID = (options.rpID ?? new URL(origin).hostname).toLowerCase()
  if (/^(?:\d{1,3}\.){3}\d{1,3}$/.test(rpID) || rpID.includes(':')) {
    throw new Error('WebAuthn Broker policy 要求 DNS RP ID；本机请使用 localhost 打开工作台，而不是 IP 地址')
  }
  const cryptoProvider = options.crypto ?? browserCrypto()
  const challenge = cryptoProvider.getRandomValues(new Uint8Array(32))
  const userID = cryptoProvider.getRandomValues(new Uint8Array(32))
  const credential = await (options.credentials ?? browserCredentials()).create({
    publicKey: {
      rp: { id: rpID, name: 'MongoJSON Steward Approval' },
      user: { id: userID, name: options.userName ?? 'local-operator', displayName: options.name },
      challenge,
      pubKeyCredParams: [{ alg: -7, type: 'public-key' }],
      timeout: 120_000,
      attestation: 'none',
      authenticatorSelection: { residentKey: 'preferred', userVerification: 'required' },
    },
  })
  if (!credential) throw new Error('WebAuthn 登记已取消')
  if (credential.type !== 'public-key') throw new Error('WebAuthn 返回了不支持的 credential 类型')
  const publicKeyCredential = credential as PublicKeyCredential
  const response = publicKeyCredential.response as AuthenticatorAttestationResponse & {
    getPublicKey?: () => ArrayBuffer | null
    getPublicKeyAlgorithm?: () => number
  }
  if (typeof response.getPublicKey !== 'function' || typeof response.getPublicKeyAlgorithm !== 'function') {
    throw new Error('浏览器无法导出 WebAuthn 公钥；需要 getPublicKey() 支持')
  }
  if (response.getPublicKeyAlgorithm() !== -7) throw new Error('WebAuthn credential 不是 ES256')
  const publicKey = response.getPublicKey()
  if (!publicKey) throw new Error('浏览器未返回 WebAuthn 公钥')
  const credentialID = encodeBase64URL(publicKeyCredential.rawId)
  if (credential.id !== credentialID) throw new Error('WebAuthn 登记返回了不匹配的 credential')
  return {
    name: options.name.trim(),
    algorithm: 'webauthn-es256',
    public_key: encodeBase64(publicKey),
    credential_id: credentialID,
    rp_id: rpID,
    allowed_origins: [origin],
    enabled: true,
  }
}
