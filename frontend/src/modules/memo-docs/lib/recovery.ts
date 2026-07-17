import type { MemoEditorSnapshot } from '../types'

const DATABASE_NAME = 'mongojson-memo-recovery'
const STORE_NAME = 'documents'

export type MemoRecoverySnapshot = MemoEditorSnapshot & {
  documentId: string
  revision: number
  title: string
  savedAt: string
}

function openRecoveryDatabase() {
  return new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE_NAME)) {
        request.result.createObjectStore(STORE_NAME, { keyPath: 'documentId' })
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

export async function loadMemoRecovery(documentId: string): Promise<MemoRecoverySnapshot | null> {
  if (typeof indexedDB === 'undefined') return null
  const database = await openRecoveryDatabase()
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, 'readonly')
    const request = transaction.objectStore(STORE_NAME).get(documentId)
    request.onsuccess = () => resolve((request.result as MemoRecoverySnapshot | undefined) ?? null)
    request.onerror = () => reject(request.error)
    transaction.oncomplete = () => database.close()
  })
}

export async function saveMemoRecovery(snapshot: MemoRecoverySnapshot) {
  if (typeof indexedDB === 'undefined') return
  const database = await openRecoveryDatabase()
  await new Promise<void>((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    transaction.objectStore(STORE_NAME).put(snapshot)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error)
  })
  database.close()
}

export async function clearMemoRecovery(documentId: string) {
  if (typeof indexedDB === 'undefined') return
  const database = await openRecoveryDatabase()
  await new Promise<void>((resolve, reject) => {
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    transaction.objectStore(STORE_NAME).delete(documentId)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error)
  })
  database.close()
}
