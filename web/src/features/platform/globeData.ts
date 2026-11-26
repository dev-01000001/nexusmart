// Geographic datasets for the homepage globe (coordinates only — no labels or borders).
// Tuple format: [latitudeDeg, longitudeDeg, prominenceWeight (0.35..1.0)].

export type CityPoint = readonly [lat: number, lon: number, weight: number];

export interface GlobeArcSpec {
  readonly from: readonly [lat: number, lon: number];
  readonly to: readonly [lat: number, lon: number];
  /** Peak lift above the sphere surface as a multiple of chord length (0.25..0.4). */
  readonly altitude: number;
  /** Gentle tangent-plane bow so central north-south arcs retain visible curvature. */
  readonly lateralBow: number;
  /** Normalized cycle phase offset in [0, 1). */
  readonly phase: number;
  /** Seconds per animation cycle. */
  readonly duration: number;
}

// 34 provincial/municipal capitals of Vietnam (2025 administrative units,
// aligned 1:1 with web/src/lib/provinces.ts).
export const VN_CAPITALS: readonly CityPoint[] = [
  // Centrally governed cities (6)
  [21.03, 105.85, 1.0], // HN
  [20.84, 106.69, 0.9], // HP
  [16.46, 107.59, 0.84], // HUE
  [16.05, 108.2, 0.94], // DN
  [10.78, 106.7, 1.0], // HCM
  [10.04, 105.75, 0.9], // CT
  // Northern provinces (12)
  [22.4, 103.46, 0.66], // LCH
  [21.39, 103.02, 0.66], // DB
  [21.33, 103.91, 0.68], // SL
  [21.85, 106.76, 0.7], // LS
  [20.96, 107.07, 0.82], // QN
  [22.67, 106.26, 0.66], // CB
  [21.82, 105.21, 0.68], // TQ
  [21.72, 104.91, 0.72], // LC
  [21.59, 105.84, 0.76], // TNG
  [21.32, 105.4, 0.74], // PT
  [21.27, 106.2, 0.78], // BN
  [20.65, 106.05, 0.76], // HY
  [20.25, 105.97, 0.74], // NB
  // Central & Central Highlands provinces (9)
  [19.81, 105.78, 0.78], // TH
  [18.68, 105.68, 0.82], // NA
  [18.34, 105.91, 0.7], // HT
  [17.47, 106.62, 0.72], // QTR
  [15.12, 108.8, 0.74], // QNG
  [13.78, 109.22, 0.8], // GL
  [12.24, 109.19, 0.84], // KH
  [11.94, 108.44, 0.8], // LD
  [12.67, 108.04, 0.76], // DL
  // Southern provinces (6)
  [10.95, 106.84, 0.82], // DNI
  [10.54, 106.41, 0.74], // TNI
  [10.25, 105.97, 0.74], // VL
  [10.36, 106.36, 0.74], // DT
  [9.18, 105.15, 0.76], // CM
  [10.01, 105.08, 0.76], // AG
];

// ~156 major world cities (subtle amber urban lights across the globe,
// excluding Vietnam which is represented by VN_CAPITALS above).
export const WORLD_CITIES: readonly CityPoint[] = [
  // Southeast Asia
  [13.76, 100.5, 0.88],
  [18.79, 98.98, 0.55],
  [7.88, 98.39, 0.48],
  [12.93, 100.88, 0.52],
  [1.35, 103.82, 0.92],
  [3.14, 101.69, 0.84],
  [5.41, 100.33, 0.58],
  [1.49, 103.74, 0.56],
  [5.98, 116.07, 0.48],
  [1.55, 110.36, 0.48],
  [-6.21, 106.85, 0.9],
  [-7.25, 112.75, 0.78],
  [-6.91, 107.61, 0.7],
  [-7.01, 110.42, 0.6],
  [3.59, 98.67, 0.64],
  [-5.14, 119.43, 0.56],
  [-8.65, 115.22, 0.54],
  [-2.98, 104.76, 0.52],
  [14.6, 120.98, 0.88],
  [10.32, 123.89, 0.64],
  [7.19, 125.46, 0.58],
  [16.87, 96.2, 0.72],
  [21.96, 96.09, 0.56],
  [11.56, 104.92, 0.62],
  [17.97, 102.63, 0.52],
  [4.9, 114.94, 0.42],
  // East Asia
  [23.13, 113.26, 0.9],
  [22.54, 114.06, 0.88],
  [22.32, 114.17, 0.88],
  [22.82, 108.37, 0.62],
  [25.04, 102.71, 0.66],
  [24.48, 118.09, 0.7],
  [26.07, 119.3, 0.68],
  [28.23, 112.94, 0.72],
  [30.59, 114.3, 0.82],
  [30.57, 104.07, 0.82],
  [29.56, 106.55, 0.82],
  [31.23, 121.47, 0.94],
  [30.27, 120.15, 0.82],
  [32.06, 118.8, 0.78],
  [31.3, 120.59, 0.74],
  [34.34, 108.94, 0.76],
  [34.75, 113.63, 0.74],
  [36.07, 120.38, 0.74],
  [36.65, 117.12, 0.7],
  [39.9, 116.41, 0.94],
  [39.34, 117.36, 0.82],
  [38.91, 121.61, 0.68],
  [41.8, 123.43, 0.74],
  [43.82, 125.32, 0.66],
  [45.8, 126.53, 0.7],
  [36.06, 103.83, 0.58],
  [43.83, 87.62, 0.56],
  [29.65, 91.11, 0.4],
  [25.03, 121.57, 0.84],
  [22.63, 120.3, 0.68],
  [24.15, 120.67, 0.66],
  [37.57, 126.98, 0.92],
  [35.18, 129.08, 0.76],
  [35.87, 128.6, 0.64],
  [39.04, 125.76, 0.52],
  [35.68, 139.69, 0.95],
  [34.69, 135.5, 0.86],
  [35.18, 136.91, 0.78],
  [33.59, 130.4, 0.72],
  [43.06, 141.35, 0.66],
  [38.27, 140.87, 0.6],
  [34.39, 132.46, 0.6],
  [26.21, 127.68, 0.48],
  [47.92, 106.92, 0.5],
  // South Asia
  [28.61, 77.21, 0.92],
  [19.08, 72.88, 0.92],
  [22.57, 88.36, 0.86],
  [12.97, 77.59, 0.84],
  [13.08, 80.27, 0.82],
  [17.39, 78.49, 0.82],
  [23.02, 72.57, 0.76],
  [18.52, 73.86, 0.74],
  [26.91, 75.79, 0.7],
  [26.85, 80.95, 0.7],
  [25.59, 85.14, 0.66],
  [21.15, 79.09, 0.64],
  [9.93, 76.27, 0.6],
  [26.14, 91.74, 0.56],
  [23.81, 90.41, 0.84],
  [22.36, 91.78, 0.64],
  [24.86, 67.01, 0.84],
  [31.52, 74.36, 0.8],
  [33.68, 73.05, 0.68],
  [6.93, 79.86, 0.64],
  [27.72, 85.32, 0.6],
  [34.56, 69.21, 0.56],
  // Oceania
  [-33.87, 151.21, 0.84],
  [-37.81, 144.96, 0.82],
  [-27.47, 153.03, 0.72],
  [-31.95, 115.86, 0.68],
  [-34.93, 138.6, 0.6],
  [-12.46, 130.84, 0.44],
  [-36.85, 174.76, 0.66],
  [-41.29, 174.78, 0.5],
  [-9.44, 147.18, 0.44],
  // Central & West Asia / Middle East
  [41.3, 69.24, 0.64],
  [43.22, 76.85, 0.62],
  [51.17, 71.47, 0.54],
  [40.41, 49.87, 0.6],
  [35.69, 51.39, 0.82],
  [36.26, 59.62, 0.62],
  [25.2, 55.27, 0.82],
  [24.45, 54.38, 0.68],
  [25.29, 51.53, 0.66],
  [24.71, 46.68, 0.78],
  [21.49, 39.19, 0.72],
  [29.38, 47.98, 0.66],
  [23.59, 58.41, 0.56],
  [33.31, 44.37, 0.74],
  [31.95, 35.93, 0.64],
  [32.09, 34.78, 0.68],
  [41.01, 28.98, 0.86],
  [39.93, 32.86, 0.72],
  // Northern Eurasia & Europe
  [55.76, 37.62, 0.86],
  [59.93, 30.34, 0.76],
  [55.01, 82.93, 0.62],
  [56.84, 60.6, 0.62],
  [56.02, 92.87, 0.54],
  [52.29, 104.29, 0.52],
  [43.12, 131.89, 0.56],
  [48.48, 135.08, 0.52],
  [50.45, 30.52, 0.72],
  [52.23, 21.01, 0.72],
  [44.43, 26.1, 0.64],
  [37.98, 23.73, 0.68],
  [52.52, 13.4, 0.78],
  [48.21, 16.37, 0.7],
  [41.9, 12.5, 0.76],
  [48.86, 2.35, 0.84],
  [51.51, -0.13, 0.86],
  [40.42, -3.7, 0.78],
  // Africa
  [30.04, 31.24, 0.84],
  [9.03, 38.74, 0.66],
  [-1.29, 36.82, 0.7],
  [-6.79, 39.21, 0.66],
  [-26.2, 28.05, 0.78],
  [-33.92, 18.42, 0.68],
  [6.52, 3.38, 0.82],
  [33.57, -7.59, 0.68],
  // Americas (for global dataset completeness)
  [40.71, -74.01, 0.92],
  [34.05, -118.24, 0.88],
  [41.88, -87.63, 0.82],
  [37.77, -122.42, 0.78],
  [43.65, -79.38, 0.78],
  [19.43, -99.13, 0.86],
  [-23.55, -46.63, 0.88],
  [-34.6, -58.38, 0.82],
  [4.71, -74.07, 0.76],
  [-12.05, -77.04, 0.76],
  [-33.45, -70.67, 0.72],
  [61.22, -149.9, 0.44],
];

// Anchor coordinates of key Vietnamese hubs for the animated domestic arcs.
const HANOI: readonly [number, number] = [21.03, 105.85];
const HAIPHONG: readonly [number, number] = [20.84, 106.69];
const QUANGNINH: readonly [number, number] = [20.96, 107.07];
const VINH: readonly [number, number] = [18.68, 105.68];
const HUE: readonly [number, number] = [16.46, 107.59];
const DANANG: readonly [number, number] = [16.05, 108.2];
const QUYNHON: readonly [number, number] = [13.78, 109.22];
const NHATRANG: readonly [number, number] = [12.24, 109.19];
const DALAT: readonly [number, number] = [11.94, 108.44];
const HCMC: readonly [number, number] = [10.78, 106.7];
const CANTHO: readonly [number, number] = [10.04, 105.75];
const CAMAU: readonly [number, number] = [9.18, 105.15];

// 14 luminous arcs criss-crossing Vietnam's domestic trade corridors.
export const VN_ARCS: readonly GlobeArcSpec[] = [
  { from: HANOI, to: HCMC, altitude: 0.36, lateralBow: 0.34, phase: 0.0, duration: 4.4 },
  { from: HCMC, to: HANOI, altitude: 0.38, lateralBow: -0.32, phase: 0.48, duration: 4.8 },
  { from: HANOI, to: DANANG, altitude: 0.34, lateralBow: 0.34, phase: 0.14, duration: 3.6 },
  { from: DANANG, to: HCMC, altitude: 0.36, lateralBow: 0.34, phase: 0.62, duration: 3.8 },
  { from: HAIPHONG, to: HCMC, altitude: 0.38, lateralBow: 0.44, phase: 0.27, duration: 4.6 },
  { from: HAIPHONG, to: DANANG, altitude: 0.32, lateralBow: -0.28, phase: 0.78, duration: 3.5 },
  { from: HCMC, to: CANTHO, altitude: 0.36, lateralBow: -0.4, phase: 0.09, duration: 3.0 },
  { from: DANANG, to: CANTHO, altitude: 0.38, lateralBow: -0.34, phase: 0.38, duration: 4.1 },
  { from: HANOI, to: HUE, altitude: 0.32, lateralBow: -0.34, phase: 0.54, duration: 3.4 },
  { from: HUE, to: NHATRANG, altitude: 0.34, lateralBow: 0.38, phase: 0.85, duration: 3.7 },
  { from: HCMC, to: NHATRANG, altitude: 0.36, lateralBow: 0.38, phase: 0.21, duration: 3.2 },
  { from: HCMC, to: DALAT, altitude: 0.34, lateralBow: -0.36, phase: 0.69, duration: 3.1 },
  { from: VINH, to: HCMC, altitude: 0.36, lateralBow: -0.4, phase: 0.33, duration: 4.2 },
  { from: QUANGNINH, to: QUYNHON, altitude: 0.36, lateralBow: 0.38, phase: 0.91, duration: 4.3 },
  { from: CANTHO, to: NHATRANG, altitude: 0.36, lateralBow: -0.34, phase: 0.44, duration: 3.9 },
  { from: HCMC, to: CAMAU, altitude: 0.36, lateralBow: 0.4, phase: 0.74, duration: 3.2 },
];

// Origin of the signature soaring hero arc that vaults above the upper globe rim.
export const HERO_ARC_ORIGIN: readonly [number, number] = HCMC;
