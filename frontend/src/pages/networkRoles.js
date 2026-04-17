// Shared metadata for network-device roles. Kept in one file so the
// list page's filter bar, the card badge, and the detail page's
// inline-edit dropdown all use the same vocabulary + iconography.
//
// Values must match pkg/cisco role constants (router, switch,
// l3-switch, firewall, other, unknown). Backend is the source of
// truth; the handler validates against pkg/cisco.ValidRoles before
// accepting POST /role.

import { Router, Cpu, Network, ShieldAlert, HelpCircle, Server } from 'lucide-react'

export const ROLES = [
  { value: 'router',    label: 'Router',     short: 'Routers',     icon: Router,      color: 'cyan'   },
  { value: 'l3-switch', label: 'L3 switch',  short: 'L3 switches', icon: Network,     color: 'teal'   },
  { value: 'switch',    label: 'Switch',     short: 'Switches',    icon: Cpu,         color: 'brand'  },
  { value: 'firewall',  label: 'Firewall',   short: 'Firewalls',   icon: ShieldAlert, color: 'red'    },
  { value: 'other',     label: 'Other',      short: 'Other',       icon: Server,      color: 'slate'  },
  { value: 'unknown',   label: 'Unknown',    short: 'Unknown',     icon: HelpCircle,  color: 'amber'  },
]

// Fast lookup by value. Falls back to 'unknown' so the UI doesn't
// explode on a stray value that somehow got past validation.
export const ROLE_BY_VALUE = Object.fromEntries(ROLES.map(r => [r.value, r]))

export function roleMeta(value) {
  return ROLE_BY_VALUE[value] || ROLE_BY_VALUE['unknown']
}
