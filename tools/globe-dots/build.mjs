#!/usr/bin/env node
// Equal-area spherical sampling of Natural Earth 110m land polygons.
// Zero dependencies — requires Node 24+.
//
// Usage:
//   node tools/globe-dots/build.mjs <input.geojson> [output.json] [--step=1.25]

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { gzipSync } from "node:zlib";

const DEFAULT_OUTPUT = "web/public/globe/land-dots.json";
const DEFAULT_STEP_DEG = 1.2;

function parseArgs(argv) {
  let inputPath = "";
  let outputPath = DEFAULT_OUTPUT;
  let step = DEFAULT_STEP_DEG;
  const positional = [];

  for (const arg of argv) {
    if (arg.startsWith("--step=")) {
      const parsed = Number.parseFloat(arg.slice("--step=".length));
      if (!Number.isFinite(parsed) || parsed < 0.5 || parsed > 5) {
        throw new Error(`Invalid --step value: ${arg}`);
      }
      step = parsed;
    } else if (arg === "-h" || arg === "--help") {
      console.log(
        "Usage: node tools/globe-dots/build.mjs <ne_110m_land.geojson> [output.json] [--step=1.25]"
      );
      process.exit(0);
    } else {
      positional.push(arg);
    }
  }

  if (positional.length < 1) {
    throw new Error(
      "Missing input GeoJSON path. Download ne_110m_land.geojson to /tmp and pass its path."
    );
  }
  inputPath = positional[0];
  if (positional.length >= 2) {
    outputPath = positional[1];
  }

  return { inputPath, outputPath, step };
}

function computeBBox(ring) {
  let minLon = Infinity;
  let maxLon = -Infinity;
  let minLat = Infinity;
  let maxLat = -Infinity;
  for (let i = 0; i < ring.length; i++) {
    const lon = ring[i][0];
    const lat = ring[i][1];
    if (lon < minLon) minLon = lon;
    if (lon > maxLon) maxLon = lon;
    if (lat < minLat) minLat = lat;
    if (lat > maxLat) maxLat = lat;
  }
  return { minLon, maxLon, minLat, maxLat };
}

function extractPolygons(geojson) {
  const polygons = [];
  const features =
    geojson.type === "FeatureCollection"
      ? geojson.features
      : geojson.type === "Feature"
        ? [geojson]
        : [{ geometry: geojson }];

  for (const feature of features) {
    const geom = feature?.geometry;
    if (!geom) continue;
    const rawPolys =
      geom.type === "Polygon"
        ? [geom.coordinates]
        : geom.type === "MultiPolygon"
          ? geom.coordinates
          : [];

    for (const rings of rawPolys) {
      if (!rings || rings.length === 0 || rings[0].length < 3) continue;
      const outer = rings[0];
      const holes = rings.slice(1).filter((r) => r.length >= 3);
      polygons.push({
        bbox: computeBBox(outer),
        outer,
        holes: holes.map((ring) => ({ ring, bbox: computeBBox(ring) })),
      });
    }
  }
  return polygons;
}

// Even-odd ray casting algorithm in (lon, lat) space.
function pointInRing(lon, lat, ring) {
  let inside = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const xi = ring[i][0];
    const yi = ring[i][1];
    const xj = ring[j][0];
    const yj = ring[j][1];

    const intersects = yi > lat !== yj > lat && lon < ((xj - xi) * (lat - yi)) / (yj - yi) + xi;
    if (intersects) inside = !inside;
  }
  return inside;
}

function pointInBBox(lon, lat, bbox) {
  return lon >= bbox.minLon && lon <= bbox.maxLon && lat >= bbox.minLat && lat <= bbox.maxLat;
}

function isLand(lon, lat, polygons) {
  for (let i = 0; i < polygons.length; i++) {
    const poly = polygons[i];
    if (!pointInBBox(lon, lat, poly.bbox)) continue;
    if (!pointInRing(lon, lat, poly.outer)) continue;

    let inHole = false;
    for (let h = 0; h < poly.holes.length; h++) {
      const hole = poly.holes[h];
      if (pointInBBox(lon, lat, hole.bbox) && pointInRing(lon, lat, hole.ring)) {
        inHole = true;
        break;
      }
    }
    if (!inHole) return true;
  }
  return false;
}

function round1(value) {
  const rounded = Math.round(value * 10) / 10;
  return rounded === 0 ? 0 : rounded;
}

function sampleLandDots(polygons, stepDeg) {
  const dots = [];
  const halfStep = stepDeg / 2;
  let rowIndex = 0;

  for (let lat = -90 + halfStep; lat <= 90 - halfStep; lat += stepDeg, rowIndex++) {
    const cosLat = Math.cos((lat * Math.PI) / 180);
    const nLon = Math.max(1, Math.round((360 * cosLat) / stepDeg));
    const dLon = 360 / nLon;
    // Offset alternate latitude bands by half a cell for a natural staggered stipple.
    const stagger = (rowIndex & 1) === 1 ? 0.5 : 0;

    for (let j = 0; j < nLon; j++) {
      let lon = -180 + (j + stagger) * dLon;
      if (lon >= 180) lon -= 360;
      if (isLand(lon, lat, polygons)) {
        dots.push([round1(lat), round1(lon)]);
      }
    }
  }
  return dots;
}

function main() {
  const { inputPath, outputPath, step } = parseArgs(process.argv.slice(2));
  const rawText = readFileSync(resolve(inputPath), "utf8");
  const geojson = JSON.parse(rawText);
  const polygons = extractPolygons(geojson);
  const dots = sampleLandDots(polygons, step);

  const json = JSON.stringify(dots);
  const absOut = resolve(outputPath);
  mkdirSync(dirname(absOut), { recursive: true });
  writeFileSync(absOut, json, "utf8");

  const rawBytes = Buffer.byteLength(json, "utf8");
  const gzipBytes = gzipSync(json, { level: 9 }).length;

  console.log(
    `Generated ${dots.length} land dots (step=${step} deg) -> ${outputPath}\n` +
      `Size: ${rawBytes} B raw (${(rawBytes / 1024).toFixed(2)} kB), ` +
      `${gzipBytes} B gzip (${(gzipBytes / 1024).toFixed(2)} kB)`
  );
}

main();
