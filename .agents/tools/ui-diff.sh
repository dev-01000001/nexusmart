#!/usr/bin/env bash
# Pixel-compare two screenshot folders with the same file names (e.g. two ui-shots.mjs runs on different ports).
#   .agents/tools/ui-diff.sh <base-dir> <cand-dir> [out-dir]
#   MASK="0:ih-72:72:72;..." blacks out regions in both images first (ffmpeg drawbox x:y:w:h; e.g. Next dev badge).
# Writes <out-dir>/report.txt (default <cand-dir>-diff) sorted worst first, plus an amplified diff PNG for each
# non-identical pair (changed pixels white over the dimmed base). YAVG/YMAX are 0-255 luma; 0 = identical.
set -euo pipefail
base=${1:?base dir}; cand=${2:?cand dir}; out=${3:-${cand%/}-diff}
mkdir -p "$out"
pre=""
if [ -n "${MASK:-}" ]; then
  IFS=';' read -ra boxes <<< "$MASK"
  for b in "${boxes[@]}"; do IFS=: read -r x y w h <<< "$b"; pre+="drawbox=x=$x:y=$y:w=$w:h=$h:color=black:t=fill,"; done
fi
rows=()
for a in "$base"/*.png; do
  f=$(basename "$a"); b="$cand/$f"
  [ -f "$b" ] || { rows+=("MISSING  $f"); continue; }
  sa=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0:s=x "$a")
  sb=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0:s=x "$b")
  if [ "$sa" != "$sb" ]; then rows+=("SIZE     $f $sa vs $sb"); continue; fi
  m=$(ffmpeg -v error -i "$a" -i "$b" -filter_complex \
    "[0]${pre}format=yuv420p[x];[1]${pre}format=yuv420p[y];[x][y]blend=all_mode=difference,signalstats,metadata=print:file=-" \
    -f null - | grep -oE "YAVG=[0-9.]+|YMAX=[0-9.]+" | head -2 | tr '\n' ' ')
  yavg=$(sed -E 's/.*YAVG=([0-9.]+).*/\1/' <<< "$m"); ymax=$(sed -E 's/.*YMAX=([0-9.]+).*/\1/' <<< "$m")
  if [ "$yavg" = "0" ] && [ "$ymax" = "0" ]; then rows+=("SAME     $f"); continue; fi
  ffmpeg -v error -y -i "$a" -i "$b" -filter_complex \
    "[0]${pre}format=gray[x];[1]${pre}format=gray[y];[x][y]blend=all_mode=difference,lut=y='if(gt(val,0),255,0)'[d];[x]lut=y='val/3'[g];[g][d]blend=all_mode=lighten" \
    "$out/$f"
  rows+=("$(printf 'DIFF     %-48s YAVG=%-10s YMAX=%s' "$f" "$yavg" "$ymax")")
done
{
  printf '%s\n' "${rows[@]}" | grep -E "^(MISSING|SIZE)" || true
  printf '%s\n' "${rows[@]}" | grep "^DIFF" | awk '{split($3, a, "="); print a[2] "\t" $0}' | sort -g -r | cut -f2- || true
  echo
  echo "same=$(printf '%s\n' "${rows[@]}" | grep -c '^SAME' || true) diff=$(printf '%s\n' "${rows[@]}" | grep -c '^DIFF' || true) size=$(printf '%s\n' "${rows[@]}" | grep -c '^SIZE' || true) missing=$(printf '%s\n' "${rows[@]}" | grep -c '^MISSING' || true)"
} | tee "$out/report.txt"
