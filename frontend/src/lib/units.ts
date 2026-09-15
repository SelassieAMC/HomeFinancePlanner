/** Fixed measure vocabulary for the unit dropdown (shared by bills and
 *  products so both forms offer the same measures). */
export const MEASURE_OPTIONS: { value: string; label: string }[] = [
  { value: 'liters', label: 'Liters' },
  { value: 'mililiters', label: 'Mililiters' },
  { value: 'grams', label: 'Grams' },
  { value: 'kilograms', label: 'Kilograms' },
  { value: 'per unit', label: 'Per unit' },
  { value: 'onzas', label: 'Onzas' },
];

/** Maps a receipt measure (kg, g, l, pcs, …) onto the fixed dropdown values. */
export function normalizeUnit(raw: string | undefined): string {
  const u = (raw ?? '').trim().toLowerCase();
  if (!u) return '';
  const known = MEASURE_OPTIONS.find((m) => m.value === u);
  if (known) return known.value;
  const map: Record<string, string> = {
    kg: 'kilograms',
    kgs: 'kilograms',
    kilo: 'kilograms',
    kilos: 'kilograms',
    g: 'grams',
    gr: 'grams',
    l: 'liters',
    lt: 'liters',
    ml: 'mililiters',
    oz: 'onzas',
    onza: 'onzas',
    unit: 'per unit',
    units: 'per unit',
    pc: 'per unit',
    pcs: 'per unit',
    un: 'per unit',
    u: 'per unit',
  };
  return map[u] ?? u;
}