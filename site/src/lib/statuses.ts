// The five skill statuses, with the one-line meaning used across the site.
export type Status = 'required' | 'recommended' | 'available' | 'deprecated' | 'blocked';

export const STATUSES: Record<Status, { name: string; meaning: string }> = {
  required: {
    name: 'required',
    meaning: 'Installed automatically, kept up to date, restored if removed or edited. Cannot be disabled.',
  },
  recommended: {
    name: 'recommended',
    meaning: 'Installed by default. Developers can turn it off with skillcrew disable.',
  },
  available: {
    name: 'available',
    meaning: 'In the catalog. Developers add it with skillcrew install.',
  },
  deprecated: {
    name: 'deprecated',
    meaning: 'Stays where installed, never reaches new machines, shows its replacement.',
  },
  blocked: {
    name: 'blocked',
    meaning: 'The kill switch: removed from every machine at the next sync.',
  },
};

export const STATUS_ORDER: Status[] = ['required', 'recommended', 'available', 'deprecated', 'blocked'];
