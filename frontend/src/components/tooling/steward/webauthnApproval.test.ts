import { describe, expect, it, vi } from 'vitest'

import type { StewardSignedApprovalProof } from '../../../types/tooling'
import {
  approvalProofChallenge,
  encodeBase64URL,
  issueWebAuthnApprovalProof,
  type WebAuthnApprovalAuthority,
} from './webauthnApproval'

const credentialID = encodeBase64URL(Uint8Array.from([1, 2, 3, 4]))
const authority: WebAuthnApprovalAuthority = {
  name: 'hello',
  algorithm: 'webauthn-es256',
  key_id: 'approval-key',
  credential_id: credentialID,
  rp_id: 'localhost',
  allowed_origins: ['https://localhost'],
}

describe('WebAuthn approval', () => {
  it('matches the Go ApprovalProofChallenge fixed vector', async () => {
    const claims: StewardSignedApprovalProof['claims'] = {
      version: 'steward-approval-proof/v1',
      proof_id: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
      subject: 'runtime:run-1',
      plan_hash: 'a'.repeat(64),
      capability: 'tool:whoami',
      control_generation: 7,
      granted_by: 'local-user',
      reason: 'approve once',
      issued_at: '2025-01-01T00:00:00.123Z',
      expires_at: '2025-01-01T00:05:00.123Z',
    }

    const challenge = await approvalProofChallenge(claims)

    expect(Array.from(challenge, (byte) => byte.toString(16).padStart(2, '0')).join('')).toBe(
      'd642a460b9decde3a070488517a4944ac4efb98342d06e47c197ee79783db3e2',
    )
  })

  it('rejects an authority that does not allow the current origin', async () => {
    const get = vi.fn()

    await expect(issueWebAuthnApprovalProof({
      authority,
      subject: 'runtime:run-1',
      planHash: 'a'.repeat(64),
      capability: 'tool:whoami',
      controlGeneration: 7,
      grantedBy: 'local-user',
      reason: 'approve once',
      origin: 'https://evil.example',
      credentials: { get },
    })).rejects.toThrow('origin')
    expect(get).not.toHaveBeenCalled()
  })

  it('rejects a credential response that does not match policy', async () => {
    const otherID = Uint8Array.from([9, 9, 9, 9])
    const get = vi.fn(async () => ({
      id: encodeBase64URL(otherID),
      rawId: otherID.buffer,
      type: 'public-key',
      response: {
        clientDataJSON: new Uint8Array([1]).buffer,
        authenticatorData: new Uint8Array(37).buffer,
        signature: new Uint8Array([2]).buffer,
      },
    }) as unknown as Credential)

    await expect(issueWebAuthnApprovalProof({
      authority,
      subject: 'runtime:run-1',
      planHash: 'a'.repeat(64),
      capability: 'tool:whoami',
      controlGeneration: 7,
      grantedBy: 'local-user',
      reason: 'approve once',
      origin: 'https://localhost',
      credentials: { get },
    })).rejects.toThrow('不匹配的 credential')
  })
})
