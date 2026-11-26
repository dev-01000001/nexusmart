import {
  HERO_ARC_ORIGIN,
  VN_ARCS,
  VN_CAPITALS,
  WORLD_CITIES,
  type CityPoint,
  type GlobeArcSpec,
} from "./globeData";

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
// Visual palette & projection constants (all Canvas colors live here)
// ---------------------------------------------------------------------------
const DEG = Math.PI / 180;

// Orthographic center: ~14°N, 106°E puts Vietnam near center & slightly above
// (Vietnam spans 8.6°N..23.4°N -> midpoint ~16°N).
const CENTER_LAT_DEG = 13.5;
const CENTER_LON_DEG = 106.0;
const AXIAL_ROLL_DEG = -9.5; // gentle planetary tilt (north slightly top-left)

// Sphere layout inside the container (must mirror PlatformGlobe.module.css fallback)
const GLOBE_CENTER_X_RATIO = 0.46;
const GLOBE_CENTER_Y_RATIO = 0.56;
const GLOBE_RADIUS_W_RATIO = 0.39; // diameter = 78% of container width

// Sun direction in view space (+x right, +y north/up, +z toward viewer):
// Strong upper-left illumination (~10:30) with a soft diagonal terminator toward lower-right.
const LIGHT_RAW: readonly [number, number, number] = [-0.74, 0.56, 0.37];
const LIGHT_LEN = Math.hypot(LIGHT_RAW[0], LIGHT_RAW[1], LIGHT_RAW[2]);
const LIGHT_DIR: readonly [number, number, number] = [
  LIGHT_RAW[0] / LIGHT_LEN,
  LIGHT_RAW[1] / LIGHT_LEN,
  LIGHT_RAW[2] / LIGHT_LEN,
];

const PALETTE = {
  // Ocean spherical shading (sunlit cyan-teal -> deep ocean blue -> moody midnight indigo)
  oceanBase: "#04081c",
  oceanSunCore: "#1ea6ba",
  oceanSunMid: "#117896",
  oceanMidBlue: "#0a456e",
  oceanRoyalNavy: "#072352",
  oceanTwilight: "#061134",
  oceanNight: "#040922",
  oceanDeepShadow: "#030616",
  oceanNightTint: "rgba(16, 36, 98, 0.24)",

  // Outer atmosphere & directional inner Fresnel scatter (strong on sunlit left & top rim)
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

  // Textured emerald/teal landmass RGB endpoints (dark forest-teal -> rich emerald -> luminous sunlit seafoam-teal)
  landDarkRgb: [12, 46, 44] as const,
  landMidRgb: [28, 118, 90] as const,
  landLitRgb: [62, 182, 138] as const,

  // Urban lights (warm amber world clusters vs brightest crisp golden-amber Vietnam capitals)
  worldGlow: "255, 146, 42",
  worldCore: "255, 218, 134",
  vnGlowOuter: "255, 136, 28",
  vnGlowMid: "255, 200, 86",
  vnCore: "255, 244, 198",

  // Luminous arcs & hero orbital arc
  arcGlow: "198, 246, 252",
  arcCore: "255, 252, 242",
  arcSpark: "255, 255, 255",
  heroBaseGlow: "168, 240, 248",
  heroBaseCore: "242, 253, 255",

  // Background stars
  starTint: "236, 252, 252",
  starHalo: "196, 242, 244",
} as const;

// ---------------------------------------------------------------------------
// Math & deterministic PRNG helpers
// ---------------------------------------------------------------------------
const SIN_LAT0 = Math.sin(CENTER_LAT_DEG * DEG);
const COS_LAT0 = Math.cos(CENTER_LAT_DEG * DEG);
const LON0_RAD = CENTER_LON_DEG * DEG;
const SIN_ROLL = Math.sin(AXIAL_ROLL_DEG * DEG);
const COS_ROLL = Math.cos(AXIAL_ROLL_DEG * DEG);

interface Vec3 {
  x: number;
  y: number;
  z: number;
}

/**
 * Converts (lat, lon) in degrees to a 3D unit vector in view space:
 * +x right, +y up (north), +z out of screen toward viewer.
 */
function latLonToView(latDeg: number, lonDeg: number): Vec3 {
  const lat = latDeg * DEG;
  const dLon = lonDeg * DEG - LON0_RAD;
  const cosLat = Math.cos(lat);
  const sinLat = Math.sin(lat);
  const cosDLon = Math.cos(dLon);
  const sinDLon = Math.sin(dLon);

  const rx = cosLat * sinDLon;
  const ry = COS_LAT0 * sinLat - SIN_LAT0 * cosLat * cosDLon;
  const rz = SIN_LAT0 * sinLat + COS_LAT0 * cosLat * cosDLon;

  // Apply subtle roll in the view plane
  return {
    x: rx * COS_ROLL - ry * SIN_ROLL,
    y: rx * SIN_ROLL + ry * COS_ROLL,
    z: rz,
  };
}

function hash2(a: number, b: number): number {
  const s = Math.sin(a * 127.1 + b * 311.7) * 43758.5453123;
  return s - Math.floor(s);
}

/** Smooth 2D value noise in [0, 1] for organic continental brightness & stipple variation. */
function valueNoise2D(x: number, y: number): number {
  const ix = Math.floor(x);
  const iy = Math.floor(y);
  const fx = x - ix;
  const fy = y - iy;
  const ux = fx * fx * (3 - 2 * fx);
  const uy = fy * fy * (3 - 2 * fy);
  const a = hash2(ix, iy);
  const b = hash2(ix + 1, iy);
  const c = hash2(ix, iy + 1);
  const d = hash2(ix + 1, iy + 1);
  return lerp(lerp(a, b, ux), lerp(c, d, ux), uy);
}

/** Deterministic Box-Muller standard normal pair from PRNG. */
function gaussianPair(rand: () => number): readonly [number, number] {
  const u1 = Math.max(1e-6, rand());
  const u2 = rand();
  const mag = Math.sqrt(-2 * Math.log(u1));
  return [mag * Math.cos(2 * Math.PI * u2), mag * Math.sin(2 * Math.PI * u2)];
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

function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

function clamp01(v: number): number {
  return v < 0 ? 0 : v > 1 ? 1 : v;
}

function smoothstep(edge0: number, edge1: number, x: number): number {
  const t = clamp01((x - edge0) / (edge1 - edge0));
  return t * t * (3 - 2 * t);
}

function easeInOutCubic(t: number): number {
  return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
}

// ---------------------------------------------------------------------------
// Precomputed scene structures
// ---------------------------------------------------------------------------
interface ProjectedCity {
  x: number;
  y: number;
  limb: number;
  weight: number;
  phase: number;
  speed: number;
  isVietnam: boolean;
}

interface SampledArcPoint {
  x: number;
  y: number;
  visible: boolean;
}

interface PreparedArc {
  points: SampledArcPoint[];
  phase: number;
  duration: number;
  isHero: boolean;
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
  dpr: number;
  cx: number;
  cy: number;
  radius: number;
}

function buildStars(metrics: LayoutMetrics): Star[] {
  const { width, height, cx, cy, radius } = metrics;
  const rand = mulberry32(20261126);
  const stars: Star[] = [];
  const targetCount = 78;
  const minGlobeDist = radius * 1.03;

  let attempts = 0;
  const edgePad = Math.min(20, Math.floor(Math.min(width, height) * 0.04));
  const spanX = Math.max(1, width - edgePad * 2);
  const spanY = Math.max(1, height - edgePad * 2);
  while (stars.length < targetCount && attempts < 400) {
    attempts++;
    const x = edgePad + rand() * spanX;
    const y = edgePad + rand() * spanY;
    const dist = Math.hypot(x - cx, y - cy);
    if (dist < minGlobeDist) continue;

    // ~10 larger soft-halo stars scattered in open space like the Shopify reference
    const isHaloStar = stars.length < 10 && dist > radius * 1.12;
    stars.push({
      x,
      y,
      radius: isHaloStar ? 1.45 + rand() * 0.85 : 0.55 + rand() * 0.9,
      haloRadius: isHaloStar ? 8 + rand() * 9 : 0,
      baseAlpha: isHaloStar ? 0.72 + rand() * 0.24 : 0.24 + rand() * 0.58,
      phase: rand() * Math.PI * 2,
      speed: isHaloStar ? 0.45 + rand() * 0.45 : 0.65 + rand() * 1.15,
    });
  }
  return stars;
}

function projectCities(
  cities: readonly CityPoint[],
  isVietnam: boolean,
  metrics: LayoutMetrics
): ProjectedCity[] {
  const out: ProjectedCity[] = [];
  const { cx, cy, radius } = metrics;

  for (let i = 0; i < cities.length; i++) {
    const [lat, lon, weight] = cities[i];
    const v = latLonToView(lat, lon);
    if (v.z <= 0.04) continue;

    out.push({
      x: cx + v.x * radius,
      y: cy - v.y * radius,
      limb: Math.pow(v.z, 0.42),
      weight,
      phase: hash2(lat, lon) * Math.PI * 2,
      speed: isVietnam ? 1.1 + hash2(lon, lat) * 0.9 : 0.8 + hash2(lon, lat) * 1.2,
      isVietnam,
    });
  }
  return out;
}

function sampleDomesticArc(spec: GlobeArcSpec, metrics: LayoutMetrics): PreparedArc {
  const { cx, cy, radius } = metrics;
  const p0 = latLonToView(spec.from[0], spec.from[1]);
  const p1 = latLonToView(spec.to[0], spec.to[1]);

  const dx = p1.x - p0.x;
  const dy = p1.y - p0.y;
  const dz = p1.z - p0.z;
  const chordLen = Math.hypot(dx, dy, dz);

  // Midpoint radial vector
  const mx = p0.x + p1.x;
  const my = p0.y + p1.y;
  const mz = p0.z + p1.z;
  const mLen = Math.hypot(mx, my, mz) || 1;
  const rx = mx / mLen;
  const ry = my / mLen;
  const rz = mz / mLen;

  // Tangent-plane lateral axis = normalize(chord x radial)
  let lx = dy * rz - dz * ry;
  let ly = dz * rx - dx * rz;
  let lz = dx * ry - dy * rx;
  const lLen = Math.hypot(lx, ly, lz) || 1;
  lx /= lLen;
  ly /= lLen;
  lz /= lLen;

  const segments = 28;
  const points: SampledArcPoint[] = [];

  for (let i = 0; i <= segments; i++) {
    const t = i / segments;
    const arch = Math.sin(Math.PI * t);

    // Base point along chord + gentle lateral bow, normalized back to sphere
    const bx = lerp(p0.x, p1.x, t) + lx * chordLen * spec.lateralBow * arch;
    const by = lerp(p0.y, p1.y, t) + ly * chordLen * spec.lateralBow * arch;
    const bz = lerp(p0.z, p1.z, t) + lz * chordLen * spec.lateralBow * arch;
    const bLen = Math.hypot(bx, by, bz) || 1;

    // Radial elevation (exaggerated so the arc lifts dramatically off the globe)
    const lift = 1 + spec.altitude * chordLen * arch;
    const vx = (bx / bLen) * lift;
    const vy = (by / bLen) * lift;
    const vz = (bz / bLen) * lift;

    const visible = vz >= 0 || vx * vx + vy * vy >= 0.995;
    points.push({
      x: cx + vx * radius,
      y: cy - vy * radius,
      visible,
    });
  }

  return {
    points,
    phase: spec.phase,
    duration: spec.duration,
    isHero: false,
  };
}

function sampleHeroArc(metrics: LayoutMetrics): PreparedArc {
  const { cx, cy, radius } = metrics;
  const p0 = latLonToView(HERO_ARC_ORIGIN[0], HERO_ARC_ORIGIN[1]);

  // 3D cubic Bezier launching out of HCMC, sweeping gently east as it climbs
  // to keep Vietnam unobstructed, vaulting ~0.25R above the top rim into dark space,
  // then curving gracefully down behind the sunlit upper-left horizon.
  const c1: Vec3 = {
    x: p0.x + 0.44,
    y: 0.96,
    z: 0.74,
  };
  const c2: Vec3 = {
    x: -0.26,
    y: 1.84,
    z: -0.18,
  };
  const p3: Vec3 = {
    x: -0.54,
    y: 0.72,
    z: -0.44,
  };

  const segments = 76;
  const points: SampledArcPoint[] = [];

  for (let i = 0; i <= segments; i++) {
    const t = i / segments;
    const u = 1 - t;
    const u2 = u * u;
    const u3 = u2 * u;
    const t2 = t * t;
    const t3 = t2 * t;

    const vx = u3 * p0.x + 3 * u2 * t * c1.x + 3 * u * t2 * c2.x + t3 * p3.x;
    const vy = u3 * p0.y + 3 * u2 * t * c1.y + 3 * u * t2 * c2.y + t3 * p3.y;
    const vz = u3 * p0.z + 3 * u2 * t * c1.z + 3 * u * t2 * c2.z + t3 * p3.z;

    const visible = vz >= 0 || vx * vx + vy * vy >= 0.995;
    points.push({
      x: cx + vx * radius,
      y: cy - vy * radius,
      visible,
    });
  }

  return {
    points,
    phase: 0.15,
    duration: 5.8,
    isHero: true,
  };
}

// ---------------------------------------------------------------------------
// Static background rendering (drawn ONCE per size onto offscreen canvas)
// ---------------------------------------------------------------------------
function renderStaticBackdrop(
  ctx: CanvasRenderingContext2D,
  metrics: LayoutMetrics,
  landDots: readonly LandDot[]
): void {
  const { width, height, cx, cy, radius, dpr } = metrics;
  ctx.clearRect(0, 0, width, height);

  // 1. Wide atmospheric outer aura strongly weighted toward the sunlit upper-left
  const outerAura = ctx.createRadialGradient(
    cx - radius * 0.2,
    cy - radius * 0.18,
    radius * 0.62,
    cx - radius * 0.11,
    cy - radius * 0.1,
    radius * 1.32
  );
  outerAura.addColorStop(0, PALETTE.atmoOuterInner);
  outerAura.addColorStop(0.55, PALETTE.atmoOuterMid);
  outerAura.addColorStop(1, PALETTE.atmoOuterEdge);
  ctx.fillStyle = outerAura;
  ctx.fillRect(0, 0, width, height);

  // 2. Crisp atmospheric corona right outside the left and top sunlit limbs
  const rimHalo = ctx.createRadialGradient(
    cx - radius * 0.11,
    cy - radius * 0.09,
    radius * 0.88,
    cx - radius * 0.07,
    cy - radius * 0.06,
    radius * 1.13
  );
  rimHalo.addColorStop(0, PALETTE.atmoRimPeak);
  rimHalo.addColorStop(0.36, PALETTE.atmoRimSoft);
  rimHalo.addColorStop(0.72, PALETTE.atmoRimTail);
  rimHalo.addColorStop(1, PALETTE.atmoRimEdge);
  ctx.fillStyle = rimHalo;
  ctx.beginPath();
  ctx.arc(cx - radius * 0.07, cy - radius * 0.06, radius * 1.13, 0, Math.PI * 2);
  ctx.fill();

  // Feather ONLY the outer atmospheric glow to exact alpha 0 before all 4 canvas edges
  // (leaving the planet sphere drawn next at full brightness and diameter, with zero hard seam).
  ctx.save();
  ctx.globalCompositeOperation = "destination-out";

  const leftEdgePad = Math.max(16, (cx - radius) * 0.94);
  const leftFade = ctx.createLinearGradient(0, 0, leftEdgePad, 0);
  leftFade.addColorStop(0.0, "rgba(0, 0, 0, 1)");
  leftFade.addColorStop(0.45, "rgba(0, 0, 0, 0.42)");
  leftFade.addColorStop(1.0, "rgba(0, 0, 0, 0)");
  ctx.fillStyle = leftFade;
  ctx.fillRect(0, 0, leftEdgePad, height);

  const topEdgePad = Math.max(18, (cy - radius) * 0.48);
  const topFade = ctx.createLinearGradient(0, 0, 0, topEdgePad);
  topFade.addColorStop(0.0, "rgba(0, 0, 0, 1)");
  topFade.addColorStop(0.45, "rgba(0, 0, 0, 0.38)");
  topFade.addColorStop(1.0, "rgba(0, 0, 0, 0)");
  ctx.fillStyle = topFade;
  ctx.fillRect(0, 0, width, topEdgePad);

  const rightEdgePad = Math.max(24, (width - (cx + radius)) * 0.72);
  const rightFade = ctx.createLinearGradient(width - rightEdgePad, 0, width, 0);
  rightFade.addColorStop(0.0, "rgba(0, 0, 0, 0)");
  rightFade.addColorStop(0.55, "rgba(0, 0, 0, 0.38)");
  rightFade.addColorStop(1.0, "rgba(0, 0, 0, 1)");
  ctx.fillStyle = rightFade;
  ctx.fillRect(width - rightEdgePad, 0, rightEdgePad, height);

  const bottomEdgePad = Math.max(20, Math.round(height * 0.05));
  const bottomEdgeFade = ctx.createLinearGradient(0, height - bottomEdgePad, 0, height);
  bottomEdgeFade.addColorStop(0.0, "rgba(0, 0, 0, 0)");
  bottomEdgeFade.addColorStop(1.0, "rgba(0, 0, 0, 1)");
  ctx.fillStyle = bottomEdgeFade;
  ctx.fillRect(0, height - bottomEdgePad, width, bottomEdgePad);
  ctx.restore();

  // 3. Clipped planet sphere: ocean + textured land + terminator + atmospheric rims + world clusters
  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, radius, 0, Math.PI * 2);
  ctx.clip();

  // Base deep midnight indigo ocean (distinct navy hue, never flat black)
  ctx.fillStyle = PALETTE.oceanBase;
  ctx.fillRect(cx - radius, cy - radius, radius * 2, radius * 2);

  // One-sided sunlit cyan-teal -> deep royal navy -> midnight indigo ocean hemisphere
  const oceanGrad = ctx.createRadialGradient(
    cx - radius * 0.68,
    cy - radius * 0.56,
    radius * 0.04,
    cx - radius * 0.28,
    cy - radius * 0.22,
    radius * 1.4
  );
  oceanGrad.addColorStop(0.0, PALETTE.oceanSunCore);
  oceanGrad.addColorStop(0.15, PALETTE.oceanSunMid);
  oceanGrad.addColorStop(0.32, PALETTE.oceanMidBlue);
  oceanGrad.addColorStop(0.5, PALETTE.oceanRoyalNavy);
  oceanGrad.addColorStop(0.7, PALETTE.oceanTwilight);
  oceanGrad.addColorStop(0.88, PALETTE.oceanNight);
  oceanGrad.addColorStop(1.0, PALETTE.oceanDeepShadow);
  ctx.fillStyle = oceanGrad;
  ctx.fillRect(cx - radius, cy - radius, radius * 2, radius * 2);

  // Subtle deep sapphire-indigo richness on the shaded right flank
  const nightTint = ctx.createRadialGradient(
    cx + radius * 0.56,
    cy - radius * 0.14,
    radius * 0.08,
    cx + radius * 0.46,
    cy - radius * 0.08,
    radius * 0.94
  );
  nightTint.addColorStop(0, PALETTE.oceanNightTint);
  nightTint.addColorStop(1, "rgba(6, 14, 48, 0)");
  ctx.fillStyle = nightTint;
  ctx.fillRect(cx - radius, cy - radius, radius * 2, radius * 2);

  // Continents from Natural Earth 110m land:
  // Combine a soft cohesive emerald-teal landmass base (Pass A) with 3 organic jittered
  // stipple micro-dots per 1.2° cell (Pass B) so continents read sharply at both dpr=1 and dpr=2.
  const scaleFactor = Math.max(0.68, Math.min(1.1, radius / 292));
  const dprDotBoost = dpr < 1.5 ? 0.14 : 0;
  const dprBaseBoost = dpr < 1.5 ? 0.22 : 0;

  // Pass A: soft overlapping forest-emerald continental base layer (jittered to avoid latitude bands)
  for (let i = 0; i < landDots.length; i++) {
    const rawLat = landDots[i][0];
    const rawLon = landDots[i][1];
    const cosLat = Math.max(0.26, Math.cos(rawLat * DEG));
    const jLat = (hash2(rawLat * 1.07 + 1.9, rawLon * 0.93 - 3.1) - 0.5) * 0.68;
    const jLon = (hash2(rawLon * 1.13 - 2.7, rawLat * 0.87 + 4.3) - 0.5) * (0.72 / cosLat);
    const lat = Math.max(-89, Math.min(89, rawLat + jLat));
    const lon = rawLon + jLon;

    const v = latLonToView(lat, lon);
    if (v.z <= 0.03) continue;

    const rawDot = v.x * LIGHT_DIR[0] + v.y * LIGHT_DIR[1] + v.z * LIGHT_DIR[2];
    const dayLight = smoothstep(-0.32, 0.92, rawDot);
    const limbFade = Math.pow(v.z, 0.46);
    const biomeNoise = valueNoise2D(lat * 0.13 + 5.2, lon * 0.13 - 3.7);
    const sizeSeed = hash2(rawLat * 1.91 + 3.4, rawLon * 1.57 - 2.2);
    const shade = clamp01(dayLight * (0.76 + 0.32 * biomeNoise));

    let r: number;
    let g: number;
    let b: number;
    if (shade < 0.5) {
      const t = shade * 2;
      r = lerp(PALETTE.landDarkRgb[0], PALETTE.landMidRgb[0], t);
      g = lerp(PALETTE.landDarkRgb[1], PALETTE.landMidRgb[1], t);
      b = lerp(PALETTE.landDarkRgb[2], PALETTE.landMidRgb[2], t);
    } else {
      const t = (shade - 0.5) * 2;
      r = lerp(PALETTE.landMidRgb[0], PALETTE.landLitRgb[0], t);
      g = lerp(PALETTE.landMidRgb[1], PALETTE.landLitRgb[1], t);
      b = lerp(PALETTE.landMidRgb[2], PALETTE.landLitRgb[2], t);
    }

    const px = cx + v.x * radius;
    const py = cy - v.y * radius;
    const baseRadius = (3.0 + 0.9 * sizeSeed + dprBaseBoost) * scaleFactor * (0.58 + 0.42 * v.z);
    const alpha = (0.12 + 0.3 * Math.pow(dayLight, 0.78)) * limbFade * (0.8 + 0.32 * biomeNoise);

    ctx.fillStyle = `rgba(${Math.round(r)},${Math.round(g)},${Math.round(b)},${clamp01(alpha).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(px, py, baseRadius, 0, Math.PI * 2);
    ctx.fill();
  }

  // Pass B: dense organic multi-grain stipple (3 deterministic sub-dots per cell)
  for (let i = 0; i < landDots.length; i++) {
    const rawLat = landDots[i][0];
    const rawLon = landDots[i][1];
    const cosLat = Math.max(0.26, Math.cos(rawLat * DEG));

    for (let s = 0; s < 3; s++) {
      const jLat =
        (hash2(rawLat * (1.17 + s * 0.73) + 4.3 + s * 11.7, rawLon * (0.89 + s * 0.41) - 2.1) -
          0.5) *
        1.04;
      const jLon =
        (hash2(rawLon * (1.31 + s * 0.59) - 6.7, rawLat * (0.79 + s * 0.67) + 9.4 + s * 7.3) -
          0.5) *
        (1.08 / cosLat);
      const lat = Math.max(-89, Math.min(89, rawLat + jLat));
      const lon = rawLon + jLon;

      const v = latLonToView(lat, lon);
      if (v.z <= 0.03) continue;

      const rawDot = v.x * LIGHT_DIR[0] + v.y * LIGHT_DIR[1] + v.z * LIGHT_DIR[2];
      const dayLight = smoothstep(-0.32, 0.92, rawDot);
      const limbFade = Math.pow(v.z, 0.48);
      const sizeSeed = hash2(rawLat * 2.41 + s * 13.1, rawLon * 1.73 - s * 5.9);
      const terrainNoise =
        0.58 * valueNoise2D(lat * 0.17 + 12.4, lon * 0.17 - 7.8) +
        0.42 * hash2(rawLat * 3.17 + s * 9.3, rawLon * 2.63 + s * 4.1);

      const px = cx + v.x * radius;
      const py = cy - v.y * radius;

      const shade = clamp01(dayLight * (0.72 + 0.46 * terrainNoise));
      let r: number;
      let g: number;
      let b: number;
      if (shade < 0.5) {
        const t = shade * 2;
        r = lerp(PALETTE.landDarkRgb[0], PALETTE.landMidRgb[0], t);
        g = lerp(PALETTE.landDarkRgb[1], PALETTE.landMidRgb[1], t);
        b = lerp(PALETTE.landDarkRgb[2], PALETTE.landMidRgb[2], t);
      } else {
        const t = (shade - 0.5) * 2;
        r = lerp(PALETTE.landMidRgb[0], PALETTE.landLitRgb[0], t);
        g = lerp(PALETTE.landMidRgb[1], PALETTE.landLitRgb[1], t);
        b = lerp(PALETTE.landMidRgb[2], PALETTE.landLitRgb[2], t);
      }

      const dotRadius = (0.46 + 0.48 * sizeSeed + dprDotBoost) * scaleFactor * (0.78 + 0.22 * v.z);
      const alpha =
        (0.22 + 0.72 * Math.pow(dayLight, 0.65)) * limbFade * (0.52 + 0.5 * terrainNoise);

      ctx.fillStyle = `rgba(${Math.round(r)},${Math.round(g)},${Math.round(b)},${clamp01(alpha).toFixed(3)})`;
      ctx.beginPath();
      ctx.arc(px, py, dotRadius, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  // Soft diagonal terminator shadow unifying ocean & continents across the night flank
  const terminatorVeil = ctx.createLinearGradient(
    cx - radius * 0.44,
    cy - radius * 0.44,
    cx + radius * 0.86,
    cy + radius * 0.8
  );
  terminatorVeil.addColorStop(0.0, "rgba(2, 5, 18, 0)");
  terminatorVeil.addColorStop(0.4, "rgba(2, 5, 18, 0)");
  terminatorVeil.addColorStop(0.72, "rgba(3, 6, 22, 0.42)");
  terminatorVeil.addColorStop(1.0, "rgba(2, 4, 16, 0.72)");
  ctx.fillStyle = terminatorVeil;
  ctx.fillRect(cx - radius, cy - radius, radius * 2, radius * 2);

  // Subtle sunlit atmospheric sheen washing the upper-left day hemisphere
  const sunHaze = ctx.createRadialGradient(
    cx - radius * 0.72,
    cy - radius * 0.6,
    radius * 0.04,
    cx - radius * 0.6,
    cy - radius * 0.5,
    radius * 0.82
  );
  sunHaze.addColorStop(0.0, "rgba(86, 228, 234, 0.09)");
  sunHaze.addColorStop(0.5, "rgba(32, 182, 198, 0.03)");
  sunHaze.addColorStop(1.0, "rgba(32, 182, 198, 0)");
  ctx.fillStyle = sunHaze;
  ctx.fillRect(cx - radius, cy - radius, radius * 2, radius * 2);

  // Pass C: Baked Gaussian warm urban light clusters around major world metros
  // Drawn after terminator shading in source-over mode so lights stay warm amber without clipping white.
  ctx.save();
  ctx.globalCompositeOperation = "source-over";
  const clusterRand = mulberry32(20261127);
  for (let i = 0; i < WORLD_CITIES.length; i++) {
    const [cLat, cLon, weight] = WORLD_CITIES[i];
    const centerV = latLonToView(cLat, cLon);
    if (centerV.z <= 0.02) continue;

    const cosLat = Math.max(0.3, Math.cos(cLat * DEG));
    const sparkCount = Math.round(4 + weight * 10);
    const sigmaLat = 0.48 + weight * 0.85;
    const sigmaLon = (0.58 + weight * 1.0) / cosLat;

    for (let k = 0; k < sparkCount; k++) {
      const [g0, g1] = gaussianPair(clusterRand);
      const sLat = Math.max(-85, Math.min(85, cLat + g0 * sigmaLat));
      const sLon = cLon + g1 * sigmaLon;
      const sv = latLonToView(sLat, sLon);
      if (sv.z <= 0.05) continue;

      const limb = Math.pow(sv.z, 0.5);
      const distNorm = Math.hypot(g0, g1);
      const falloff = Math.exp(-distNorm * 0.58);
      const intensity = weight * limb * falloff * (0.48 + 0.52 * clusterRand());
      const sx = cx + sv.x * radius;
      const sy = cy - sv.y * radius;

      const glowR = (1.3 + 1.4 * falloff) * scaleFactor;
      const glow = ctx.createRadialGradient(sx, sy, 0, sx, sy, glowR);
      glow.addColorStop(0, `rgba(${PALETTE.worldGlow}, ${(0.34 * intensity).toFixed(3)})`);
      glow.addColorStop(1, `rgba(${PALETTE.worldGlow}, 0)`);
      ctx.fillStyle = glow;
      ctx.beginPath();
      ctx.arc(sx, sy, glowR, 0, Math.PI * 2);
      ctx.fill();

      ctx.fillStyle = `rgba(${PALETTE.worldCore}, ${(0.8 * intensity).toFixed(3)})`;
      ctx.beginPath();
      ctx.arc(sx, sy, (0.42 + 0.38 * falloff) * scaleFactor, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  // Minimal tight suburban sparks only around Vietnam's 6 major municipalities so the S-curve stays crisp
  for (let i = 0; i < VN_CAPITALS.length; i++) {
    const [cLat, cLon, weight] = VN_CAPITALS[i];
    if (weight < 0.84) continue;
    const sigma = 0.12;
    for (let k = 0; k < 2; k++) {
      const [g0, g1] = gaussianPair(clusterRand);
      const sv = latLonToView(cLat + g0 * sigma, cLon + g1 * sigma);
      if (sv.z <= 0.1) continue;
      const distNorm = Math.hypot(g0, g1);
      const falloff = Math.exp(-distNorm * 0.72);
      const intensity = weight * falloff * (0.5 + 0.5 * clusterRand());
      const sx = cx + sv.x * radius;
      const sy = cy - sv.y * radius;

      const glowR = (1.1 + 0.8 * falloff) * scaleFactor;
      const glow = ctx.createRadialGradient(sx, sy, 0, sx, sy, glowR);
      glow.addColorStop(0, `rgba(${PALETTE.vnGlowMid}, ${(0.28 * intensity).toFixed(3)})`);
      glow.addColorStop(1, `rgba(${PALETTE.vnGlowOuter}, 0)`);
      ctx.fillStyle = glow;
      ctx.beginPath();
      ctx.arc(sx, sy, glowR, 0, Math.PI * 2);
      ctx.fill();

      ctx.fillStyle = `rgba(${PALETTE.vnCore}, ${(0.55 * intensity).toFixed(3)})`;
      ctx.beginPath();
      ctx.arc(sx, sy, (0.36 + 0.24 * falloff) * scaleFactor, 0, Math.PI * 2);
      ctx.fill();
    }
  }
  ctx.restore();

  // Inner directional Fresnel scatter hugging the sunlit left & top horizon (fading on shaded bottom-right)
  const fresnel = ctx.createRadialGradient(
    cx + radius * 0.24,
    cy + radius * 0.22,
    radius * 0.76,
    cx + radius * 0.14,
    cy + radius * 0.13,
    radius * 1.1
  );
  fresnel.addColorStop(0.0, "rgba(62, 224, 226, 0)");
  fresnel.addColorStop(0.76, "rgba(62, 224, 226, 0)");
  fresnel.addColorStop(0.9, PALETTE.fresnelMid);
  fresnel.addColorStop(1.0, PALETTE.fresnelEdge);
  ctx.fillStyle = fresnel;
  ctx.fillRect(cx - radius, cy - radius, radius * 2, radius * 2);

  // Crisp sunlit atmospheric rim arc along the left and top limb (~8:30 through 12:00 to ~1:00)
  ctx.save();
  ctx.lineWidth = Math.max(6.5, radius * 0.04);
  const sunRimBand = ctx.createLinearGradient(
    cx - radius * 0.92,
    cy + radius * 0.42,
    cx + radius * 0.34,
    cy - radius * 0.96
  );
  sunRimBand.addColorStop(0.0, "rgba(86, 236, 238, 0)");
  sunRimBand.addColorStop(0.28, PALETTE.sunRimSoft);
  sunRimBand.addColorStop(0.58, PALETTE.sunRimSoft);
  sunRimBand.addColorStop(1.0, "rgba(86, 236, 238, 0)");
  ctx.strokeStyle = sunRimBand;
  ctx.beginPath();
  ctx.arc(cx, cy, radius - ctx.lineWidth * 0.38, Math.PI * 0.78, Math.PI * 1.58);
  ctx.stroke();

  ctx.lineWidth = Math.max(2.0, radius * 0.013);
  const sunRimEdge = ctx.createLinearGradient(
    cx - radius * 0.92,
    cy + radius * 0.42,
    cx + radius * 0.34,
    cy - radius * 0.96
  );
  sunRimEdge.addColorStop(0.0, "rgba(166, 250, 248, 0)");
  sunRimEdge.addColorStop(0.26, PALETTE.sunRimPeak);
  sunRimEdge.addColorStop(0.62, PALETTE.sunRimPeak);
  sunRimEdge.addColorStop(1.0, "rgba(166, 250, 248, 0)");
  ctx.strokeStyle = sunRimEdge;
  ctx.beginPath();
  ctx.arc(cx, cy, radius - ctx.lineWidth * 0.5, Math.PI * 0.8, Math.PI * 1.56);
  ctx.stroke();
  ctx.restore();

  // Warm grazing horizon glint along the upper-right limb (like Shopify ref)
  ctx.save();
  ctx.lineWidth = Math.max(4.5, radius * 0.032);
  const glintHaloGrad = ctx.createLinearGradient(
    cx + radius * 0.18,
    cy - radius * 0.96,
    cx + radius * 0.94,
    cy - radius * 0.08
  );
  glintHaloGrad.addColorStop(0, "rgba(255, 172, 82, 0)");
  glintHaloGrad.addColorStop(0.36, PALETTE.twilightRimWarmHalo);
  glintHaloGrad.addColorStop(0.75, PALETTE.twilightRimIndigo);
  glintHaloGrad.addColorStop(1, "rgba(108, 138, 232, 0)");
  ctx.strokeStyle = glintHaloGrad;
  ctx.beginPath();
  ctx.arc(cx, cy, radius - ctx.lineWidth * 0.35, -Math.PI * 0.42, -Math.PI * 0.02);
  ctx.stroke();

  ctx.lineWidth = Math.max(1.8, radius * 0.011);
  const glintGrad = ctx.createLinearGradient(
    cx + radius * 0.18,
    cy - radius * 0.96,
    cx + radius * 0.94,
    cy - radius * 0.12
  );
  glintGrad.addColorStop(0, "rgba(255, 214, 146, 0)");
  glintGrad.addColorStop(0.35, PALETTE.twilightRimGold);
  glintGrad.addColorStop(0.68, PALETTE.twilightRimIndigo);
  glintGrad.addColorStop(1, "rgba(108, 138, 232, 0)");
  ctx.strokeStyle = glintGrad;
  ctx.beginPath();
  ctx.arc(cx, cy, radius - ctx.lineWidth * 0.5, -Math.PI * 0.4, -Math.PI * 0.06);
  ctx.stroke();
  ctx.restore();

  ctx.restore();

  // Gently melt the bottom ~20% of the globe into the dark cosmic backdrop
  // using alpha feathering so it blends naturally on any dark surface (--home-pine / --home-band).
  ctx.save();
  ctx.globalCompositeOperation = "destination-out";
  const bottomFeather = ctx.createLinearGradient(cx, cy + radius * 0.56, cx, cy + radius * 1.12);
  bottomFeather.addColorStop(0.0, "rgba(0, 0, 0, 0)");
  bottomFeather.addColorStop(0.55, "rgba(0, 0, 0, 0.32)");
  bottomFeather.addColorStop(0.84, "rgba(0, 0, 0, 0.88)");
  bottomFeather.addColorStop(1.0, "rgba(0, 0, 0, 1)");
  ctx.fillStyle = bottomFeather;
  ctx.fillRect(0, cy + radius * 0.54, width, height - (cy + radius * 0.54));
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Dynamic frame rendering (stars + city lights + animated glowing arcs)
// ---------------------------------------------------------------------------
function drawStars(
  ctx: CanvasRenderingContext2D,
  stars: readonly Star[],
  timeSec: number,
  reducedMotion: boolean
): void {
  for (let i = 0; i < stars.length; i++) {
    const s = stars[i];
    const twinkle = reducedMotion ? 0.85 : 0.62 + 0.38 * Math.sin(timeSec * s.speed + s.phase);
    const alpha = s.baseAlpha * twinkle;

    if (s.haloRadius > 0) {
      const grad = ctx.createRadialGradient(s.x, s.y, 0, s.x, s.y, s.haloRadius);
      grad.addColorStop(0, `rgba(${PALETTE.starTint}, ${(alpha * 0.52).toFixed(3)})`);
      grad.addColorStop(0.35, `rgba(${PALETTE.starHalo}, ${(alpha * 0.16).toFixed(3)})`);
      grad.addColorStop(1, `rgba(${PALETTE.starHalo}, 0)`);
      ctx.fillStyle = grad;
      ctx.beginPath();
      ctx.arc(s.x, s.y, s.haloRadius, 0, Math.PI * 2);
      ctx.fill();
    }

    ctx.fillStyle = `rgba(${PALETTE.starTint}, ${alpha.toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(s.x, s.y, s.radius, 0, Math.PI * 2);
    ctx.fill();
  }
}

function drawCities(
  ctx: CanvasRenderingContext2D,
  worldCities: readonly ProjectedCity[],
  vnCities: readonly ProjectedCity[],
  metrics: LayoutMetrics,
  timeSec: number,
  reducedMotion: boolean
): void {
  const { cx, cy, radius } = metrics;
  const scale = Math.max(0.62, Math.min(1.08, radius / 292));

  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, radius - 0.5, 0, Math.PI * 2);
  ctx.clip();
  // Use source-over (never additive) so dense provincial clusters stay warm gold-amber
  // and never blow out into a white mass hiding Vietnam's S-shaped landmass.
  ctx.globalCompositeOperation = "source-over";

  // World cities: crisp warm amber lights across Asia/Oceania/Eurasia
  for (let i = 0; i < worldCities.length; i++) {
    const c = worldCities[i];
    const shimmer = reducedMotion ? 0.92 : 0.82 + 0.18 * Math.sin(timeSec * c.speed + c.phase);
    const strength = c.weight * c.limb * shimmer;

    const glowR = (2.1 + 2.0 * c.weight) * scale;
    const glow = ctx.createRadialGradient(c.x, c.y, 0, c.x, c.y, glowR);
    glow.addColorStop(0, `rgba(${PALETTE.worldGlow}, ${(0.52 * strength).toFixed(3)})`);
    glow.addColorStop(1, `rgba(${PALETTE.worldGlow}, 0)`);
    ctx.fillStyle = glow;
    ctx.beginPath();
    ctx.arc(c.x, c.y, glowR, 0, Math.PI * 2);
    ctx.fill();

    ctx.fillStyle = `rgba(${PALETTE.worldCore}, ${(0.92 * strength).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(c.x, c.y, (0.68 + 0.5 * c.weight) * scale, 0, Math.PI * 2);
    ctx.fill();
  }

  // 34 Vietnamese provincial capitals: brightest warm gold jewels on the globe,
  // with compact halos so the S-curve coastline and individual provinces read clearly.
  for (let i = 0; i < vnCities.length; i++) {
    const c = vnCities[i];
    const shimmer = reducedMotion ? 0.96 : 0.88 + 0.12 * Math.sin(timeSec * c.speed + c.phase);
    const strength = c.weight * c.limb * shimmer;

    const outerR = (2.2 + 2.2 * c.weight) * scale;
    const outer = ctx.createRadialGradient(c.x, c.y, 0, c.x, c.y, outerR);
    outer.addColorStop(0, `rgba(${PALETTE.vnGlowMid}, ${(0.78 * strength).toFixed(3)})`);
    outer.addColorStop(0.48, `rgba(${PALETTE.vnGlowOuter}, ${(0.34 * strength).toFixed(3)})`);
    outer.addColorStop(1, `rgba(${PALETTE.vnGlowOuter}, 0)`);
    ctx.fillStyle = outer;
    ctx.beginPath();
    ctx.arc(c.x, c.y, outerR, 0, Math.PI * 2);
    ctx.fill();

    ctx.fillStyle = `rgba(${PALETTE.vnCore}, ${clamp01(1.0 * strength).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(c.x, c.y, (0.72 + 0.56 * c.weight) * scale, 0, Math.PI * 2);
    ctx.fill();
  }

  ctx.restore();
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
    return {
      x: lerp(a.x, b.x, f),
      y: lerp(a.y, b.y, f),
      visible: a.visible || b.visible,
    };
  };

  ctx.lineWidth = width;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.strokeStyle = strokeStyle;
  ctx.beginPath();

  let penDown = false;
  let lastPoint: SampledArcPoint | null = null;

  for (let i = i0; i <= i1; i++) {
    let pt: SampledArcPoint;
    if (i === i0 && startIdx > i0) {
      pt = sampleAt(startIdx);
    } else if (i === i1 && endIdx < i1) {
      pt = sampleAt(endIdx);
    } else {
      pt = points[i];
    }

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

  ctx.stroke();
  return lastPoint;
}

function drawArcSpark(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  radius: number,
  alpha: number
): void {
  const halo = ctx.createRadialGradient(x, y, 0, x, y, radius * 2.8);
  halo.addColorStop(0, `rgba(${PALETTE.arcSpark}, ${(alpha * 0.95).toFixed(3)})`);
  halo.addColorStop(0.35, `rgba(${PALETTE.arcGlow}, ${(alpha * 0.45).toFixed(3)})`);
  halo.addColorStop(1, `rgba(${PALETTE.arcGlow}, 0)`);
  ctx.fillStyle = halo;
  ctx.beginPath();
  ctx.arc(x, y, radius * 2.8, 0, Math.PI * 2);
  ctx.fill();

  ctx.fillStyle = `rgba(${PALETTE.arcSpark}, ${alpha.toFixed(3)})`;
  ctx.beginPath();
  ctx.arc(x, y, radius * 0.72, 0, Math.PI * 2);
  ctx.fill();
}

function drawArcs(
  ctx: CanvasRenderingContext2D,
  arcs: readonly PreparedArc[],
  heroArc: PreparedArc,
  metrics: LayoutMetrics,
  timeSec: number,
  reducedMotion: boolean
): void {
  const scale = Math.max(0.68, Math.min(1.08, metrics.radius / 292));

  ctx.save();
  ctx.globalCompositeOperation = "lighter";

  // Domestic luminous arcs across Vietnam
  for (let i = 0; i < arcs.length; i++) {
    const arc = arcs[i];
    const pts = arc.points;
    const pStart = pts[0];
    const pEnd = pts[pts.length - 1];

    if (reducedMotion) {
      const glowGrad = ctx.createLinearGradient(pStart.x, pStart.y, pEnd.x, pEnd.y);
      glowGrad.addColorStop(0, `rgba(${PALETTE.arcGlow}, 0.04)`);
      glowGrad.addColorStop(0.5, `rgba(${PALETTE.arcGlow}, 0.16)`);
      glowGrad.addColorStop(1, `rgba(${PALETTE.arcGlow}, 0.05)`);
      strokeArcSlice(ctx, pts, 0, 1, 2.0 * scale, glowGrad);

      const coreGrad = ctx.createLinearGradient(pStart.x, pStart.y, pEnd.x, pEnd.y);
      coreGrad.addColorStop(0, `rgba(${PALETTE.arcCore}, 0.14)`);
      coreGrad.addColorStop(0.5, `rgba(${PALETTE.arcCore}, 0.48)`);
      coreGrad.addColorStop(1, `rgba(${PALETTE.arcCore}, 0.18)`);
      strokeArcSlice(ctx, pts, 0, 1, 0.8 * scale, coreGrad);
      continue;
    }

    // Delicate persistent filaments on primary backbone corridors so Vietnam's trade web reads continuously
    if (i < 8) {
      const baseGrad = ctx.createLinearGradient(pStart.x, pStart.y, pEnd.x, pEnd.y);
      baseGrad.addColorStop(0, `rgba(${PALETTE.arcCore}, 0.025)`);
      baseGrad.addColorStop(0.5, `rgba(${PALETTE.arcCore}, 0.15)`);
      baseGrad.addColorStop(1, `rgba(${PALETTE.arcCore}, 0.025)`);
      strokeArcSlice(ctx, pts, 0, 1, 0.65 * scale, baseGrad);
    }

    const cycle = (timeSec / arc.duration + arc.phase) % 1;
    // 0..0.52 draw head forward with trailing tail, 0.52..0.84 tail catches up & fades, 0.84..1 rest
    if (cycle >= 0.86) continue;

    let head: number;
    let tail: number;
    let fade: number;

    if (cycle < 0.52) {
      const p = easeInOutCubic(cycle / 0.52);
      head = p;
      tail = Math.max(0, p - 0.68);
      fade = Math.min(1, cycle / 0.08);
    } else {
      const p = (cycle - 0.52) / 0.34;
      head = 1;
      tail = lerp(0.32, 1, easeInOutCubic(p));
      fade = 1 - p * p;
    }

    if (head - tail < 0.015 || fade <= 0.01) continue;

    const tailPt = pts[Math.floor(tail * (pts.length - 1))];
    const headPt = pts[Math.min(pts.length - 1, Math.ceil(head * (pts.length - 1)))];

    const glowGrad = ctx.createLinearGradient(tailPt.x, tailPt.y, headPt.x, headPt.y);
    glowGrad.addColorStop(0, `rgba(${PALETTE.arcGlow}, 0)`);
    glowGrad.addColorStop(0.65, `rgba(${PALETTE.arcGlow}, ${(0.16 * fade).toFixed(3)})`);
    glowGrad.addColorStop(1, `rgba(${PALETTE.arcSpark}, ${(0.3 * fade).toFixed(3)})`);
    strokeArcSlice(ctx, pts, tail, head, 2.3 * scale, glowGrad);

    const coreGrad = ctx.createLinearGradient(tailPt.x, tailPt.y, headPt.x, headPt.y);
    coreGrad.addColorStop(0, `rgba(${PALETTE.arcCore}, 0)`);
    coreGrad.addColorStop(0.55, `rgba(${PALETTE.arcCore}, ${(0.54 * fade).toFixed(3)})`);
    coreGrad.addColorStop(1, `rgba(${PALETTE.arcSpark}, ${(0.92 * fade).toFixed(3)})`);
    const tip = strokeArcSlice(ctx, pts, tail, head, 0.9 * scale, coreGrad);

    if (tip && cycle < 0.62) {
      drawArcSpark(ctx, tip.x, tip.y, 1.2 * scale, fade * 0.78);
    }
  }

  // Signature hero arc soaring from HCMC high above the top edge of the globe
  const hPts = heroArc.points;
  const hStart = hPts[0];
  const hPeak = hPts[Math.floor(hPts.length * 0.56)];
  const hEnd = hPts[hPts.length - 1];

  // Persistent luminous bridge so the iconic upper arch is always clearly visible
  // from HCMC over the top rim to where it tucks behind the upper-left horizon.
  const baseGlowOpacity = reducedMotion ? 0.26 : 0.2;
  const baseCoreOpacity = reducedMotion ? 0.78 : 0.56;

  const heroOuter = ctx.createLinearGradient(hStart.x, hStart.y, hEnd.x, hEnd.y);
  heroOuter.addColorStop(
    0,
    `rgba(${PALETTE.heroBaseGlow}, ${(baseGlowOpacity * 0.25).toFixed(3)})`
  );
  heroOuter.addColorStop(0.32, `rgba(${PALETTE.heroBaseGlow}, ${baseGlowOpacity.toFixed(3)})`);
  heroOuter.addColorStop(
    0.86,
    `rgba(${PALETTE.heroBaseGlow}, ${(baseGlowOpacity * 0.88).toFixed(3)})`
  );
  heroOuter.addColorStop(
    1,
    `rgba(${PALETTE.heroBaseGlow}, ${(baseGlowOpacity * 0.35).toFixed(3)})`
  );
  strokeArcSlice(ctx, hPts, 0, 1, 8.2 * scale, heroOuter);

  const heroMid = ctx.createLinearGradient(hStart.x, hStart.y, hEnd.x, hEnd.y);
  heroMid.addColorStop(0, `rgba(${PALETTE.heroBaseCore}, ${(baseCoreOpacity * 0.22).toFixed(3)})`);
  heroMid.addColorStop(
    0.34,
    `rgba(${PALETTE.heroBaseCore}, ${(baseCoreOpacity * 0.78).toFixed(3)})`
  );
  heroMid.addColorStop(
    0.86,
    `rgba(${PALETTE.heroBaseCore}, ${(baseCoreOpacity * 0.72).toFixed(3)})`
  );
  heroMid.addColorStop(1, `rgba(${PALETTE.heroBaseCore}, ${(baseCoreOpacity * 0.28).toFixed(3)})`);
  strokeArcSlice(ctx, hPts, 0, 1, 3.1 * scale, heroMid);

  const heroCore = ctx.createLinearGradient(hStart.x, hStart.y, hEnd.x, hEnd.y);
  heroCore.addColorStop(0, `rgba(${PALETTE.arcSpark}, ${(baseCoreOpacity * 0.3).toFixed(3)})`);
  heroCore.addColorStop(0.34, `rgba(${PALETTE.arcSpark}, ${baseCoreOpacity.toFixed(3)})`);
  heroCore.addColorStop(0.86, `rgba(${PALETTE.arcSpark}, ${(baseCoreOpacity * 0.88).toFixed(3)})`);
  heroCore.addColorStop(1, `rgba(${PALETTE.arcSpark}, ${(baseCoreOpacity * 0.35).toFixed(3)})`);
  strokeArcSlice(ctx, hPts, 0, 1, 1.35 * scale, heroCore);

  if (!reducedMotion) {
    // Sweeping bright comet pulse along the hero arc
    const cycle = (timeSec / heroArc.duration + heroArc.phase) % 1;
    if (cycle < 0.88) {
      let head: number;
      let tail: number;
      let fade: number;

      if (cycle < 0.6) {
        const p = easeInOutCubic(cycle / 0.6);
        head = p;
        tail = Math.max(0, p - 0.52);
        fade = Math.min(1, cycle / 0.08);
      } else {
        const p = (cycle - 0.6) / 0.28;
        head = 1;
        tail = lerp(0.48, 1, easeInOutCubic(p));
        fade = 1 - p * p;
      }

      if (head - tail > 0.02 && fade > 0.02) {
        const tailPt = hPts[Math.floor(tail * (hPts.length - 1))];
        const headPt = hPts[Math.min(hPts.length - 1, Math.ceil(head * (hPts.length - 1)))];

        const pulseHalo = ctx.createLinearGradient(tailPt.x, tailPt.y, headPt.x, headPt.y);
        pulseHalo.addColorStop(0, `rgba(${PALETTE.arcGlow}, 0)`);
        pulseHalo.addColorStop(0.65, `rgba(${PALETTE.arcGlow}, ${(0.28 * fade).toFixed(3)})`);
        pulseHalo.addColorStop(1, `rgba(${PALETTE.arcSpark}, ${(0.56 * fade).toFixed(3)})`);
        strokeArcSlice(ctx, hPts, tail, head, 6.2 * scale, pulseHalo);

        const pulseCore = ctx.createLinearGradient(tailPt.x, tailPt.y, headPt.x, headPt.y);
        pulseCore.addColorStop(0, `rgba(${PALETTE.arcSpark}, 0)`);
        pulseCore.addColorStop(0.55, `rgba(${PALETTE.arcSpark}, ${(0.76 * fade).toFixed(3)})`);
        pulseCore.addColorStop(1, `rgba(${PALETTE.arcSpark}, ${fade.toFixed(3)})`);
        const tip = strokeArcSlice(ctx, hPts, tail, head, 1.8 * scale, pulseCore);

        if (tip && tip.visible && cycle < 0.68) {
          drawArcSpark(ctx, tip.x, tip.y, 2.4 * scale, fade);
        }
      }
    }
  } else if (hPeak.visible) {
    // Gentle peak highlight in reduced-motion static frame
    drawArcSpark(ctx, hPeak.x, hPeak.y, 2.0 * scale, 0.72);
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
    return {
      setVisible() {},
      setReducedMotion() {},
      resize() {},
      destroy() {},
    };
  }

  const offscreen = document.createElement("canvas");
  const offCtx = offscreen.getContext("2d");

  let destroyed = false;
  let visible = true;
  let reducedMotion = options.reducedMotion;
  let rafId = 0;
  let firstRendered = false;
  let startTimeMs = 0;
  let lastWidth = 0;
  let lastHeight = 0;
  let lastDpr = 0;

  let metrics: LayoutMetrics = {
    width: 0,
    height: 0,
    dpr: 1,
    cx: 0,
    cy: 0,
    radius: 0,
  };
  let stars: Star[] = [];
  let worldCities: ProjectedCity[] = [];
  let vnCities: ProjectedCity[] = [];
  let domesticArcs: PreparedArc[] = [];
  let heroArc: PreparedArc = { points: [], phase: 0, duration: 5.8, isHero: true };

  function rebuildScene(): boolean {
    const rect = canvas.getBoundingClientRect();
    const width = Math.round(rect.width);
    const height = Math.round(rect.height);
    if (width < 16 || height < 16) return false;

    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    if (width === lastWidth && height === lastHeight && dpr === lastDpr) {
      return true;
    }

    lastWidth = width;
    lastHeight = height;
    lastDpr = dpr;

    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
    offscreen.width = canvas.width;
    offscreen.height = canvas.height;

    const radius = Math.min(width * GLOBE_RADIUS_W_RATIO, height * 0.42);
    metrics = {
      width,
      height,
      dpr,
      cx: width * GLOBE_CENTER_X_RATIO,
      cy: height * GLOBE_CENTER_Y_RATIO,
      radius,
    };

    if (offCtx) {
      offCtx.setTransform(dpr, 0, 0, dpr, 0, 0);
      renderStaticBackdrop(offCtx, metrics, landDots);
    }

    stars = buildStars(metrics);
    worldCities = projectCities(WORLD_CITIES, false, metrics);
    vnCities = projectCities(VN_CAPITALS, true, metrics);
    domesticArcs = VN_ARCS.map((spec) => sampleDomesticArc(spec, metrics));
    heroArc = sampleHeroArc(metrics);
    return true;
  }

  function renderFrame(nowMs: number): void {
    if (destroyed || metrics.width < 16) return;
    if (startTimeMs === 0) startTimeMs = nowMs;
    const timeSec = reducedMotion ? 0 : (nowMs - startTimeMs) / 1000;

    ctx!.setTransform(metrics.dpr, 0, 0, metrics.dpr, 0, 0);
    ctx!.clearRect(0, 0, metrics.width, metrics.height);
    ctx!.drawImage(offscreen, 0, 0, metrics.width, metrics.height);

    drawStars(ctx!, stars, timeSec, reducedMotion);
    drawCities(ctx!, worldCities, vnCities, metrics, timeSec, reducedMotion);
    drawArcs(ctx!, domesticArcs, heroArc, metrics, timeSec, reducedMotion);

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
  }

  function shouldAnimate(): boolean {
    return !destroyed && visible && !reducedMotion && !document.hidden;
  }

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
        rafId = requestAnimationFrame(tick);
      }
    } else {
      stopLoop();
      if (reducedMotion || !firstRendered) {
        renderFrame(performance.now());
      }
    }
  }

  function handleVisibilityChange(): void {
    syncPlayback();
  }

  document.addEventListener("visibilitychange", handleVisibilityChange);

  if (rebuildScene()) {
    renderFrame(performance.now());
    syncPlayback();
  }

  return {
    setVisible(nextVisible: boolean) {
      if (visible === nextVisible) return;
      visible = nextVisible;
      syncPlayback();
    },
    setReducedMotion(nextReduced: boolean) {
      if (reducedMotion === nextReduced) return;
      reducedMotion = nextReduced;
      syncPlayback();
    },
    resize() {
      if (destroyed) return;
      if (rebuildScene()) {
        renderFrame(performance.now());
        syncPlayback();
      }
    },
    destroy() {
      destroyed = true;
      stopLoop();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      offscreen.width = 0;
      offscreen.height = 0;
    },
  };
}
