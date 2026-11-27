// Run: node --test web/src/features/platform/globeMotion.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  AXIAL_ROLL_DEG,
  MAX_LAT_DEG,
  MIN_LAT_DEG,
  REST_LAT_DEG,
  REST_LON_DEG,
  RETURN_DURATION_MS,
  RETURN_IDLE_DELAY_MS,
  beginDrag,
  buildRotationMatrix,
  clampLatDeg,
  createGlobeMotionState,
  endDrag,
  latLonToPlanet,
  latLonToViewAt,
  moveDrag,
  rotateVec3,
  rotateVec3Transpose,
  shortestAngleDeltaDeg,
  stepGlobeMotion,
  wrapLonDeg,
} from "./globeMotion.ts";

function approxEqual(actual, expected, eps = 1e-5, msg) {
  assert.ok(
    Math.abs(actual - expected) <= eps,
    `${msg ? msg + ": " : ""}expected ${actual} to be within ${eps} of ${expected}`
  );
}

test("rest rotation matrix matches legacy latLonToView and maps (13.5, 106) to (0, 0, 1)", () => {
  const center = latLonToViewAt(REST_LAT_DEG, REST_LON_DEG);
  approxEqual(center.x, 0, 1e-6, "center.x");
  approxEqual(center.y, 0, 1e-6, "center.y");
  approxEqual(center.z, 1, 1e-6, "center.z");

  // Compare multiple geographic coordinates against the original explicit formula
  const DEG = Math.PI / 180;
  const sinLat0 = Math.sin(REST_LAT_DEG * DEG);
  const cosLat0 = Math.cos(REST_LAT_DEG * DEG);
  const lon0 = REST_LON_DEG * DEG;
  const sinRoll = Math.sin(AXIAL_ROLL_DEG * DEG);
  const cosRoll = Math.cos(AXIAL_ROLL_DEG * DEG);

  const samples = [
    [21.03, 105.85], // Hanoi
    [10.78, 106.7], // HCMC
    [35.68, 139.69], // Tokyo
    [28.61, 77.21], // Delhi
    [-33.87, 151.21], // Sydney
    [40.71, -74.01], // New York
  ];

  const mRest = buildRotationMatrix(REST_LAT_DEG, REST_LON_DEG, AXIAL_ROLL_DEG);
  for (const [latDeg, lonDeg] of samples) {
    const lat = latDeg * DEG;
    const dLon = lonDeg * DEG - lon0;
    const rx = Math.cos(lat) * Math.sin(dLon);
    const ry = cosLat0 * Math.sin(lat) - sinLat0 * Math.cos(lat) * Math.cos(dLon);
    const rz = sinLat0 * Math.sin(lat) + cosLat0 * Math.cos(lat) * Math.cos(dLon);
    const expectedX = rx * cosRoll - ry * sinRoll;
    const expectedY = rx * sinRoll + ry * cosRoll;
    const expectedZ = rz;

    const actual = latLonToViewAt(latDeg, lonDeg);
    approxEqual(actual.x, expectedX, 1e-6, `x(${latDeg},${lonDeg})`);
    approxEqual(actual.y, expectedY, 1e-6, `y(${latDeg},${lonDeg})`);
    approxEqual(actual.z, expectedZ, 1e-6, `z(${latDeg},${lonDeg})`);

    // Transpose round-trip restores planet vector (orthogonality check)
    const planet = latLonToPlanet(latDeg, lonDeg);
    const view = rotateVec3(mRest, planet[0], planet[1], planet[2]);
    const back = rotateVec3Transpose(mRest, view[0], view[1], view[2]);
    approxEqual(back[0], planet[0], 1e-6);
    approxEqual(back[1], planet[1], 1e-6);
    approxEqual(back[2], planet[2], 1e-6);
  }
});

test("latitude is clamped within ±20° around resting latitude during drag", () => {
  assert.equal(clampLatDeg(REST_LAT_DEG + 50), MAX_LAT_DEG);
  assert.equal(clampLatDeg(REST_LAT_DEG - 50), MIN_LAT_DEG);
  assert.equal(MAX_LAT_DEG, 33.5);
  assert.equal(MIN_LAT_DEG, -6.5);

  const state = createGlobeMotionState();
  beginDrag(state, 0);
  // Large downward drag -> latitude increases but clamps at MAX_LAT_DEG
  moveDrag(state, 0, 1000, 300, 16, false);
  assert.equal(state.latDeg, MAX_LAT_DEG);

  // Large upward drag -> latitude decreases but clamps at MIN_LAT_DEG
  moveDrag(state, 0, -2000, 300, 32, false);
  assert.equal(state.latDeg, MIN_LAT_DEG);

  // Horizontal-only (touch) drag keeps latitude unchanged while changing longitude
  const latBefore = state.latDeg;
  const lonBefore = state.lonDeg;
  moveDrag(state, 150, 400, 300, 48, true);
  assert.equal(state.latDeg, latBefore);
  assert.notEqual(state.lonDeg, lonBefore);
});

test("inertia decays exponentially to zero within ~1s and transitions to idle_wait", () => {
  const state = createGlobeMotionState();
  beginDrag(state, 0);
  for (let i = 1; i <= 5; i++) {
    moveDrag(state, 24, 6, 280, i * 16, false);
  }
  const lonAtRelease = state.lonDeg;
  endDrag(state, 80, { reducedMotion: false });
  assert.equal(state.mode, "inertia");
  assert.ok(Math.abs(state.velLonDegPerSec) > 10, "expected noticeable release velocity");

  let now = 80;
  let movedDuringInertia = false;
  for (let step = 0; step < 90; step++) {
    now += 16;
    const changed = stepGlobeMotion(state, now, 0.016, false);
    if (changed) movedDuringInertia = true;
    if (state.mode !== "inertia") break;
  }

  assert.ok(movedDuringInertia, "globe should continue coasting after release");
  assert.notEqual(state.lonDeg, lonAtRelease);
  assert.equal(state.mode, "idle_wait");
  assert.equal(state.velLonDegPerSec, 0);
  assert.equal(state.velLatDegPerSec, 0);
  assert.ok(now - 80 <= 1250, `inertia should settle in ~1s, took ${now - 80}ms`);
});

test("return-to-rest takes the shortest longitude arc (+200° offset wraps via 160°) and lands at rest", () => {
  // Offset +200° from REST_LON_DEG (106°) is 306° ≡ -54°; shortest path back to 106° is +160° (magnitude 160°, not 200°)
  const offsetPlus200 = wrapLonDeg(REST_LON_DEG + 200);
  assert.equal(offsetPlus200, -54);
  approxEqual(shortestAngleDeltaDeg(offsetPlus200, REST_LON_DEG), 160, 1e-6);

  // Offset -200° from REST_LON_DEG (106°) is -94°; shortest path back to 106° is -160°
  const offsetMinus200 = wrapLonDeg(REST_LON_DEG - 200);
  assert.equal(offsetMinus200, -94);
  approxEqual(shortestAngleDeltaDeg(offsetMinus200, REST_LON_DEG), -160, 1e-6);

  // Simulate full idle_wait -> returning -> rest starting from +200° offset
  const state = createGlobeMotionState();
  state.lonDeg = offsetPlus200;
  state.latDeg = REST_LAT_DEG + 12;
  state.mode = "idle_wait";
  state.idleStartMs = 1000;

  // Still waiting just before 2.5s
  assert.equal(stepGlobeMotion(state, 1000 + RETURN_IDLE_DELAY_MS - 10, 0.016, false), false);
  assert.equal(state.mode, "idle_wait");

  // Starts returning at +2.5s
  stepGlobeMotion(state, 1000 + RETURN_IDLE_DELAY_MS, 0.016, false);
  assert.equal(state.mode, "returning");
  approxEqual(state.returnDeltaLonDeg, 160, 1e-6, "return should take 160° shortest path");
  assert.ok(Math.abs(state.returnDeltaLonDeg) <= 180);

  // Midpoint of return (t = 0.5 -> easeInOutCubic(0.5) = 0.5)
  const returnStart = 1000 + RETURN_IDLE_DELAY_MS;
  stepGlobeMotion(state, returnStart + RETURN_DURATION_MS / 2, 0.016, false);
  approxEqual(state.lonDeg, wrapLonDeg(offsetPlus200 + 80), 1e-5, "midpoint lon");
  approxEqual(state.latDeg, REST_LAT_DEG + 6, 1e-5, "midpoint lat");

  // Completion snaps to exact rest angle
  stepGlobeMotion(state, returnStart + RETURN_DURATION_MS, 0.016, false);
  assert.equal(state.mode, "rest");
  assert.equal(state.latDeg, REST_LAT_DEG);
  assert.equal(state.lonDeg, REST_LON_DEG);
});

test("grabbing during return stops immediately at current angle without jump", () => {
  const state = createGlobeMotionState();
  state.lonDeg = REST_LON_DEG + 90;
  state.latDeg = REST_LAT_DEG - 10;
  state.mode = "idle_wait";
  state.idleStartMs = 0;

  stepGlobeMotion(state, RETURN_IDLE_DELAY_MS, 0.016, false);
  stepGlobeMotion(state, RETURN_IDLE_DELAY_MS + 400, 0.016, false);
  assert.equal(state.mode, "returning");

  const interruptedLat = state.latDeg;
  const interruptedLon = state.lonDeg;
  beginDrag(state, RETURN_IDLE_DELAY_MS + 416);

  assert.equal(state.mode, "dragging");
  approxEqual(state.latDeg, interruptedLat, 1e-6);
  approxEqual(state.lonDeg, interruptedLon, 1e-6);
});

test("reduced motion and cancelInertia disable inertia and auto-return", () => {
  const state = createGlobeMotionState();
  beginDrag(state, 0);
  moveDrag(state, 100, 20, 300, 16, false);
  const droppedLat = state.latDeg;
  const droppedLon = state.lonDeg;
  endDrag(state, 20, { reducedMotion: true });

  assert.equal(state.mode, "settled");
  assert.equal(state.velLonDegPerSec, 0);
  assert.equal(stepGlobeMotion(state, 10000, 0.016, true), false);
  assert.equal(state.latDeg, droppedLat);
  assert.equal(state.lonDeg, droppedLon);

  // pointercancel case: normal motion, but cancelInertia suppresses coasting
  const touchState = createGlobeMotionState();
  beginDrag(touchState, 0);
  moveDrag(touchState, 80, 0, 250, 16, true);
  endDrag(touchState, 20, { reducedMotion: false, cancelInertia: true });
  assert.equal(touchState.mode, "idle_wait");
  assert.equal(touchState.velLonDegPerSec, 0);
});

test("hero arc endpoint sits behind horizon at rest and rotates onto front hemisphere", () => {
  const mRest = buildRotationMatrix(REST_LAT_DEG, REST_LON_DEG, AXIAL_ROLL_DEG);
  const [px, py, pz] = rotateVec3Transpose(mRest, -0.54, 0.72, -0.44);

  // At rest, endpoint z is -0.44 (strictly behind horizon -> landing beacon stays hidden)
  const atRest = rotateVec3(mRest, px, py, pz);
  approxEqual(atRest[2], -0.44, 1e-6, "rest endVz");
  assert.ok(atRest[2] <= 0.04, "hero endpoint must be hidden at rest");

  // Rotating globe ~140° west and tilting north brings endpoint cleanly onto front hemisphere
  const mRotated = buildRotationMatrix(
    REST_LAT_DEG + 18,
    wrapLonDeg(REST_LON_DEG - 140),
    AXIAL_ROLL_DEG
  );
  const rotated = rotateVec3(mRotated, px, py, pz);
  assert.ok(rotated[2] > 0.5, `expected rotated endVz > 0.5, got ${rotated[2]}`);
});
