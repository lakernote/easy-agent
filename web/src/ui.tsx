export function TrashIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m3 0-1 13H7L6 7m4 4v5m4-5v5" /></svg> }
export function MoreIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="5" cy="12" r="1" /><circle cx="12" cy="12" r="1" /><circle cx="19" cy="12" r="1" /></svg> }
export function FolderIcon({ add = false }: { add?: boolean }) { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 7.5h7l2-2h9v13H3v-11Z" />{add && <path d="M12 10v6m-3-3h6" />}</svg> }
export function ChevronIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m7 9 5 5 5-5" /></svg> }
export function Logo() { return <img src="/logo.svg" alt="" aria-hidden="true" /> }
export function Icon({ name }: { name: string }) { const paths: Record<string, string> = { chat: 'M4 5h16v11H8l-4 4V5Z', skill: 'M6 3h12v18H6zM9 8h6M9 12h6', plug: 'M9 3v6m6-6v6M7 9h10v3a5 5 0 0 1-10 0V9Zm5 8v4', settings: 'M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Zm0-5v2m0 13v2M3.5 12h2m13 0h2M5.9 5.9l1.4 1.4m9.4 9.4 1.4 1.4m0-12.2-1.4 1.4M7.3 16.7l-1.4 1.4' }; return <svg viewBox="0 0 24 24" aria-hidden="true"><path d={paths[name]} /></svg> }
export function AttachIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m8.5 12.5 6.8-6.8a3.5 3.5 0 0 1 5 5l-9.2 9.2a5 5 0 0 1-7.1-7.1l9-9m-6.2 11.4 8.5-8.5" /></svg> }
export function SendIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5m0 0-6 6m6-6 6 6" /></svg> }
export function CloseIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg> }
export function FileIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l4 4v14H6V3Zm8 0v5h5M9 13h6m-6 4h5" /></svg> }
export function Avatar() { return <div className="avatar"><Logo /></div> }
