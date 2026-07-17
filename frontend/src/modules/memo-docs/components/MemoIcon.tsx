export type MemoIconName = 'archive' | 'close' | 'document' | 'notes' | 'outline' | 'plus'

const paths: Record<MemoIconName, string> = {
  archive: 'M4 7.5h16v12H4zM4 7.5l2-3h5l2 3M9 11.5h6',
  close: 'M6 6l12 12M18 6 6 18',
  document: 'M6 3.5h8l4 4v13H6zM14 3.5v4h4M9 12h6M9 15.5h6',
  notes: 'M5 5h14v14H5zM8 9h8M8 12h6M8 15h7',
  outline: 'M5 6h14M5 12h14M5 18h14',
  plus: 'M12 5v14M5 12h14',
}

export function MemoIcon({ name }: { name: MemoIconName }) {
  return (
    <svg aria-hidden="true" className="memo-command-icon" fill="none" viewBox="0 0 24 24">
      <path d={paths[name]} />
    </svg>
  )
}
