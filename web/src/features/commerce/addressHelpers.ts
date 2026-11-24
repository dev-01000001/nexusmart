export function normalizeAddressSearchText(value: string): string {
  return value
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/\s+/g, " ")
    .trim();
}

export interface SearchableWard<T extends { code: string; name: string }> {
  ward: T;
  normHay: string;
}

/**
 * Pre-normalizes ward search text once per loaded province list so typing in the filter
 * input never re-runs Unicode NFD normalization across hundreds/thousands of entries.
 */
export function prepareSearchableWards<T extends { code: string; name: string }>(
  wards: readonly T[]
): SearchableWard<T>[] {
  return wards.map((ward) => ({
    ward,
    normHay: `${normalizeAddressSearchText(ward.name)} ${ward.code.toLowerCase()}`,
  }));
}

export function filterPreparedWards<T extends { code: string; name: string }>(
  prepared: readonly SearchableWard<T>[],
  query: string
): T[] {
  const norm = normalizeAddressSearchText(query);
  if (!norm) return prepared.map((item) => item.ward);
  const tokens = norm.split(" ").filter(Boolean);
  const out: T[] = [];
  for (const item of prepared) {
    if (tokens.every((tok) => item.normHay.includes(tok))) {
      out.push(item.ward);
    }
  }
  return out;
}

/**
 * Filters a province's ward list (up to hundreds/thousands of entries per QĐ 19/2025)
 * case- and diacritic-insensitively by ward name or code.
 */
export function filterWardsByQuery<T extends { code: string; name: string }>(
  wards: readonly T[],
  query: string
): T[] {
  return filterPreparedWards(prepareSearchableWards(wards), query);
}

/**
 * Resolves the selected ward code after loading active wards for a province.
 * - Never silently falls back to `wards[0]`.
 * - Flags `legacyWardInactive` when editing an existing address whose saved ward_code
 *   is no longer present in the active 2-level catalog.
 */
export function resolveLoadedWardSelection<T extends { code: string }>(input: {
  wards: readonly T[];
  mappedWardCode?: string | null;
  currentWardCode: string;
  initialProvinceCode?: string;
  initialWardCode?: string;
  provinceCode: string;
}): { wardCode: string; legacyWardInactive: boolean } {
  const hasWard = (code: string | null | undefined): code is string =>
    Boolean(code) && input.wards.some((w) => w.code === code);

  if (hasWard(input.mappedWardCode)) {
    return { wardCode: input.mappedWardCode, legacyWardInactive: false };
  }
  if (hasWard(input.currentWardCode)) {
    return { wardCode: input.currentWardCode, legacyWardInactive: false };
  }
  if (input.initialWardCode && input.initialProvinceCode === input.provinceCode) {
    if (hasWard(input.initialWardCode)) {
      return { wardCode: input.initialWardCode, legacyWardInactive: false };
    }
    return { wardCode: "", legacyWardInactive: true };
  }
  return { wardCode: "", legacyWardInactive: false };
}

/**
 * Clears the current ward selection whenever the active search query excludes it
 * from the filtered `<select>` options so the form can never silently submit a hidden ward.
 */
export function reconcileWardAfterFilter<T extends { code: string }>(
  filteredWards: readonly T[],
  currentWardCode: string
): { wardCode: string; clearedByFilter: boolean } {
  if (!currentWardCode) {
    return { wardCode: "", clearedByFilter: false };
  }
  if (filteredWards.some((w) => w.code === currentWardCode)) {
    return { wardCode: currentWardCode, clearedByFilter: false };
  }
  return { wardCode: "", clearedByFilter: true };
}

export interface MappedLegacyAddressCodes {
  provinceCode: string;
  districtCode: string;
  wardCode: string;
  targetProvinceCode: string;
  targetWardCode: string;
}

/**
 * Returns `legacy_*_code` payload fields ONLY when `map-legacy` succeeded AND the user has not
 * changed province/ward away from the mapped destination. Never falls back to raw typed names.
 */
export function resolveLegacyCodesForPayload(
  mapped: MappedLegacyAddressCodes | null,
  selectedProvinceCode: string,
  selectedWardCode: string
): {
  legacy_province_code?: string;
  legacy_district_code?: string;
  legacy_ward_code?: string;
} {
  if (!mapped) return {};
  if (
    mapped.targetProvinceCode !== selectedProvinceCode ||
    mapped.targetWardCode !== selectedWardCode
  ) {
    return {};
  }
  const prov = mapped.provinceCode.trim();
  const dist = mapped.districtCode.trim();
  const ward = mapped.wardCode.trim();
  return {
    ...(prov ? { legacy_province_code: prov } : {}),
    ...(dist ? { legacy_district_code: dist } : {}),
    ...(ward ? { legacy_ward_code: ward } : {}),
  };
}
