// Pure math and motion state machine for interactive homepage globe rotation.
// Kept dependency-free so Node type-stripping unit tests can import it directly.

const DEG = Math.PI / 180;
const RAD_TO_DEG = 180 / Math.PI;
const MAX_LAT_OFFSET_DEG = 20.0;
const VELOCITY_WINDOW_MS = 80;
const MAX_INERTIA_SPEED_DEG_PER_SEC = 320;
const INERTIA_DECAY_PER_SEC = 5.4;
const INERTIA_STOP_EPS_DEG_PER_SEC = 1.5;
const ANGLE_EPS_DEG = 0.005;

export const REST_LAT_DEG = 13.5;
export const REST_LON_DEG = 106.0;
export const AXIAL_ROLL_DEG = -9.5;
export const MIN_LAT_DEG = REST_LAT_DEG - MAX_LAT_OFFSET_DEG;
export const MAX_LAT_DEG = REST_LAT_DEG + MAX_LAT_OFFSET_DEG;
export const RETURN_IDLE_DELAY_MS = 2500;
export const RETURN_DURATION_MS = 1600;

export type Mat3 = readonly number[];
export type GlobeMotionMode =
  "rest" | "dragging" | "inertia" | "idle_wait" | "returning" | "settled";

interface DragSample {
  timeMs: number;
  prevTimeMs: number;
  dLonDeg: number;
  dLatDeg: number;
}

export interface GlobeMotionState {
  mode: GlobeMotionMode;
  latDeg: number;
  lonDeg: number;
  velLonDegPerSec: number;
  velLatDegPerSec: number;
  dragStartMs: number;
  lastMoveMs: number;
  samples: DragSample[];
  idleStartMs: number;
  returnStartMs: number;
  returnStartLatDeg: number;
  returnStartLonDeg: number;
  returnDeltaLatDeg: number;
  returnDeltaLonDeg: number;
}

export function easeInOutCubic(t: number): number {
  const c = t < 0 ? 0 : t > 1 ? 1 : t;
  return c < 0.5 ? 4 * c * c * c : 1 - Math.pow(-2 * c + 2, 3) / 2;
}

export function clampLatDeg(latDeg: number): number {
  return latDeg < MIN_LAT_DEG ? MIN_LAT_DEG : latDeg > MAX_LAT_DEG ? MAX_LAT_DEG : latDeg;
}

/** Wraps an angle in degrees into (-180, 180]. */
export function wrapLonDeg(lonDeg: number): number {
  const w = ((((lonDeg + 180) % 360) + 360) % 360) - 180;
  return w <= -180 ? w + 360 : w;
}

/** Signed shortest angular distance from `fromDeg` to `toDeg` in (-180, 180]. */
export function shortestAngleDeltaDeg(fromDeg: number, toDeg: number): number {
  const d = (((toDeg - fromDeg) % 360) + 360) % 360;
  return d > 180 ? d - 360 : d;
}

/** Planet-fixed unit vector: +y north pole, +z prime meridian (0°E), +x 90°E. */
export function latLonToPlanet(latDeg: number, lonDeg: number): readonly [number, number, number] {
  const lat = latDeg * DEG;
  const lon = lonDeg * DEG;
  const cosLat = Math.cos(lat);
  return [cosLat * Math.sin(lon), Math.sin(lat), cosLat * Math.cos(lon)];
}

/** Writes the 3x3 orthogonal planet->view rotation matrix into `out` (length 9). */
export function writeRotationMatrix(
  out: Float32Array | number[],
  centerLatDeg: number,
  centerLonDeg: number,
  rollDeg: number = AXIAL_ROLL_DEG
): void {
  const lat0 = centerLatDeg * DEG;
  const lon0 = centerLonDeg * DEG;
  const roll = rollDeg * DEG;
  const sinLat0 = Math.sin(lat0);
  const cosLat0 = Math.cos(lat0);
  const sinLon0 = Math.sin(lon0);
  const cosLon0 = Math.cos(lon0);
  const sinRoll = Math.sin(roll);
  const cosRoll = Math.cos(roll);
  const r10 = -sinLat0 * sinLon0;
  const r12 = -sinLat0 * cosLon0;

  out[0] = cosRoll * cosLon0 - sinRoll * r10;
  out[1] = -sinRoll * cosLat0;
  out[2] = cosRoll * -sinLon0 - sinRoll * r12;
  out[3] = sinRoll * cosLon0 + cosRoll * r10;
  out[4] = cosRoll * cosLat0;
  out[5] = sinRoll * -sinLon0 + cosRoll * r12;
  out[6] = cosLat0 * sinLon0;
  out[7] = sinLat0;
  out[8] = cosLat0 * cosLon0;
}

export function buildRotationMatrix(
  centerLatDeg: number,
  centerLonDeg: number,
  rollDeg: number = AXIAL_ROLL_DEG
): Mat3 {
  const m = [0, 0, 0, 0, 0, 0, 0, 0, 0];
  writeRotationMatrix(m, centerLatDeg, centerLonDeg, rollDeg);
  return m;
}

export function rotateVec3(
  m: Mat3,
  x: number,
  y: number,
  z: number
): readonly [number, number, number] {
  return [
    m[0] * x + m[1] * y + m[2] * z,
    m[3] * x + m[4] * y + m[5] * z,
    m[6] * x + m[7] * y + m[8] * z,
  ];
}

/** Multiplies by M^T (inverse of an orthogonal rotation matrix). */
export function rotateVec3Transpose(
  m: Mat3,
  x: number,
  y: number,
  z: number
): readonly [number, number, number] {
  return [
    m[0] * x + m[3] * y + m[6] * z,
    m[1] * x + m[4] * y + m[7] * z,
    m[2] * x + m[5] * y + m[8] * z,
  ];
}

export function latLonToViewAt(
  latDeg: number,
  lonDeg: number,
  centerLatDeg: number = REST_LAT_DEG,
  centerLonDeg: number = REST_LON_DEG,
  rollDeg: number = AXIAL_ROLL_DEG
): { x: number; y: number; z: number } {
  const [px, py, pz] = latLonToPlanet(latDeg, lonDeg);
  const [x, y, z] = rotateVec3(
    buildRotationMatrix(centerLatDeg, centerLonDeg, rollDeg),
    px,
    py,
    pz
  );
  return { x, y, z };
}

function settleState(state: GlobeMotionState, nowMs: number, reducedMotion: boolean): void {
  state.velLonDegPerSec = 0;
  state.velLatDegPerSec = 0;
  const atRest =
    Math.abs(state.latDeg - REST_LAT_DEG) < ANGLE_EPS_DEG &&
    Math.abs(shortestAngleDeltaDeg(state.lonDeg, REST_LON_DEG)) < ANGLE_EPS_DEG;

  if (atRest) {
    state.latDeg = REST_LAT_DEG;
    state.lonDeg = REST_LON_DEG;
    state.mode = "rest";
  } else if (reducedMotion) {
    state.mode = "settled";
  } else {
    state.mode = "idle_wait";
    state.idleStartMs = nowMs;
  }
}

export function createGlobeMotionState(): GlobeMotionState {
  return {
    mode: "rest",
    latDeg: REST_LAT_DEG,
    lonDeg: REST_LON_DEG,
    velLonDegPerSec: 0,
    velLatDegPerSec: 0,
    dragStartMs: 0,
    lastMoveMs: 0,
    samples: [],
    idleStartMs: 0,
    returnStartMs: 0,
    returnStartLatDeg: REST_LAT_DEG,
    returnStartLonDeg: REST_LON_DEG,
    returnDeltaLatDeg: 0,
    returnDeltaLonDeg: 0,
  };
}

export function beginDrag(state: GlobeMotionState, nowMs: number): void {
  state.lonDeg = wrapLonDeg(state.lonDeg);
  state.latDeg = clampLatDeg(state.latDeg);
  state.mode = "dragging";
  state.velLonDegPerSec = 0;
  state.velLatDegPerSec = 0;
  state.dragStartMs = nowMs;
  state.lastMoveMs = nowMs;
  state.samples.length = 0;
}

export function moveDrag(
  state: GlobeMotionState,
  dxPx: number,
  dyPx: number,
  radiusPx: number,
  nowMs: number,
  horizontalOnly: boolean
): boolean {
  if (state.mode !== "dragging" || radiusPx <= 0) return false;
  const dLonDeg = -(dxPx / radiusPx) * RAD_TO_DEG;
  const dLatDeg = horizontalOnly ? 0 : (dyPx / radiusPx) * RAD_TO_DEG;
  const nextLat = clampLatDeg(state.latDeg + dLatDeg);
  const actualDLat = nextLat - state.latDeg;

  state.latDeg = nextLat;
  state.lonDeg = wrapLonDeg(state.lonDeg + dLonDeg);
  state.samples.push({ timeMs: nowMs, prevTimeMs: state.lastMoveMs, dLonDeg, dLatDeg: actualDLat });
  state.lastMoveMs = nowMs;

  const cutoff = nowMs - VELOCITY_WINDOW_MS;
  let drop = 0;
  while (drop < state.samples.length && state.samples[drop].timeMs < cutoff) drop++;
  if (drop > 0) state.samples.splice(0, drop);
  return Math.abs(dLonDeg) > 1e-6 || Math.abs(actualDLat) > 1e-6;
}

export function endDrag(
  state: GlobeMotionState,
  nowMs: number,
  options: { reducedMotion: boolean; cancelInertia?: boolean }
): void {
  if (state.mode !== "dragging") return;

  let velLon = 0;
  let velLat = 0;
  if (!options.reducedMotion && !options.cancelInertia && state.samples.length > 0) {
    const cutoff = nowMs - VELOCITY_WINDOW_MS;
    let sumLon = 0;
    let sumLat = 0;
    let firstPrev = -1;
    for (const s of state.samples) {
      if (s.timeMs < cutoff) continue;
      if (firstPrev < 0) firstPrev = s.prevTimeMs;
      sumLon += s.dLonDeg;
      sumLat += s.dLatDeg;
    }
    if (firstPrev >= 0) {
      const spanSec = Math.max(0.016, (nowMs - Math.max(state.dragStartMs, firstPrev)) / 1000);
      velLon = sumLon / spanSec;
      velLat = sumLat / spanSec;
      const speed = Math.hypot(velLon, velLat);
      if (speed > MAX_INERTIA_SPEED_DEG_PER_SEC) {
        const scale = MAX_INERTIA_SPEED_DEG_PER_SEC / speed;
        velLon *= scale;
        velLat *= scale;
      }
    }
  }
  state.samples.length = 0;

  if (!options.reducedMotion && Math.hypot(velLon, velLat) >= INERTIA_STOP_EPS_DEG_PER_SEC) {
    state.velLonDegPerSec = velLon;
    state.velLatDegPerSec = velLat;
    state.mode = "inertia";
    return;
  }
  settleState(state, nowMs, options.reducedMotion);
}

/** Advances inertia, idle wait, and smooth return-to-rest; returns true if orientation changed. */
export function stepGlobeMotion(
  state: GlobeMotionState,
  nowMs: number,
  dtSec: number,
  reducedMotion: boolean
): boolean {
  const dt = dtSec <= 0 ? 0 : dtSec > 0.1 ? 0.1 : dtSec;

  if (reducedMotion) {
    if (state.mode === "inertia" || state.mode === "returning" || state.mode === "idle_wait") {
      settleState(state, nowMs, true);
    }
    return false;
  }
  if (state.mode === "settled") {
    settleState(state, nowMs, false);
    return false;
  }
  if (state.mode === "inertia") {
    if (dt === 0) return false;
    const decay = Math.exp(-INERTIA_DECAY_PER_SEC * dt);
    const factor = (1 - decay) / INERTIA_DECAY_PER_SEC;
    const dLon = state.velLonDegPerSec * factor;
    const prevLat = state.latDeg;
    const nextLat = clampLatDeg(prevLat + state.velLatDegPerSec * factor);

    state.latDeg = nextLat;
    state.lonDeg = wrapLonDeg(state.lonDeg + dLon);
    state.velLonDegPerSec *= decay;
    state.velLatDegPerSec =
      nextLat === MIN_LAT_DEG || nextLat === MAX_LAT_DEG ? 0 : state.velLatDegPerSec * decay;
    if (Math.hypot(state.velLonDegPerSec, state.velLatDegPerSec) < INERTIA_STOP_EPS_DEG_PER_SEC) {
      settleState(state, nowMs, false);
    }
    return Math.abs(state.latDeg - prevLat) > 1e-6 || Math.abs(dLon) > 1e-6;
  }
  if (state.mode === "idle_wait") {
    if (nowMs - state.idleStartMs >= RETURN_IDLE_DELAY_MS) {
      state.mode = "returning";
      state.returnStartMs = nowMs;
      state.returnStartLatDeg = state.latDeg;
      state.returnStartLonDeg = state.lonDeg;
      state.returnDeltaLatDeg = REST_LAT_DEG - state.latDeg;
      state.returnDeltaLonDeg = shortestAngleDeltaDeg(state.lonDeg, REST_LON_DEG);
    }
    return false;
  }
  if (state.mode === "returning") {
    const elapsed = nowMs - state.returnStartMs;
    if (elapsed >= RETURN_DURATION_MS) {
      state.latDeg = REST_LAT_DEG;
      state.lonDeg = REST_LON_DEG;
      state.mode = "rest";
      return true;
    }
    const eased = easeInOutCubic(elapsed <= 0 ? 0 : elapsed / RETURN_DURATION_MS);
    state.latDeg = state.returnStartLatDeg + state.returnDeltaLatDeg * eased;
    state.lonDeg = wrapLonDeg(state.returnStartLonDeg + state.returnDeltaLonDeg * eased);
    return true;
  }
  return false;
}
