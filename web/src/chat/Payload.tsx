import { useMemo } from 'react'

export function Payload({ value }: { value: string }) {
  const formatted = useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }, [value])

  return <pre>{formatted}</pre>
}
