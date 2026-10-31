// A search snippet: the server marks matched words with  … .
export function Snippet({ text, className = '' }: { text: string; className?: string }) {
  const parts = text.split(/([^]*)/g).filter(Boolean)
  return <span className={`snippet ${className}`}>{parts.map((p, i) => p.startsWith('') ? <mark key={i}>{p.slice(1, -1)}</mark> : <span key={i}>{p}</span>)}</span>
}
