// Brand marks for connected drives (Google Drive, Dropbox, OneDrive, iCloud Drive),
// drawn inline like the stroke icons. Anything else (a pasted rclone remote) gets the plain cloud.
import { useId } from 'react'
import { Icon } from './icons'

const CLOUD = 'M17.5 19H7a5 5 0 1 1 .9-9.92A6.5 6.5 0 0 1 20.5 11a4 4 0 0 1-3 8z'

export function CloudLogo({ kind, size = 16, className }: { kind?: string; size?: number; className?: string }) {
  const id = useId()
  const svg = (box: string, body: React.ReactNode) => <svg width={size} height={size} viewBox={box} className={className} aria-hidden="true" style={{ flex: 'none' }}>{body}</svg>
  switch (kind) {
    case 'gdrive':
      return svg('0 0 87.3 78', <>
        <path d="m6.6 66.85 3.85 6.65c.8 1.4 1.95 2.5 3.3 3.3l13.75-23.8h-27.5c0 1.55.4 3.1 1.2 4.5z" fill="#0066da" />
        <path d="m43.65 25-13.75-23.8c-1.35.8-2.5 1.9-3.3 3.3l-25.4 44a9.06 9.06 0 0 0-1.2 4.5h27.5z" fill="#00ac47" />
        <path d="m73.55 76.8c1.35-.8 2.5-1.9 3.3-3.3l1.6-2.75 7.65-13.25c.8-1.4 1.2-2.95 1.2-4.5h-27.502l5.852 11.5z" fill="#ea4335" />
        <path d="m43.65 25 13.75-23.8c-1.35-.8-2.9-1.2-4.5-1.2h-18.5c-1.6 0-3.15.45-4.5 1.2z" fill="#00832d" />
        <path d="m59.8 53h-32.3l-13.75 23.8c1.35.8 2.9 1.2 4.5 1.2h50.8c1.6 0 3.15-.45 4.5-1.2z" fill="#2684fc" />
        <path d="m73.4 26.5-12.7-22c-.8-1.4-1.95-2.5-3.3-3.3l-13.75 23.8 16.15 28h27.45c0-1.55-.4-3.1-1.2-4.5z" fill="#ffba00" />
      </>)
    case 'dropbox':
      return svg('0 0 24 24', <path fill="#0061fe" d="M6 1.807 0 5.629l6 3.822 6.001-3.822L6 1.807zM18 1.807l-6 3.822 6 3.822 6-3.822-6-3.822zM0 13.274l6 3.822 6.001-3.822L6 9.452l-6 3.822zM18 9.452l-6 3.822 6 3.822 6-3.822-6-3.822zM6 18.371l6.001 3.822 6-3.822-6-3.822L6 18.371z" />)
    case 'onedrive':
      return svg('0 0 24 24', <>
        <defs><linearGradient id={id} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="#28a8ea" /><stop offset="1" stopColor="#1490df" /></linearGradient></defs>
        <path d={CLOUD} fill="#0364b8" transform="translate(-.5 .5) scale(.8)" />
        <path d={CLOUD} fill={`url(#${id})`} transform="translate(5.5 4.5) scale(.8)" />
      </>)
    case 'icloud':
      return svg('0 0 24 24', <>
        <defs><linearGradient id={id} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="#6cc4fa" /><stop offset="1" stopColor="#2f7cf6" /></linearGradient></defs>
        <path d={CLOUD} fill={`url(#${id})`} transform="translate(0 1)" />
      </>)
    default:
      return <Icon name="cloud" size={size} className={className} />
  }
}
