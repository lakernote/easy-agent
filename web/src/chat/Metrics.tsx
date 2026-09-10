export function Metric({ label, value, sub, className = '' }: { label: string; value: string; sub?: string; className?: string }) {
  return <div className={`metric ${className}`.trim()}><span>{label}</span><strong>{value}</strong>{sub && <small>{sub}</small>}</div>
}
