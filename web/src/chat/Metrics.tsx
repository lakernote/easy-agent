export function Metric({ label, value, sub }: { label: string; value: string; sub: string }) {
  return <div><span>{label}</span><strong>{value}</strong><small>{sub}</small></div>
}
