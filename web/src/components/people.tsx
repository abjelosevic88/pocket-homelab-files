import type { User } from '../lib/types'
import { colorFor, initials } from '../lib/format'

export function Avatar({ user, size = 24 }: { user: Pick<User, 'id' | 'name' | 'email'>; size?: number }) {
  const name = user.name || user.email
  return <span className="avatar" style={{ width: size, height: size, fontSize: Math.round(size * 0.4), background: colorFor(user.id) }} title={name}>{initials(name)}</span>
}
