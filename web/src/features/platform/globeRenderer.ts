import {
  HERO_ARC_ORIGIN,
  VN_ARCS,
  VN_CAPITALS,
  WORLD_CITIES,
  type CityPoint,
  type GlobeArcSpec,
} from "./globeData";
import {
  AXIAL_ROLL_DEG,
  REST_LAT_DEG,
  REST_LON_DEG,
  beginDrag,
  buildRotationMatrix,
  createGlobeMotionState,
  easeInOutCubic,
  endDrag,
  latLonToPlanet,
  latLonToViewAt,
  moveDrag,
  rotateVec3Transpose,
  stepGlobeMotion,
  writeRotationMatrix,
} from "./globeMotion";

export type LandDot = readonly [lat: number, lon: number];

export interface GlobeController {
  setVisible(visible: boolean): void;
  setReducedMotion(reduced: boolean): void;
  resize(): void;
  destroy(): void;
}

export interface CreateGlobeOptions {
  canvas: HTMLCanvasElement;
  landDots: readonly LandDot[];
  reducedMotion: boolean;
  onFirstRender?: () => void;
}

// ---------------------------------------------------------------------------
// Palette & projection constants (all Canvas colors live here)
// ---------------------------------------------------------------------------
const DEG = Math.PI / 180;
const TAU = Math.PI * 2;
const GLOBE_CENTER_X_RATIO = 0.46;
const GLOBE_CENTER_Y_RATIO = 0.56;
const GLOBE_RADIUS_W_RATIO = 0.39; // diameter = 78% of container width

// Sun direction in view space (+x right, +y north/up, +z toward viewer)
const LIGHT_LEN = Math.hypot(-0.74, 0.56, 0.37);
const LIGHT_X = -0.74 / LIGHT_LEN;
const LIGHT_Y = 0.56 / LIGHT_LEN;
const LIGHT_Z = 0.37 / LIGHT_LEN;
const INV_LIGHT_SPAN = 1 / 1.24; // 0.92 - (-0.32)

const PALETTE = {
  oceanBase: "#04081c",
  oceanSunCore: "#1ea6ba",
  oceanSunMid: "#117896",
  oceanMidBlue: "#0a456e",
  oceanRoyalNavy: "#072352",
  oceanTwilight: "#061134",
  oceanNight: "#040922",
  oceanDeepShadow: "#030616",
  oceanNightTint: "rgba(16, 36, 98, 0.24)",
  atmoOuterInner: "rgba(32, 178, 190, 0.26)",
  atmoOuterMid: "rgba(12, 118, 138, 0.11)",
  atmoOuterEdge: "rgba(0, 84, 88, 0)",
  atmoRimPeak: "rgba(96, 232, 236, 0.50)",
  atmoRimSoft: "rgba(32, 176, 194, 0.21)",
  atmoRimTail: "rgba(10, 110, 136, 0.06)",
  atmoRimEdge: "rgba(0, 84, 88, 0)",
  fresnelMid: "rgba(42, 198, 212, 0.24)",
  fresnelEdge: "rgba(124, 240, 240, 0.60)",
  sunRimSoft: "rgba(86, 228, 232, 0.26)",
  sunRimPeak: "rgba(162, 248, 246, 0.68)",
  twilightRimGold: "rgba(255, 218, 146, 0.84)",
  twilightRimWarmHalo: "rgba(255, 162, 68, 0.32)",
  twilightRimIndigo: "rgba(74, 116, 206, 0.30)",
  landDarkRgb: [12, 46, 44] as const,
  landMidRgb: [28, 118, 90] as const,
  landLitRgb: [62, 182, 138] as const,
  worldGlow: "255, 146, 42",
  worldCore: "255, 218, 134",
  vnGlowOuter: "255, 136, 28",
  vnGlowMid: "255, 200, 86",
  vnCore: "255, 244, 198",
  arcGlow: "198, 246, 252",
  arcCore: "255, 252, 242",
  arcSpark: "255, 255, 255",
  heroBaseGlow: "168, 240, 248",
  heroBaseCore: "242, 253, 255",
  starTint: "236, 252, 252",
  starHalo: "196, 242, 244",
} as const;

// Quantized buckets (24 Pass A + 24 Pass B) keep fill() calls low at full device resolution.
// Grouping overlapping Pass A dots merges union alpha once, so PASS_A_ALPHA_COMP preserves density.
const PASS_A_SHADE_BINS = 6;
const PASS_A_ALPHA_BINS = 4;
const PASS_A_BUCKET_COUNT = PASS_A_SHADE_BINS * PASS_A_ALPHA_BINS;
const PASS_A_ALPHA_COMP = 1.18;
const PASS_A_ALPHA_MAX = 0.56;

const PASS_B_SHADE_BINS = 6;
const PASS_B_ALPHA_BINS = 4;
const PASS_B_BUCKET_COUNT = PASS_B_SHADE_BINS * PASS_B_ALPHA_BINS;
const PASS_B_RECT_SCALE = 1.82;
// During active motion, skip barely visible Pass B stipple dots on the dark flank & extreme limb
// (masked by the 72% terminator veil and bright Fresnel rim) while keeping full device resolution.
const MOTION_PASS_B_MIN_Z = 0.22;
const MOTION_PASS_B_MIN_ALPHA = 0.35;

const SPARK_GLOW_BINS = 4;
const CORE_ALPHA_BINS = 8;

// ---------------------------------------------------------------------------
// Precomputed LUTs, styles & math helpers
// ---------------------------------------------------------------------------
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
const clamp01 = (v: number) => (v < 0 ? 0 : v > 1 ? 1 : v);

function makePowLut(exp: number): Float32Array {
  const lut = new Float32Array(256);
  for (let i = 0; i < 256; i++) lut[i] = Math.pow(i / 255, exp);
  return lut;
}

const POW_042 = makePowLut(0.42);
const POW_046 = makePowLut(0.46);
const POW_048 = makePowLut(0.48);
const POW_050 = makePowLut(0.5);
const POW_065 = makePowLut(0.65);
const POW_078 = makePowLut(0.78);

type ColorStop = readonly [offset: number, color: string];

function addStops(grad: CanvasGradient, stops: readonly ColorStop[]): CanvasGradient {
  for (let i = 0; i < stops.length; i++) grad.addColorStop(stops[i][0], stops[i][1]);
  return grad;
}

function linearGrad(
  ctx: CanvasRenderingContext2D,
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  stops: readonly ColorStop[]
): CanvasGradient {
  return addStops(ctx.createLinearGradient(x0, y0, x1, y1), stops);
}

function radialGrad(
  ctx: CanvasRenderingContext2D,
  x0: number,
  y0: number,
  r0: number,
  x1: number,
  y1: number,
  r1: number,
  stops: readonly ColorStop[]
): CanvasGradient {
  return addStops(ctx.createRadialGradient(x0, y0, r0, x1, y1, r1), stops);
}

function buildLandStyles(
  shadeBins: number,
  alphaBins: number,
  alphaMin: number,
  alphaMax: number
): string[] {
  const styles: string[] = new Array(shadeBins * alphaBins);
  for (let sb = 0; sb < shadeBins; sb++) {
    const shade = (sb + 0.5) / shadeBins;
    const low = shade < 0.5;
    const t = low ? shade * 2 : (shade - 0.5) * 2;
    const from = low ? PALETTE.landDarkRgb : PALETTE.landMidRgb;
    const to = low ? PALETTE.landMidRgb : PALETTE.landLitRgb;
    const rgb = `${Math.round(lerp(from[0], to[0], t))},${Math.round(lerp(from[1], to[1], t))},${Math.round(lerp(from[2], to[2], t))}`;
    for (let ab = 0; ab < alphaBins; ab++) {
      styles[sb * alphaBins + ab] =
        `rgba(${rgb},${lerp(alphaMin, alphaMax, (ab + 0.5) / alphaBins).toFixed(3)})`;
    }
  }
  return styles;
}

function buildAlphaStyles(rgb: string, bins: number, minA: number, maxA: number): string[] {
  return Array.from(
    { length: bins },
    (_, i) => `rgba(${rgb},${lerp(minA, maxA, (i + 0.5) / bins).toFixed(3)})`
  );
}

const PASS_A_STYLES = buildLandStyles(PASS_A_SHADE_BINS, PASS_A_ALPHA_BINS, 0.04, PASS_A_ALPHA_MAX);
const PASS_B_STYLES = buildLandStyles(PASS_B_SHADE_BINS, PASS_B_ALPHA_BINS, 0.06, 0.96);
const WORLD_CORE_STYLES = buildAlphaStyles(PALETTE.worldCore, CORE_ALPHA_BINS, 0.08, 0.95);
const VN_CORE_STYLES = buildAlphaStyles(PALETTE.vnCore, CORE_ALPHA_BINS, 0.14, 1.0);
const SPARK_GLOW_ALPHAS = [0.16, 0.24, 0.32, 0.4] as const;

function hash2(a: number, b: number): number {
  const s = Math.sin(a * 127.1 + b * 311.7) * 43758.5453123;
  return s - Math.floor(s);
}

function valueNoise2D(x: number, y: number): number {
  const ix = Math.floor(x);
  const iy = Math.floor(y);
  const fx = x - ix;
  const fy = y - iy;
  const ux = fx * fx * (3 - 2 * fx);
  const uy = fy * fy * (3 - 2 * fy);
  return lerp(
    lerp(hash2(ix, iy), hash2(ix + 1, iy), ux),
    lerp(hash2(ix, iy + 1), hash2(ix + 1, iy + 1), ux),
    uy
  );
}

function gaussianPair(rand: () => number): readonly [number, number] {
  const mag = Math.sqrt(-2 * Math.log(Math.max(1e-6, rand())));
  const theta = TAU * rand();
  return [mag * Math.cos(theta), mag * Math.sin(theta)];
}

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------------------------------------------------------------------------
// Reusable typed-array draw buckets & precomputed planet-space geometry
// ---------------------------------------------------------------------------
interface DrawBuckets {
  x: Float32Array;
  y: Float32Array;
  s: Float32Array;
  next: Int32Array;
  heads: Int32Array;
}

function makeBuckets(capacity: number, bins: number): DrawBuckets {
  return {
    x: new Float32Array(capacity),
    y: new Float32Array(capacity),
    s: new Float32Array(capacity),
    next: new Int32Array(capacity),
    heads: new Int32Array(bins),
  };
}

function flushRectBuckets(
  ctx: CanvasRenderingContext2D,
  buckets: DrawBuckets,
  styles: readonly string[]
): void {
  const { x, y, s, next, heads } = buckets;
  for (let b = 0; b < heads.length; b++) {
    let idx = heads[b];
    if (idx === -1) continue;
    ctx.fillStyle = styles[b];
    ctx.beginPath();
    while (idx !== -1) {
      const size = s[idx];
      ctx.rect(x[idx], y[idx], size, size);
      idx = next[idx];
    }
    ctx.fill();
  }
}

interface PrecomputedLand {
  aCount: number;
  aPx: Float32Array;
  aPy: Float32Array;
  aPz: Float32Array;
  aBiome: Float32Array;
  aSize: Float32Array;
  bPx: Float32Array;
  bPy: Float32Array;
  bPz: Float32Array;
  bTerrain: Float32Array;
  bSize: Float32Array;
  aBuckets: DrawBuckets;
  bBuckets: DrawBuckets;
  visIdx: Int32Array;
  visVz: Float32Array;
  visDl: Float32Array;
  visLimbB: Float32Array;
  visDayPowB: Float32Array;
}

interface PrecomputedSparks {
  count: number;
  px: Float32Array;
  py: Float32Array;
  pz: Float32Array;
  baseIntensity: Float32Array;
  glowFactor: Float32Array;
  coreFactor: Float32Array;
  glow: DrawBuckets;
  core: DrawBuckets;
}

interface CityEntry {
  px: number;
  py: number;
  pz: number;
  weight: number;
  phase: number;
  speed: number;
  x: number;
  y: number;
  z: number;
  limb: number;
  visible: boolean;
}

interface SampledArcPoint {
  x: number;
  y: number;
  visible: boolean;
}

interface PrecomputedArc {
  pointCount: number;
  px: Float32Array;
  py: Float32Array;
  pz: Float32Array;
  points: SampledArcPoint[];
  anyVisible: boolean;
  endVz: number;
  phase: number;
  duration: number;
}

interface Star {
  x: number;
  y: number;
  radius: number;
  haloRadius: number;
  baseAlpha: number;
  phase: number;
  speed: number;
}

interface LayoutMetrics {
  width: number;
  height: number;
  pxW: number;
  pxH: number;
  dpr: number;
  cx: number;
  cy: number;
  radius: number;
  boxPxX: number;
  boxPxY: number;
  boxPxSize: number;
  featherTopPx: number;
}

interface GlowSprites {
  worldGlow: HTMLCanvasElement;
  vnCityGlow: HTMLCanvasElement;
  vnSparkGlow: HTMLCanvasElement;
  starHalo: HTMLCanvasElement;
  arcSpark: HTMLCanvasElement;
}

function precomputeLand(landDots: readonly LandDot[]): PrecomputedLand {
  const aCount = landDots.length;
  const bCount = aCount * 3;
  const land: PrecomputedLand = {
    aCount,
    aPx: new Float32Array(aCount),
    aPy: new Float32Array(aCount),
    aPz: new Float32Array(aCount),
    aBiome: new Float32Array(aCount),
    aSize: new Float32Array(aCount),
    bPx: new Float32Array(bCount),
    bPy: new Float32Array(bCount),
    bPz: new Float32Array(bCount),
    bTerrain: new Float32Array(bCount),
    bSize: new Float32Array(bCount),
    aBuckets: makeBuckets(aCount, PASS_A_BUCKET_COUNT),
    bBuckets: makeBuckets(bCount, PASS_B_BUCKET_COUNT),
    visIdx: new Int32Array(aCount),
    visVz: new Float32Array(aCount),
    visDl: new Float32Array(aCount),
    visLimbB: new Float32Array(aCount),
    visDayPowB: new Float32Array(aCount),
  };

  let bi = 0;
  for (let i = 0; i < aCount; i++) {
    const [rawLat, rawLon] = landDots[i];
    const cosLat = Math.max(0.26, Math.cos(rawLat * DEG));
    const latA = Math.max(
      -89,
      Math.min(89, rawLat + (hash2(rawLat * 1.07 + 1.9, rawLon * 0.93 - 3.1) - 0.5) * 0.68)
    );
    const lonA = rawLon + (hash2(rawLon * 1.13 - 2.7, rawLat * 0.87 + 4.3) - 0.5) * (0.72 / cosLat);
    [land.aPx[i], land.aPy[i], land.aPz[i]] = latLonToPlanet(latA, lonA);
    land.aBiome[i] = valueNoise2D(latA * 0.13 + 5.2, lonA * 0.13 - 3.7);
    land.aSize[i] = hash2(rawLat * 1.91 + 3.4, rawLon * 1.57 - 2.2);

    for (let s = 0; s < 3; s++) {
      const jLatB =
        (hash2(rawLat * (1.17 + s * 0.73) + 4.3 + s * 11.7, rawLon * (0.89 + s * 0.41) - 2.1) -
          0.5) *
        1.04;
      const jLonB =
        (hash2(rawLon * (1.31 + s * 0.59) - 6.7, rawLat * (0.79 + s * 0.67) + 9.4 + s * 7.3) -
          0.5) *
        (1.08 / cosLat);
      const latB = Math.max(-89, Math.min(89, rawLat + jLatB));
      const lonB = rawLon + jLonB;
      [land.bPx[bi], land.bPy[bi], land.bPz[bi]] = latLonToPlanet(latB, lonB);
      land.bTerrain[bi] =
        0.58 * valueNoise2D(latB * 0.17 + 12.4, lonB * 0.17 - 7.8) +
        0.42 * hash2(rawLat * 3.17 + s * 9.3, rawLon * 2.63 + s * 4.1);
      land.bSize[bi] = hash2(rawLat * 2.41 + s * 13.1, rawLon * 1.73 - s * 5.9);
      bi++;
    }
  }
  return land;
}

function allocSparks(count: number): PrecomputedSparks {
  return {
    count,
    px: new Float32Array(count),
    py: new Float32Array(count),
    pz: new Float32Array(count),
    baseIntensity: new Float32Array(count),
    glowFactor: new Float32Array(count),
    coreFactor: new Float32Array(count),
    glow: makeBuckets(count, SPARK_GLOW_BINS),
    core: makeBuckets(count, CORE_ALPHA_BINS),
  };
}

function precomputeSparks(): { world: PrecomputedSparks; vn: PrecomputedSparks } {
  const rand = mulberry32(20261127);

  let worldTotal = 0;
  for (const c of WORLD_CITIES) worldTotal += Math.round(4 + c[2] * 10);
  const world = allocSparks(worldTotal);
  let wi = 0;
  for (const [cLat, cLon, weight] of WORLD_CITIES) {
    const cosLat = Math.max(0.3, Math.cos(cLat * DEG));
    const sparkCount = Math.round(4 + weight * 10);
    const sigmaLat = 0.48 + weight * 0.85;
    const sigmaLon = (0.58 + weight * 1.0) / cosLat;
    for (let k = 0; k < sparkCount; k++) {
      const [g0, g1] = gaussianPair(rand);
      [world.px[wi], world.py[wi], world.pz[wi]] = latLonToPlanet(
        Math.max(-85, Math.min(85, cLat + g0 * sigmaLat)),
        cLon + g1 * sigmaLon
      );
      const falloff = Math.exp(-Math.hypot(g0, g1) * 0.58);
      world.baseIntensity[wi] = weight * falloff * (0.48 + 0.52 * rand());
      world.glowFactor[wi] = 1.3 + 1.4 * falloff;
      world.coreFactor[wi] = 0.42 + 0.38 * falloff;
      wi++;
    }
  }

  let vnTotal = 0;
  for (const c of VN_CAPITALS) if (c[2] >= 0.84) vnTotal += 2;
  const vn = allocSparks(vnTotal);
  let vi = 0;
  for (const [cLat, cLon, weight] of VN_CAPITALS) {
    if (weight < 0.84) continue;
    for (let k = 0; k < 2; k++) {
      const [g0, g1] = gaussianPair(rand);
      [vn.px[vi], vn.py[vi], vn.pz[vi]] = latLonToPlanet(cLat + g0 * 0.12, cLon + g1 * 0.12);
      const falloff = Math.exp(-Math.hypot(g0, g1) * 0.72);
      vn.baseIntensity[vi] = weight * falloff * (0.5 + 0.5 * rand());
      vn.glowFactor[vi] = 1.1 + 0.8 * falloff;
      vn.coreFactor[vi] = 0.36 + 0.24 * falloff;
      vi++;
    }
  }
  return { world, vn };
}

function precomputeCities(cities: readonly CityPoint[], isVietnam: boolean): CityEntry[] {
  return cities.map(([lat, lon, weight]) => {
    const [px, py, pz] = latLonToPlanet(lat, lon);
    return {
      px,
      py,
      pz,
      weight,
      phase: hash2(lat, lon) * TAU,
      speed: isVietnam ? 1.1 + hash2(lon, lat) * 0.9 : 0.8 + hash2(lon, lat) * 1.2,
      x: 0,
      y: 0,
      z: 0,
      limb: 0,
      visible: false,
    };
  });
}

function makeArcShell(pointCount: number, phase: number, duration: number): PrecomputedArc {
  return {
    pointCount,
    px: new Float32Array(pointCount),
    py: new Float32Array(pointCount),
    pz: new Float32Array(pointCount),
    points: Array.from({ length: pointCount }, () => ({ x: 0, y: 0, visible: false })),
    anyVisible: false,
    endVz: -1,
    phase,
    duration,
  };
}

function precomputeDomesticArc(spec: GlobeArcSpec): PrecomputedArc {
  const [p0x, p0y, p0z] = latLonToPlanet(spec.from[0], spec.from[1]);
  const [p1x, p1y, p1z] = latLonToPlanet(spec.to[0], spec.to[1]);
  const [dx, dy, dz] = [p1x - p0x, p1y - p0y, p1z - p0z];
  const chordLen = Math.hypot(dx, dy, dz);
  const mLen = Math.hypot(p0x + p1x, p0y + p1y, p0z + p1z) || 1;
  const [rx, ry, rz] = [(p0x + p1x) / mLen, (p0y + p1y) / mLen, (p0z + p1z) / mLen];
  let [lx, ly, lz] = [dy * rz - dz * ry, dz * rx - dx * rz, dx * ry - dy * rx];
  const lLen = Math.hypot(lx, ly, lz) || 1;
  lx /= lLen;
  ly /= lLen;
  lz /= lLen;

  const segments = 28;
  const arc = makeArcShell(segments + 1, spec.phase, spec.duration);
  for (let i = 0; i <= segments; i++) {
    const t = i / segments;
    const arch = Math.sin(Math.PI * t);
    const bx = lerp(p0x, p1x, t) + lx * chordLen * spec.lateralBow * arch;
    const by = lerp(p0y, p1y, t) + ly * chordLen * spec.lateralBow * arch;
    const bz = lerp(p0z, p1z, t) + lz * chordLen * spec.lateralBow * arch;
    const invLen = (1 + spec.altitude * chordLen * arch) / (Math.hypot(bx, by, bz) || 1);
    arc.px[i] = bx * invLen;
    arc.py[i] = by * invLen;
    arc.pz[i] = bz * invLen;
  }
  return arc;
}

function precomputeHeroArc(): PrecomputedArc {
  const mRest = buildRotationMatrix(REST_LAT_DEG, REST_LON_DEG, AXIAL_ROLL_DEG);
  const p0 = latLonToViewAt(HERO_ARC_ORIGIN[0], HERO_ARC_ORIGIN[1]);
  const [c1x, c1y, c1z] = [p0.x + 0.44, 0.96, 0.74];
  const [c2x, c2y, c2z] = [-0.26, 1.84, -0.18];
  const [p3x, p3y, p3z] = [-0.54, 0.72, -0.44];

  const segments = 76;
  const arc = makeArcShell(segments + 1, 0.15, 5.8);
  for (let i = 0; i <= segments; i++) {
    const t = i / segments;
    const u = 1 - t;
    const [u3, w1, w2, t3] = [u * u * u, 3 * u * u * t, 3 * u * t * t, t * t * t];
    [arc.px[i], arc.py[i], arc.pz[i]] = rotateVec3Transpose(
      mRest,
      u3 * p0.x + w1 * c1x + w2 * c2x + t3 * p3x,
      u3 * p0.y + w1 * c1y + w2 * c2y + t3 * p3y,
      u3 * p0.z + w1 * c1z + w2 * c2z + t3 * p3z
    );
  }
  return arc;
}

// ---------------------------------------------------------------------------
// Pre-rendered radial glow sprites & stars
// ---------------------------------------------------------------------------
function makeRadialSprite(size: number, stops: readonly ColorStop[]): HTMLCanvasElement {
  const c = document.createElement("canvas");
  c.width = c.height = size;
  const g = c.getContext("2d");
  if (!g) return c;
  const half = size * 0.5;
  g.fillStyle = radialGrad(g, half, half, 0, half, half, half, stops);
  g.fillRect(0, 0, size, size);
  return c;
}

function buildGlowSprites(): GlowSprites {
  return {
    worldGlow: makeRadialSprite(16, [
      [0, `rgba(${PALETTE.worldGlow}, 1)`],
      [1, `rgba(${PALETTE.worldGlow}, 0)`],
    ]),
    vnCityGlow: makeRadialSprite(24, [
      [0, `rgba(${PALETTE.vnGlowMid}, 0.78)`],
      [0.48, `rgba(${PALETTE.vnGlowOuter}, 0.34)`],
      [1, `rgba(${PALETTE.vnGlowOuter}, 0)`],
    ]),
    vnSparkGlow: makeRadialSprite(16, [
      [0, `rgba(${PALETTE.vnGlowMid}, 0.28)`],
      [1, `rgba(${PALETTE.vnGlowOuter}, 0)`],
    ]),
    starHalo: makeRadialSprite(24, [
      [0, `rgba(${PALETTE.starTint}, 0.52)`],
      [0.35, `rgba(${PALETTE.starHalo}, 0.16)`],
      [1, `rgba(${PALETTE.starHalo}, 0)`],
    ]),
    arcSpark: makeRadialSprite(24, [
      [0, `rgba(${PALETTE.arcSpark}, 0.95)`],
      [0.35, `rgba(${PALETTE.arcGlow}, 0.45)`],
      [1, `rgba(${PALETTE.arcGlow}, 0)`],
    ]),
  };
}

function buildStars(metrics: LayoutMetrics): Star[] {
  const { width, height, cx, cy, radius } = metrics;
  const rand = mulberry32(20261126);
  const stars: Star[] = [];
  const edgePad = Math.min(20, Math.floor(Math.min(width, height) * 0.04));
  const spanX = Math.max(1, width - edgePad * 2);
  const spanY = Math.max(1, height - edgePad * 2);

  for (let attempts = 0; stars.length < 78 && attempts < 400; attempts++) {
    const x = edgePad + rand() * spanX;
    const y = edgePad + rand() * spanY;
    const dist = Math.hypot(x - cx, y - cy);
    if (dist < radius * 1.03) continue;
    const isHalo = stars.length < 10 && dist > radius * 1.12;
    stars.push({
      x,
      y,
      radius: isHalo ? 1.45 + rand() * 0.85 : 0.55 + rand() * 0.9,
      haloRadius: isHalo ? 8 + rand() * 9 : 0,
      baseAlpha: isHalo ? 0.72 + rand() * 0.24 : 0.24 + rand() * 0.58,
      phase: rand() * TAU,
      speed: isHalo ? 0.45 + rand() * 0.45 : 0.65 + rand() * 1.15,
    });
  }
  return stars;
}

// ---------------------------------------------------------------------------
// Static size-dependent layers (cached once per resize)
// ---------------------------------------------------------------------------
function renderBaseLayer(ctx: CanvasRenderingContext2D, metrics: LayoutMetrics): void {
  const { width, height, cx, cy, radius } = metrics;
  ctx.clearRect(0, 0, width, height);

  ctx.fillStyle = radialGrad(
    ctx,
    cx - radius * 0.2,
    cy - radius * 0.18,
    radius * 0.62,
    cx - radius * 0.11,
    cy - radius * 0.1,
    radius * 1.32,
    [
      [0, PALETTE.atmoOuterInner],
      [0.55, PALETTE.atmoOuterMid],
      [1, PALETTE.atmoOuterEdge],
    ]
  );
  ctx.fillRect(0, 0, width, height);

  ctx.fillStyle = radialGrad(
    ctx,
    cx - radius * 0.11,
    cy - radius * 0.09,
    radius * 0.88,
    cx - radius * 0.07,
    cy - radius * 0.06,
    radius * 1.13,
    [
      [0, PALETTE.atmoRimPeak],
      [0.36, PALETTE.atmoRimSoft],
      [0.72, PALETTE.atmoRimTail],
      [1, PALETTE.atmoRimEdge],
    ]
  );
  ctx.beginPath();
  ctx.arc(cx - radius * 0.07, cy - radius * 0.06, radius * 1.13, 0, TAU);
  ctx.fill();

  // Feather outer aura to alpha 0 before all 4 canvas edges
  ctx.save();
  ctx.globalCompositeOperation = "destination-out";
  const leftPad = Math.max(16, (cx - radius) * 0.94);
  const topPad = Math.max(18, (cy - radius) * 0.48);
  const rightPad = Math.max(24, (width - (cx + radius)) * 0.72);
  const bottomPad = Math.max(20, Math.round(height * 0.05));
  ctx.fillStyle = linearGrad(ctx, 0, 0, leftPad, 0, [
    [0, "rgba(0,0,0,1)"],
    [0.45, "rgba(0,0,0,0.42)"],
    [1, "rgba(0,0,0,0)"],
  ]);
  ctx.fillRect(0, 0, leftPad, height);
  ctx.fillStyle = linearGrad(ctx, 0, 0, 0, topPad, [
    [0, "rgba(0,0,0,1)"],
    [0.45, "rgba(0,0,0,0.38)"],
    [1, "rgba(0,0,0,0)"],
  ]);
  ctx.fillRect(0, 0, width, topPad);
  ctx.fillStyle = linearGrad(ctx, width - rightPad, 0, width, 0, [
    [0, "rgba(0,0,0,0)"],
    [0.55, "rgba(0,0,0,0.38)"],
    [1, "rgba(0,0,0,1)"],
  ]);
  ctx.fillRect(width - rightPad, 0, rightPad, height);
  ctx.fillStyle = linearGrad(ctx, 0, height - bottomPad, 0, height, [
    [0, "rgba(0,0,0,0)"],
    [1, "rgba(0,0,0,1)"],
  ]);
  ctx.fillRect(0, height - bottomPad, width, bottomPad);
  ctx.restore();

  // Clipped ocean hemisphere
  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, radius, 0, TAU);
  ctx.clip();
  const [x0, y0, diam] = [cx - radius, cy - radius, radius * 2];
  ctx.fillStyle = PALETTE.oceanBase;
  ctx.fillRect(x0, y0, diam, diam);
  ctx.fillStyle = radialGrad(
    ctx,
    cx - radius * 0.68,
    cy - radius * 0.56,
    radius * 0.04,
    cx - radius * 0.28,
    cy - radius * 0.22,
    radius * 1.4,
    [
      [0.0, PALETTE.oceanSunCore],
      [0.15, PALETTE.oceanSunMid],
      [0.32, PALETTE.oceanMidBlue],
      [0.5, PALETTE.oceanRoyalNavy],
      [0.7, PALETTE.oceanTwilight],
      [0.88, PALETTE.oceanNight],
      [1.0, PALETTE.oceanDeepShadow],
    ]
  );
  ctx.fillRect(x0, y0, diam, diam);
  ctx.fillStyle = radialGrad(
    ctx,
    cx + radius * 0.56,
    cy - radius * 0.14,
    radius * 0.08,
    cx + radius * 0.46,
    cy - radius * 0.08,
    radius * 0.94,
    [
      [0, PALETTE.oceanNightTint],
      [1, "rgba(6, 14, 48, 0)"],
    ]
  );
  ctx.fillRect(x0, y0, diam, diam);
  ctx.restore();
}

function renderOverlayALayer(ctx: CanvasRenderingContext2D, metrics: LayoutMetrics): void {
  const { cx, cy, radius, dpr, boxPxX, boxPxY, boxPxSize } = metrics;
  const boxCss = boxPxSize / dpr;
  const lcx = cx - boxPxX / dpr;
  const lcy = cy - boxPxY / dpr;
  ctx.clearRect(0, 0, boxCss, boxCss);
  ctx.save();
  ctx.beginPath();
  ctx.arc(lcx, lcy, radius, 0, TAU);
  ctx.clip();

  ctx.fillStyle = linearGrad(
    ctx,
    lcx - radius * 0.44,
    lcy - radius * 0.44,
    lcx + radius * 0.86,
    lcy + radius * 0.8,
    [
      [0.0, "rgba(2, 5, 18, 0)"],
      [0.4, "rgba(2, 5, 18, 0)"],
      [0.72, "rgba(3, 6, 22, 0.42)"],
      [1.0, "rgba(2, 4, 16, 0.72)"],
    ]
  );
  ctx.fillRect(0, 0, boxCss, boxCss);
  ctx.fillStyle = radialGrad(
    ctx,
    lcx - radius * 0.72,
    lcy - radius * 0.6,
    radius * 0.04,
    lcx - radius * 0.6,
    lcy - radius * 0.5,
    radius * 0.82,
    [
      [0.0, "rgba(86, 228, 234, 0.09)"],
      [0.5, "rgba(32, 182, 198, 0.03)"],
      [1.0, "rgba(32, 182, 198, 0)"],
    ]
  );
  ctx.fillRect(0, 0, boxCss, boxCss);
  ctx.restore();
}

function renderOverlayBLayer(ctx: CanvasRenderingContext2D, metrics: LayoutMetrics): void {
  const { cx, cy, radius, dpr, boxPxX, boxPxY, boxPxSize } = metrics;
  const boxCss = boxPxSize / dpr;
  const lcx = cx - boxPxX / dpr;
  const lcy = cy - boxPxY / dpr;
  ctx.clearRect(0, 0, boxCss, boxCss);
  ctx.save();
  ctx.beginPath();
  ctx.arc(lcx, lcy, radius, 0, TAU);
  ctx.clip();

  ctx.fillStyle = radialGrad(
    ctx,
    lcx + radius * 0.24,
    lcy + radius * 0.22,
    radius * 0.76,
    lcx + radius * 0.14,
    lcy + radius * 0.13,
    radius * 1.1,
    [
      [0.0, "rgba(62, 224, 226, 0)"],
      [0.76, "rgba(62, 224, 226, 0)"],
      [0.9, PALETTE.fresnelMid],
      [1.0, PALETTE.fresnelEdge],
    ]
  );
  ctx.fillRect(0, 0, boxCss, boxCss);

  const rim = (
    x0: number,
    y0: number,
    x1: number,
    y1: number,
    lw: number,
    inset: number,
    a0: number,
    a1: number,
    stops: readonly ColorStop[]
  ) => {
    ctx.lineWidth = lw;
    ctx.strokeStyle = linearGrad(ctx, x0, y0, x1, y1, stops);
    ctx.beginPath();
    ctx.arc(lcx, lcy, radius - lw * inset, a0, a1);
    ctx.stroke();
  };

  // Sunlit atmospheric rim along left & top limb
  const [sx0, sy0, sx1, sy1] = [
    lcx - radius * 0.92,
    lcy + radius * 0.42,
    lcx + radius * 0.34,
    lcy - radius * 0.96,
  ];
  rim(sx0, sy0, sx1, sy1, Math.max(6.5, radius * 0.04), 0.38, Math.PI * 0.78, Math.PI * 1.58, [
    [0.0, "rgba(86, 236, 238, 0)"],
    [0.28, PALETTE.sunRimSoft],
    [0.58, PALETTE.sunRimSoft],
    [1.0, "rgba(86, 236, 238, 0)"],
  ]);
  rim(sx0, sy0, sx1, sy1, Math.max(2.0, radius * 0.013), 0.5, Math.PI * 0.8, Math.PI * 1.56, [
    [0.0, "rgba(166, 250, 248, 0)"],
    [0.26, PALETTE.sunRimPeak],
    [0.62, PALETTE.sunRimPeak],
    [1.0, "rgba(166, 250, 248, 0)"],
  ]);

  // Warm grazing twilight glint along upper-right limb
  const [gx0, gy0, gx1] = [lcx + radius * 0.18, lcy - radius * 0.96, lcx + radius * 0.94];
  rim(
    gx0,
    gy0,
    gx1,
    lcy - radius * 0.08,
    Math.max(4.5, radius * 0.032),
    0.35,
    -Math.PI * 0.42,
    -Math.PI * 0.02,
    [
      [0, "rgba(255, 172, 82, 0)"],
      [0.36, PALETTE.twilightRimWarmHalo],
      [0.75, PALETTE.twilightRimIndigo],
      [1, "rgba(108, 138, 232, 0)"],
    ]
  );
  rim(
    gx0,
    gy0,
    gx1,
    lcy - radius * 0.12,
    Math.max(1.8, radius * 0.011),
    0.5,
    -Math.PI * 0.4,
    -Math.PI * 0.06,
    [
      [0, "rgba(255, 214, 146, 0)"],
      [0.35, PALETTE.twilightRimGold],
      [0.68, PALETTE.twilightRimIndigo],
      [1, "rgba(108, 138, 232, 0)"],
    ]
  );
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Single device-resolution landmass & Pass C renderer (used at rest and moving)
// ---------------------------------------------------------------------------
function drawLandmass(
  ctx: CanvasRenderingContext2D,
  land: PrecomputedLand,
  mat: Float32Array,
  metrics: LayoutMetrics,
  moving: boolean
): void {
  const { cx, cy, radius, dpr } = metrics;
  const cxPx = cx * dpr;
  const cyPx = cy * dpr;
  const radiusPx = radius * dpr;
  const scaleFactor = Math.max(0.68, Math.min(1.1, radius / 292));
  const dprDotBoost = dpr < 1.5 ? 0.14 : 0;
  const dprBaseBoost = dpr < 1.5 ? 0.22 : 0;
  const aScalePx = scaleFactor * dpr;
  const bScalePx = scaleFactor * PASS_B_RECT_SCALE * dpr;
  const [m00, m01, m02, m10, m11, m12, m20, m21, m22] = mat;

  // Pass A: full-resolution emerald-teal base (arc) + record visible cells for Pass B
  const {
    aCount,
    aPx,
    aPy,
    aPz,
    aBiome,
    aSize,
    aBuckets,
    visIdx,
    visVz,
    visDl,
    visLimbB,
    visDayPowB,
  } = land;
  aBuckets.heads.fill(-1);
  const aAlphaRangeInv = 1 / (PASS_A_ALPHA_MAX - 0.04);
  let visCount = 0;

  for (let i = 0; i < aCount; i++) {
    const px = aPx[i];
    const py = aPy[i];
    const pz = aPz[i];
    const vz = m20 * px + m21 * py + m22 * pz;
    if (vz <= 0.04) continue;

    const vx = m00 * px + m01 * py + m02 * pz;
    const vy = m10 * px + m11 * py + m12 * pz;
    let dl = (vx * LIGHT_X + vy * LIGHT_Y + vz * LIGHT_Z + 0.32) * INV_LIGHT_SPAN;
    if (dl <= 0) dl = 0;
    else if (dl >= 1) dl = 1;
    else dl = dl * dl * (3 - 2 * dl);

    const biome = aBiome[i];
    const vzIdx = vz >= 1 ? 255 : (vz * 255) | 0;
    const dlIdx = (dl * 255) | 0;
    visIdx[visCount] = i;
    visVz[visCount] = vz;
    visDl[visCount] = dl;
    visLimbB[visCount] = POW_048[vzIdx];
    visDayPowB[visCount] = POW_065[dlIdx];
    visCount++;

    let shade = dl * (0.76 + 0.32 * biome);
    if (shade > 1) shade = 1;
    let sb = (shade * PASS_A_SHADE_BINS) | 0;
    if (sb >= PASS_A_SHADE_BINS) sb = PASS_A_SHADE_BINS - 1;

    const alpha =
      (0.12 + 0.3 * POW_078[dlIdx]) * POW_046[vzIdx] * (0.8 + 0.32 * biome) * PASS_A_ALPHA_COMP;
    let ab = ((alpha - 0.04) * aAlphaRangeInv * PASS_A_ALPHA_BINS) | 0;
    if (ab < 0) ab = 0;
    else if (ab >= PASS_A_ALPHA_BINS) ab = PASS_A_ALPHA_BINS - 1;

    let r = (3.0 + 0.9 * aSize[i] + dprBaseBoost) * aScalePx * (0.58 + 0.42 * vz);
    const maxR = vz * vz * radiusPx * 0.48;
    if (r > maxR) r = maxR;
    if (r < 0.6) continue;

    const bucket = sb * PASS_A_ALPHA_BINS + ab;
    aBuckets.x[i] = cxPx + vx * radiusPx;
    aBuckets.y[i] = cyPx - vy * radiusPx;
    aBuckets.s[i] = r;
    aBuckets.next[i] = aBuckets.heads[bucket];
    aBuckets.heads[bucket] = i;
  }

  for (let b = 0; b < PASS_A_BUCKET_COUNT; b++) {
    let idx = aBuckets.heads[b];
    if (idx === -1) continue;
    ctx.fillStyle = PASS_A_STYLES[b];
    ctx.beginPath();
    while (idx !== -1) {
      const x = aBuckets.x[idx];
      const y = aBuckets.y[idx];
      const r = aBuckets.s[idx];
      ctx.moveTo(x + r, y);
      ctx.arc(x, y, r, 0, TAU);
      idx = aBuckets.next[idx];
    }
    ctx.fill();
  }

  // Pass B: dense device-res stipple (grouped rect), reusing Pass A cell visibility & lighting
  const { bPx, bPy, bPz, bTerrain, bSize, bBuckets } = land;
  bBuckets.heads.fill(-1);
  const bAlphaRangeInv = 1 / (0.96 - 0.06);
  const minZ = moving ? MOTION_PASS_B_MIN_Z : 0.04;
  const minAlpha = moving ? MOTION_PASS_B_MIN_ALPHA : 0;

  for (let vi = 0; vi < visCount; vi++) {
    const vz = visVz[vi];
    if (vz <= minZ) continue;
    const dl = visDl[vi];
    const baseAlpha = (0.22 + 0.72 * visDayPowB[vi]) * visLimbB[vi];
    if (baseAlpha * 1.02 < minAlpha) continue;

    const base = visIdx[vi] * 3;
    const depthScale = bScalePx * (0.78 + 0.22 * vz);
    const maxSide = vz * vz * radiusPx * 0.7;

    for (let s = 0; s < 3; s++) {
      const i = base + s;
      const terrain = bTerrain[i];
      const alpha = baseAlpha * (0.52 + 0.5 * terrain);
      if (alpha < minAlpha) continue;

      const px = bPx[i];
      const py = bPy[i];
      const pz = bPz[i];
      const vx = m00 * px + m01 * py + m02 * pz;
      const vy = m10 * px + m11 * py + m12 * pz;

      let shade = dl * (0.72 + 0.46 * terrain);
      if (shade > 1) shade = 1;
      let sb = (shade * PASS_B_SHADE_BINS) | 0;
      if (sb >= PASS_B_SHADE_BINS) sb = PASS_B_SHADE_BINS - 1;

      let ab = ((alpha - 0.06) * bAlphaRangeInv * PASS_B_ALPHA_BINS) | 0;
      if (ab < 0) ab = 0;
      else if (ab >= PASS_B_ALPHA_BINS) ab = PASS_B_ALPHA_BINS - 1;

      let side = (0.46 + 0.48 * bSize[i] + dprDotBoost) * depthScale;
      if (side > maxSide) side = maxSide;
      const sInt = side < 1.2 ? 1 : (side + 0.5) | 0;
      const half = sInt >> 1;

      const bucket = sb * PASS_B_ALPHA_BINS + ab;
      bBuckets.x[i] = ((cxPx + vx * radiusPx) | 0) - half;
      bBuckets.y[i] = ((cyPx - vy * radiusPx) | 0) - half;
      bBuckets.s[i] = sInt;
      bBuckets.next[i] = bBuckets.heads[bucket];
      bBuckets.heads[bucket] = i;
    }
  }
  flushRectBuckets(ctx, bBuckets, PASS_B_STYLES);
}

function drawSparkGroup(
  ctx: CanvasRenderingContext2D,
  sparks: PrecomputedSparks,
  mat: Float32Array,
  cxPx: number,
  cyPx: number,
  radiusPx: number,
  scalePx: number,
  glowSprite: HTMLCanvasElement,
  coreStyles: readonly string[],
  glowAlphaMul: number,
  coreAlphaMul: number,
  minZ: number,
  applyLimb: boolean
): void {
  const [m00, m01, m02, m10, m11, m12, m20, m21, m22] = mat;
  const { count, px, py, pz, baseIntensity, glowFactor, coreFactor, glow, core } = sparks;
  glow.heads.fill(-1);
  core.heads.fill(-1);

  for (let i = 0; i < count; i++) {
    const x = px[i];
    const y = py[i];
    const z = pz[i];
    const vz = m20 * x + m21 * y + m22 * z;
    if (vz <= minZ) continue;

    const sx = cxPx + (m00 * x + m01 * y + m02 * z) * radiusPx;
    const sy = cyPx - (m10 * x + m11 * y + m12 * z) * radiusPx;
    const intensity = baseIntensity[i] * (applyLimb ? POW_050[vz >= 1 ? 255 : (vz * 255) | 0] : 1);
    const rimLimit = vz * vz * radiusPx * 0.45;

    const glowAlpha = glowAlphaMul * intensity;
    if (glowAlpha > 0.13) {
      const glowR = Math.min(rimLimit, glowFactor[i] * scalePx);
      if (glowR >= 1) {
        let gb = (((glowAlpha - 0.13) / 0.25) * SPARK_GLOW_BINS) | 0;
        if (gb < 0) gb = 0;
        else if (gb >= SPARK_GLOW_BINS) gb = SPARK_GLOW_BINS - 1;
        glow.x[i] = sx - glowR;
        glow.y[i] = sy - glowR;
        glow.s[i] = glowR * 2;
        glow.next[i] = glow.heads[gb];
        glow.heads[gb] = i;
      }
    }

    const coreAlpha = coreAlphaMul * intensity;
    if (coreAlpha > 0.05) {
      let ab = (coreAlpha * CORE_ALPHA_BINS) | 0;
      if (ab >= CORE_ALPHA_BINS) ab = CORE_ALPHA_BINS - 1;
      const side = Math.max(1, (coreFactor[i] * scalePx * 1.85 + 0.5) | 0);
      const half = side >> 1;
      core.x[i] = (sx | 0) - half;
      core.y[i] = (sy | 0) - half;
      core.s[i] = side;
      core.next[i] = core.heads[ab];
      core.heads[ab] = i;
    }
  }

  for (let gb = 0; gb < SPARK_GLOW_BINS; gb++) {
    let idx = glow.heads[gb];
    if (idx === -1) continue;
    ctx.globalAlpha = SPARK_GLOW_ALPHAS[gb];
    while (idx !== -1) {
      const d = glow.s[idx];
      ctx.drawImage(glowSprite, glow.x[idx], glow.y[idx], d, d);
      idx = glow.next[idx];
    }
  }
  ctx.globalAlpha = 1;
  flushRectBuckets(ctx, core, coreStyles);
}

// ---------------------------------------------------------------------------
// Angle-dependent city & arc projection + dynamic frame rendering
// ---------------------------------------------------------------------------
function projectCitiesInPlace(
  cities: CityEntry[],
  mat: Float32Array,
  metrics: LayoutMetrics
): void {
  const { cx, cy, radius } = metrics;
  const [m00, m01, m02, m10, m11, m12, m20, m21, m22] = mat;
  for (let i = 0; i < cities.length; i++) {
    const c = cities[i];
    const vz = m20 * c.px + m21 * c.py + m22 * c.pz;
    if (vz <= 0.04) {
      c.visible = false;
      continue;
    }
    c.visible = true;
    c.x = cx + (m00 * c.px + m01 * c.py + m02 * c.pz) * radius;
    c.y = cy - (m10 * c.px + m11 * c.py + m12 * c.pz) * radius;
    c.z = vz;
    c.limb = POW_042[vz >= 1 ? 255 : (vz * 255) | 0];
  }
}

function projectArcInPlace(arc: PrecomputedArc, mat: Float32Array, metrics: LayoutMetrics): void {
  const { cx, cy, radius } = metrics;
  const [m00, m01, m02, m10, m11, m12, m20, m21, m22] = mat;
  let anyVisible = false;
  let lastVz = -1;
  for (let i = 0; i < arc.pointCount; i++) {
    const [x, y, z] = [arc.px[i], arc.py[i], arc.pz[i]];
    const vx = m00 * x + m01 * y + m02 * z;
    const vy = m10 * x + m11 * y + m12 * z;
    const vz = m20 * x + m21 * y + m22 * z;
    const visible = vz >= 0 || vx * vx + vy * vy >= 0.995;
    const pt = arc.points[i];
    pt.x = cx + vx * radius;
    pt.y = cy - vy * radius;
    pt.visible = visible;
    if (visible) anyVisible = true;
    lastVz = vz;
  }
  arc.anyVisible = anyVisible;
  arc.endVz = lastVz;
}

function drawStars(
  ctx: CanvasRenderingContext2D,
  stars: readonly Star[],
  starHaloSprite: HTMLCanvasElement,
  timeSec: number,
  reducedMotion: boolean
): void {
  for (let i = 0; i < stars.length; i++) {
    const s = stars[i];
    const alpha =
      s.baseAlpha * (reducedMotion ? 0.85 : 0.62 + 0.38 * Math.sin(timeSec * s.speed + s.phase));
    if (s.haloRadius > 0) {
      ctx.globalAlpha = alpha > 1 ? 1 : alpha;
      const r = s.haloRadius;
      ctx.drawImage(starHaloSprite, s.x - r, s.y - r, r * 2, r * 2);
      ctx.globalAlpha = 1;
    }
    ctx.fillStyle = `rgba(${PALETTE.starTint}, ${clamp01(alpha).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(s.x, s.y, s.radius, 0, TAU);
    ctx.fill();
  }
}

function drawCities(
  ctx: CanvasRenderingContext2D,
  cities: readonly CityEntry[],
  glowSprite: HTMLCanvasElement,
  coreRgb: string,
  radius: number,
  timeSec: number,
  reducedMotion: boolean,
  isVietnam: boolean
): void {
  const scale = Math.max(0.62, Math.min(1.08, radius / 292));
  const glowMul = isVietnam ? 1.0 : 0.52;
  const coreMul = isVietnam ? 1.0 : 0.92;
  const [glowBase, glowWeight] = isVietnam ? [2.2, 2.2] : [2.1, 2.0];
  const [coreBase, coreWeight] = isVietnam ? [0.72, 0.56] : [0.68, 0.5];
  const [sStatic, sBase, sAmp] = isVietnam ? [0.96, 0.88, 0.12] : [0.92, 0.82, 0.18];

  for (let i = 0; i < cities.length; i++) {
    const c = cities[i];
    if (!c.visible) continue;
    const shimmer = reducedMotion ? sStatic : sBase + sAmp * Math.sin(timeSec * c.speed + c.phase);
    const strength = c.weight * c.limb * shimmer;
    const rimCap = c.z * c.z * radius * 0.45;
    const glowR = Math.min(rimCap, (glowBase + glowWeight * c.weight) * scale);
    if (glowR >= 0.7 && glowMul * strength > 0.03) {
      ctx.globalAlpha = clamp01(glowMul * strength);
      ctx.drawImage(glowSprite, c.x - glowR, c.y - glowR, glowR * 2, glowR * 2);
      ctx.globalAlpha = 1;
    }
    ctx.fillStyle = `rgba(${coreRgb}, ${clamp01(coreMul * strength).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(c.x, c.y, (coreBase + coreWeight * c.weight) * scale, 0, TAU);
    ctx.fill();
  }
}

function strokeArcSlice(
  ctx: CanvasRenderingContext2D,
  points: readonly SampledArcPoint[],
  startFrac: number,
  endFrac: number,
  width: number,
  strokeStyle: string | CanvasGradient
): SampledArcPoint | null {
  const n = points.length - 1;
  if (n < 1 || endFrac <= startFrac) return null;
  const startIdx = clamp01(startFrac) * n;
  const endIdx = clamp01(endFrac) * n;
  const i0 = Math.floor(startIdx);
  const i1 = Math.min(n, Math.ceil(endIdx));

  const sampleAt = (idxFloat: number): SampledArcPoint => {
    const lo = Math.floor(idxFloat);
    const hi = Math.min(n, lo + 1);
    const f = idxFloat - lo;
    const a = points[lo];
    const b = points[hi];
    return { x: lerp(a.x, b.x, f), y: lerp(a.y, b.y, f), visible: a.visible || b.visible };
  };

  ctx.lineWidth = width;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.strokeStyle = strokeStyle;
  ctx.beginPath();

  let penDown = false;
  let lastPoint: SampledArcPoint | null = null;
  for (let i = i0; i <= i1; i++) {
    const pt =
      i === i0 && startIdx > i0
        ? sampleAt(startIdx)
        : i === i1 && endIdx < i1
          ? sampleAt(endIdx)
          : points[i];
    if (!pt.visible) {
      penDown = false;
      continue;
    }
    if (!penDown) {
      ctx.moveTo(pt.x, pt.y);
      penDown = true;
    } else {
      ctx.lineTo(pt.x, pt.y);
    }
    lastPoint = pt;
  }
  if (lastPoint) ctx.stroke();
  return lastPoint;
}

function drawArcSpark(
  ctx: CanvasRenderingContext2D,
  arcSparkSprite: HTMLCanvasElement,
  x: number,
  y: number,
  radius: number,
  alpha: number
): void {
  const haloR = radius * 2.8;
  ctx.globalAlpha = clamp01(alpha);
  ctx.drawImage(arcSparkSprite, x - haloR, y - haloR, haloR * 2, haloR * 2);
  ctx.fillStyle = `rgb(${PALETTE.arcSpark})`;
  ctx.beginPath();
  ctx.arc(x, y, radius * 0.72, 0, TAU);
  ctx.fill();
  ctx.globalAlpha = 1;
}

function cometWindow(
  cycle: number,
  split: number,
  end: number,
  trail: number
): { head: number; tail: number; fade: number } | null {
  if (cycle >= end) return null;
  if (cycle < split) {
    const p = easeInOutCubic(cycle / split);
    return { head: p, tail: Math.max(0, p - trail), fade: Math.min(1, cycle / 0.08) };
  }
  const p = (cycle - split) / (end - split);
  return { head: 1, tail: lerp(1 - trail, 1, easeInOutCubic(p)), fade: 1 - p * p };
}

/**
 * Glowing destination beacon at the hero arc endpoint when rotated onto the front hemisphere.
 * At the resting Vietnam angle, `endVz ≈ -0.44 <= 0.04` so this draws nothing.
 */
function drawHeroLandingBeacon(
  ctx: CanvasRenderingContext2D,
  heroArc: PrecomputedArc,
  cityGlowSprite: HTMLCanvasElement,
  scale: number,
  radius: number,
  timeSec: number,
  reducedMotion: boolean
): void {
  const vz = heroArc.endVz;
  if (vz <= 0.04) return;

  const endPt = heroArc.points[heroArc.pointCount - 1];
  const limb = POW_042[vz >= 1 ? 255 : (vz * 255) | 0];
  let pulse = 0.65;
  if (!reducedMotion) {
    const cycle = (timeSec / heroArc.duration + heroArc.phase) % 1;
    if (cycle >= 0.48 && cycle < 0.6) pulse = easeInOutCubic((cycle - 0.48) / 0.12);
    else if (cycle >= 0.6 && cycle < 0.88) {
      const p = (cycle - 0.6) / 0.28;
      pulse = 1 - p * p;
    } else pulse = 0;
  }

  const rimCap = vz * vz * radius * 0.45;
  const glowR = Math.min(rimCap, (4.8 + 3.2 * pulse) * scale);
  const coreR = Math.min(rimCap, (1.1 + 0.55 * pulse) * scale);
  if (glowR >= 0.8) {
    ctx.globalAlpha = clamp01(limb * (0.58 + 0.42 * pulse));
    ctx.drawImage(cityGlowSprite, endPt.x - glowR, endPt.y - glowR, glowR * 2, glowR * 2);
  }
  ctx.globalAlpha = clamp01(0.72 + 0.28 * pulse) * limb;
  ctx.fillStyle = `rgb(${PALETTE.vnCore})`;
  ctx.beginPath();
  ctx.arc(endPt.x, endPt.y, coreR, 0, TAU);
  ctx.fill();
  ctx.globalAlpha = 1;
}

function drawArcs(
  ctx: CanvasRenderingContext2D,
  arcs: readonly PrecomputedArc[],
  heroArc: PrecomputedArc,
  sprites: GlowSprites,
  metrics: LayoutMetrics,
  timeSec: number,
  reducedMotion: boolean
): void {
  const scale = Math.max(0.68, Math.min(1.08, metrics.radius / 292));
  // Draw hero landing beacon underneath lighter strokes (matching HCMC origin layering)
  drawHeroLandingBeacon(
    ctx,
    heroArc,
    sprites.vnCityGlow,
    scale,
    metrics.radius,
    timeSec,
    reducedMotion
  );

  ctx.save();
  ctx.globalCompositeOperation = "lighter";

  for (let i = 0; i < arcs.length; i++) {
    const arc = arcs[i];
    if (!arc.anyVisible) continue;
    const pts = arc.points;
    const pStart = pts[0];
    const pEnd = pts[pts.length - 1];

    if (reducedMotion) {
      strokeArcSlice(
        ctx,
        pts,
        0,
        1,
        2.0 * scale,
        linearGrad(ctx, pStart.x, pStart.y, pEnd.x, pEnd.y, [
          [0, `rgba(${PALETTE.arcGlow}, 0.04)`],
          [0.5, `rgba(${PALETTE.arcGlow}, 0.16)`],
          [1, `rgba(${PALETTE.arcGlow}, 0.05)`],
        ])
      );
      strokeArcSlice(
        ctx,
        pts,
        0,
        1,
        0.8 * scale,
        linearGrad(ctx, pStart.x, pStart.y, pEnd.x, pEnd.y, [
          [0, `rgba(${PALETTE.arcCore}, 0.14)`],
          [0.5, `rgba(${PALETTE.arcCore}, 0.48)`],
          [1, `rgba(${PALETTE.arcCore}, 0.18)`],
        ])
      );
      continue;
    }

    if (i < 8) strokeArcSlice(ctx, pts, 0, 1, 0.65 * scale, `rgba(${PALETTE.arcCore}, 0.11)`);
    const cycle = (timeSec / arc.duration + arc.phase) % 1;
    const win = cometWindow(cycle, 0.52, 0.86, 0.68);
    if (!win || win.head - win.tail < 0.015 || win.fade <= 0.01) continue;

    const tailPt = pts[Math.floor(win.tail * (pts.length - 1))];
    const headPt = pts[Math.min(pts.length - 1, Math.ceil(win.head * (pts.length - 1)))];
    strokeArcSlice(
      ctx,
      pts,
      win.tail,
      win.head,
      2.3 * scale,
      linearGrad(ctx, tailPt.x, tailPt.y, headPt.x, headPt.y, [
        [0, `rgba(${PALETTE.arcGlow}, 0)`],
        [0.65, `rgba(${PALETTE.arcGlow}, ${(0.16 * win.fade).toFixed(3)})`],
        [1, `rgba(${PALETTE.arcSpark}, ${(0.3 * win.fade).toFixed(3)})`],
      ])
    );
    const tip = strokeArcSlice(
      ctx,
      pts,
      win.tail,
      win.head,
      0.9 * scale,
      linearGrad(ctx, tailPt.x, tailPt.y, headPt.x, headPt.y, [
        [0, `rgba(${PALETTE.arcCore}, 0)`],
        [0.55, `rgba(${PALETTE.arcCore}, ${(0.54 * win.fade).toFixed(3)})`],
        [1, `rgba(${PALETTE.arcSpark}, ${(0.92 * win.fade).toFixed(3)})`],
      ])
    );
    if (tip && cycle < 0.62) {
      drawArcSpark(ctx, sprites.arcSpark, tip.x, tip.y, 1.2 * scale, win.fade * 0.78);
    }
  }

  if (heroArc.anyVisible) {
    const hPts = heroArc.points;
    const [hStart, hPeak, hEnd] = [
      hPts[0],
      hPts[Math.floor(hPts.length * 0.56)],
      hPts[hPts.length - 1],
    ];
    const [baseGlow, baseCore] = reducedMotion ? [0.26, 0.78] : [0.2, 0.56];

    const strokeHeroBase = (
      rgb: string,
      baseA: number,
      s: readonly [number, number, number, number],
      w: number
    ) =>
      strokeArcSlice(
        ctx,
        hPts,
        0,
        1,
        w * scale,
        linearGrad(ctx, hStart.x, hStart.y, hEnd.x, hEnd.y, [
          [0, `rgba(${rgb}, ${(baseA * s[0]).toFixed(3)})`],
          [0.34, `rgba(${rgb}, ${(baseA * s[1]).toFixed(3)})`],
          [0.86, `rgba(${rgb}, ${(baseA * s[2]).toFixed(3)})`],
          [1, `rgba(${rgb}, ${(baseA * s[3]).toFixed(3)})`],
        ])
      );
    strokeHeroBase(PALETTE.heroBaseGlow, baseGlow, [0.25, 1.0, 0.88, 0.35], 8.2);
    strokeHeroBase(PALETTE.heroBaseCore, baseCore, [0.22, 0.78, 0.72, 0.28], 3.1);
    strokeHeroBase(PALETTE.arcSpark, baseCore, [0.3, 1.0, 0.88, 0.35], 1.35);

    if (!reducedMotion) {
      const cycle = (timeSec / heroArc.duration + heroArc.phase) % 1;
      const win = cometWindow(cycle, 0.6, 0.88, 0.52);
      if (win && win.head - win.tail > 0.02 && win.fade > 0.02) {
        const tailPt = hPts[Math.floor(win.tail * (hPts.length - 1))];
        const headPt = hPts[Math.min(hPts.length - 1, Math.ceil(win.head * (hPts.length - 1)))];
        strokeArcSlice(
          ctx,
          hPts,
          win.tail,
          win.head,
          6.2 * scale,
          linearGrad(ctx, tailPt.x, tailPt.y, headPt.x, headPt.y, [
            [0, `rgba(${PALETTE.arcGlow}, 0)`],
            [0.65, `rgba(${PALETTE.arcGlow}, ${(0.28 * win.fade).toFixed(3)})`],
            [1, `rgba(${PALETTE.arcSpark}, ${(0.56 * win.fade).toFixed(3)})`],
          ])
        );
        const tip = strokeArcSlice(
          ctx,
          hPts,
          win.tail,
          win.head,
          1.8 * scale,
          linearGrad(ctx, tailPt.x, tailPt.y, headPt.x, headPt.y, [
            [0, `rgba(${PALETTE.arcSpark}, 0)`],
            [0.55, `rgba(${PALETTE.arcSpark}, ${(0.76 * win.fade).toFixed(3)})`],
            [1, `rgba(${PALETTE.arcSpark}, ${win.fade.toFixed(3)})`],
          ])
        );
        if (tip && tip.visible && cycle < 0.68) {
          drawArcSpark(ctx, sprites.arcSpark, tip.x, tip.y, 2.4 * scale, win.fade);
        }
      }
    } else if (hPeak.visible) {
      drawArcSpark(ctx, sprites.arcSpark, hPeak.x, hPeak.y, 2.0 * scale, 0.72);
    }
  }
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Public controller
// ---------------------------------------------------------------------------
export function createGlobeRenderer(options: CreateGlobeOptions): GlobeController {
  const { canvas, landDots, onFirstRender } = options;
  const ctx = canvas.getContext("2d");
  if (!ctx) {
    return { setVisible() {}, setReducedMotion() {}, resize() {}, destroy() {} };
  }

  const land = precomputeLand(landDots);
  const { world: worldSparks, vn: vnSparks } = precomputeSparks();
  const worldCities = precomputeCities(WORLD_CITIES, false);
  const vnCities = precomputeCities(VN_CAPITALS, true);
  const domesticArcs = VN_ARCS.map((spec) => precomputeDomesticArc(spec));
  const heroArc = precomputeHeroArc();
  const sprites = buildGlowSprites();

  const baseCanvas = document.createElement("canvas");
  const baseCtx = baseCanvas.getContext("2d");
  const overlayACanvas = document.createElement("canvas");
  const overlayACtx = overlayACanvas.getContext("2d");
  const overlayBCanvas = document.createElement("canvas");
  const overlayBCtx = overlayBCanvas.getContext("2d");
  const composedCanvas = document.createElement("canvas");
  const composedCtx = composedCanvas.getContext("2d");

  const rotMatrix = new Float32Array(9);
  const motion = createGlobeMotionState();

  let destroyed = false;
  let visible = true;
  let reducedMotion = options.reducedMotion;
  let rafId = 0;
  let firstRendered = false;
  let startTimeMs = 0;
  let lastTickMs = 0;
  let lastWidth = 0;
  let lastHeight = 0;
  let lastDpr = 0;
  let projectionsDirty = true;
  let dragMovedSinceRender = false;
  let globeCacheDirty = true;

  let metrics: LayoutMetrics = {
    width: 0,
    height: 0,
    pxW: 0,
    pxH: 0,
    dpr: 1,
    cx: 0,
    cy: 0,
    radius: 0,
    boxPxX: 0,
    boxPxY: 0,
    boxPxSize: 0,
    featherTopPx: 0,
  };
  let bottomFeatherGradPx: CanvasGradient | null = null;
  let stars: Star[] = [];

  let activePointerId: number | null = null;
  let activePointerType = "";
  let lastClientX = 0;
  let lastClientY = 0;
  let touchOriginX = 0;
  let touchOriginY = 0;
  let touchLock: "none" | "pending" | "horizontal" | "vertical" = "none";
  let savedUserSelect: string | null = null;

  function applyDraggingUi(dragging: boolean): void {
    if (dragging) {
      canvas.style.cursor = "grabbing";
      if (savedUserSelect === null) {
        savedUserSelect = document.body.style.userSelect;
        document.body.style.userSelect = "none";
      }
    } else {
      canvas.style.cursor = "";
      if (savedUserSelect !== null) {
        document.body.style.userSelect = savedUserSelect;
        savedUserSelect = null;
      }
    }
  }

  function updateAngleProjections(): void {
    writeRotationMatrix(rotMatrix, motion.latDeg, motion.lonDeg, AXIAL_ROLL_DEG);
    projectCitiesInPlace(worldCities, rotMatrix, metrics);
    projectCitiesInPlace(vnCities, rotMatrix, metrics);
    for (let i = 0; i < domesticArcs.length; i++)
      projectArcInPlace(domesticArcs[i], rotMatrix, metrics);
    projectArcInPlace(heroArc, rotMatrix, metrics);
  }

  function renderSurface(target: CanvasRenderingContext2D, moving: boolean): void {
    const { pxW, pxH, boxPxX, boxPxY, featherTopPx, cx, cy, radius, dpr } = metrics;
    const [cxPx, cyPx, radiusPx] = [cx * dpr, cy * dpr, radius * dpr];
    const sparkScale = Math.max(0.68, Math.min(1.1, radius / 292)) * dpr;

    target.setTransform(1, 0, 0, 1, 0, 0);
    target.globalCompositeOperation = "copy";
    target.drawImage(baseCanvas, 0, 0);
    target.globalCompositeOperation = "source-over";

    drawLandmass(target, land, rotMatrix, metrics, moving);
    target.drawImage(overlayACanvas, boxPxX, boxPxY);
    drawSparkGroup(
      target,
      worldSparks,
      rotMatrix,
      cxPx,
      cyPx,
      radiusPx,
      sparkScale,
      sprites.worldGlow,
      WORLD_CORE_STYLES,
      0.34,
      0.8,
      0.05,
      true
    );
    drawSparkGroup(
      target,
      vnSparks,
      rotMatrix,
      cxPx,
      cyPx,
      radiusPx,
      sparkScale,
      sprites.vnSparkGlow,
      VN_CORE_STYLES,
      1.0,
      0.55,
      0.1,
      false
    );
    target.drawImage(overlayBCanvas, boxPxX, boxPxY);

    target.globalCompositeOperation = "destination-out";
    target.fillStyle = bottomFeatherGradPx!;
    target.fillRect(0, featherTopPx, pxW, pxH - featherTopPx);
    target.globalCompositeOperation = "source-over";
  }

  function rebuildScene(): boolean {
    const rect = canvas.getBoundingClientRect();
    const width = Math.round(rect.width);
    const height = Math.round(rect.height);
    if (width < 16 || height < 16) return false;

    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    if (width === lastWidth && height === lastHeight && dpr === lastDpr) return true;
    lastWidth = width;
    lastHeight = height;
    lastDpr = dpr;

    const pxW = Math.round(width * dpr);
    const pxH = Math.round(height * dpr);
    canvas.width = baseCanvas.width = composedCanvas.width = pxW;
    canvas.height = baseCanvas.height = composedCanvas.height = pxH;

    const radius = Math.min(width * GLOBE_RADIUS_W_RATIO, height * 0.42);
    const cx = width * GLOBE_CENTER_X_RATIO;
    const cy = height * GLOBE_CENTER_Y_RATIO;
    const boxPxSize = Math.round((Math.ceil(radius * 2) + 4) * dpr);
    const boxPxX = Math.floor(cx * dpr - boxPxSize * 0.5);
    const boxPxY = Math.floor(cy * dpr - boxPxSize * 0.5);
    const featherTopPx = Math.floor((cy + radius * 0.54) * dpr);

    overlayACanvas.width = overlayBCanvas.width = boxPxSize;
    overlayACanvas.height = overlayBCanvas.height = boxPxSize;
    metrics = {
      width,
      height,
      pxW,
      pxH,
      dpr,
      cx,
      cy,
      radius,
      boxPxX,
      boxPxY,
      boxPxSize,
      featherTopPx,
    };

    bottomFeatherGradPx = linearGrad(
      ctx!,
      0,
      (cy + radius * 0.56) * dpr,
      0,
      (cy + radius * 1.12) * dpr,
      [
        [0.0, "rgba(0, 0, 0, 0)"],
        [0.55, "rgba(0, 0, 0, 0.32)"],
        [0.84, "rgba(0, 0, 0, 0.88)"],
        [1.0, "rgba(0, 0, 0, 1)"],
      ]
    );

    if (baseCtx) {
      baseCtx.setTransform(dpr, 0, 0, dpr, 0, 0);
      renderBaseLayer(baseCtx, metrics);
    }
    if (overlayACtx) {
      overlayACtx.setTransform(dpr, 0, 0, dpr, 0, 0);
      renderOverlayALayer(overlayACtx, metrics);
    }
    if (overlayBCtx) {
      overlayBCtx.setTransform(dpr, 0, 0, dpr, 0, 0);
      renderOverlayBLayer(overlayBCtx, metrics);
    }

    stars = buildStars(metrics);
    projectionsDirty = true;
    globeCacheDirty = true;
    return true;
  }

  function renderFrame(nowMs: number): void {
    if (destroyed || metrics.width < 16 || !bottomFeatherGradPx) return;
    if (startTimeMs === 0) startTimeMs = nowMs;

    const dtSec = lastTickMs > 0 ? (nowMs - lastTickMs) / 1000 : 0;
    lastTickMs = nowMs;

    const motionStepped = stepGlobeMotion(motion, nowMs, dtSec, reducedMotion);
    const angleChanged = motionStepped || dragMovedSinceRender;
    dragMovedSinceRender = false;
    if (motionStepped) {
      projectionsDirty = true;
      globeCacheDirty = true;
    }
    if (projectionsDirty) {
      updateAngleProjections();
      projectionsDirty = false;
    }

    const isMoving =
      angleChanged ||
      motion.mode === "dragging" ||
      motion.mode === "inertia" ||
      motion.mode === "returning";
    const movingSurface = !reducedMotion && isMoving;
    const useCache = !reducedMotion && !isMoving;
    const timeSec = reducedMotion ? 0 : (nowMs - startTimeMs) / 1000;

    if (!useCache || !composedCtx) {
      renderSurface(ctx!, movingSurface);
      globeCacheDirty = true;
    } else {
      if (globeCacheDirty) {
        renderSurface(composedCtx, false);
        globeCacheDirty = false;
      }
      ctx!.setTransform(1, 0, 0, 1, 0, 0);
      ctx!.globalCompositeOperation = "copy";
      ctx!.drawImage(composedCanvas, 0, 0);
      ctx!.globalCompositeOperation = "source-over";
    }

    ctx!.setTransform(metrics.dpr, 0, 0, metrics.dpr, 0, 0);
    drawStars(ctx!, stars, sprites.starHalo, timeSec, reducedMotion);
    drawCities(
      ctx!,
      worldCities,
      sprites.worldGlow,
      PALETTE.worldCore,
      metrics.radius,
      timeSec,
      reducedMotion,
      false
    );
    drawCities(
      ctx!,
      vnCities,
      sprites.vnCityGlow,
      PALETTE.vnCore,
      metrics.radius,
      timeSec,
      reducedMotion,
      true
    );
    drawArcs(ctx!, domesticArcs, heroArc, sprites, metrics, timeSec, reducedMotion);

    if (!firstRendered) {
      firstRendered = true;
      onFirstRender?.();
    }
  }

  function stopLoop(): void {
    if (rafId !== 0) {
      cancelAnimationFrame(rafId);
      rafId = 0;
    }
    lastTickMs = 0;
  }

  const shouldAnimate = () => !destroyed && visible && !reducedMotion && !document.hidden;

  function tick(nowMs: number): void {
    rafId = 0;
    if (!shouldAnimate()) return;
    renderFrame(nowMs);
    rafId = requestAnimationFrame(tick);
  }

  function syncPlayback(): void {
    if (destroyed) return;
    if (metrics.width < 16 && !rebuildScene()) return;
    if (shouldAnimate()) {
      if (rafId === 0) {
        lastTickMs = performance.now();
        rafId = requestAnimationFrame(tick);
      }
    } else {
      stopLoop();
      if (reducedMotion || !firstRendered) renderFrame(performance.now());
    }
  }

  function finishPointerDrag(nowMs: number, cancelInertia: boolean): void {
    const pointerId = activePointerId;
    const suppressInertia =
      cancelInertia || (activePointerType === "touch" && touchLock !== "horizontal");
    activePointerId = null;
    activePointerType = "";
    touchLock = "none";
    applyDraggingUi(false);

    if (pointerId !== null) {
      try {
        if (typeof canvas.hasPointerCapture === "function" && canvas.hasPointerCapture(pointerId)) {
          canvas.releasePointerCapture(pointerId);
        }
      } catch {
        // Ignore capture release errors if pointer is already inactive.
      }
    }
    endDrag(motion, nowMs, { reducedMotion, cancelInertia: suppressInertia });
    if (!reducedMotion) syncPlayback();
  }

  function handlePointerDown(event: PointerEvent): void {
    if (destroyed || !event.isPrimary || activePointerId !== null) return;
    if (event.pointerType === "mouse" && event.button !== 0) return;

    const isTouch = event.pointerType === "touch";
    if (!isTouch) {
      event.preventDefault();
      applyDraggingUi(true);
    }
    activePointerId = event.pointerId;
    activePointerType = event.pointerType;
    lastClientX = touchOriginX = event.clientX;
    lastClientY = touchOriginY = event.clientY;
    touchLock = isTouch ? "pending" : "none";

    try {
      canvas.setPointerCapture(event.pointerId);
    } catch {
      // Synthetic pointer events may not support capture; drag still works.
    }
    beginDrag(motion, performance.now());
  }

  function handlePointerMove(event: PointerEvent): void {
    if (destroyed || event.pointerId !== activePointerId) return;
    let dx = event.clientX - lastClientX;
    let dy = event.clientY - lastClientY;
    lastClientX = event.clientX;
    lastClientY = event.clientY;

    const isTouch = activePointerType === "touch";
    if (isTouch) {
      if (touchLock === "vertical") return;
      if (touchLock === "pending") {
        const totalDx = event.clientX - touchOriginX;
        const totalDy = event.clientY - touchOriginY;
        if (Math.hypot(totalDx, totalDy) < 5) return;
        if (Math.abs(totalDy) >= Math.abs(totalDx)) {
          touchLock = "vertical";
          return;
        }
        touchLock = "horizontal";
        dx = totalDx;
        dy = 0;
      }
    }

    const nowMs = performance.now();
    if (moveDrag(motion, dx, dy, metrics.radius, nowMs, isTouch)) {
      projectionsDirty = true;
      dragMovedSinceRender = true;
      globeCacheDirty = true;
      if (reducedMotion) renderFrame(nowMs);
      else syncPlayback();
    }
  }

  const handlePointerUp = (e: PointerEvent) => {
    if (!destroyed && e.pointerId === activePointerId) finishPointerDrag(performance.now(), false);
  };
  const handlePointerCancel = (e: PointerEvent) => {
    if (!destroyed && e.pointerId === activePointerId) finishPointerDrag(performance.now(), true);
  };
  const handleVisibilityChange = () => {
    if (document.hidden && activePointerId !== null) finishPointerDrag(performance.now(), true);
    syncPlayback();
  };

  canvas.addEventListener("pointerdown", handlePointerDown);
  canvas.addEventListener("pointermove", handlePointerMove);
  canvas.addEventListener("pointerup", handlePointerUp);
  canvas.addEventListener("pointercancel", handlePointerCancel);
  canvas.addEventListener("lostpointercapture", handlePointerCancel);
  document.addEventListener("visibilitychange", handleVisibilityChange);

  if (rebuildScene()) {
    renderFrame(performance.now());
    syncPlayback();
  }

  return {
    setVisible(nextVisible: boolean) {
      if (visible === nextVisible) return;
      visible = nextVisible;
      if (!visible && activePointerId !== null) finishPointerDrag(performance.now(), true);
      syncPlayback();
    },
    setReducedMotion(nextReduced: boolean) {
      if (reducedMotion === nextReduced) return;
      reducedMotion = nextReduced;
      syncPlayback();
    },
    resize() {
      if (!destroyed && rebuildScene()) {
        renderFrame(performance.now());
        syncPlayback();
      }
    },
    destroy() {
      destroyed = true;
      stopLoop();
      applyDraggingUi(false);
      activePointerId = null;
      canvas.removeEventListener("pointerdown", handlePointerDown);
      canvas.removeEventListener("pointermove", handlePointerMove);
      canvas.removeEventListener("pointerup", handlePointerUp);
      canvas.removeEventListener("pointercancel", handlePointerCancel);
      canvas.removeEventListener("lostpointercapture", handlePointerCancel);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      for (const c of [baseCanvas, overlayACanvas, overlayBCanvas, composedCanvas]) {
        c.width = 0;
        c.height = 0;
      }
    },
  };
}
