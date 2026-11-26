#!/usr/bin/env bash
# Production snapshot build for acceptance by Claude orchestrators (never by agy).
# Usage (run with Bash run_in_background, stop with TaskStop, then check the port is free):
#   .agents/tools/snap-build.sh <port> [path...]
# Builds HEAD's web/ plus an overlay of the listed working-tree paths in /tmp/nm-snap-<port>, so code is verified
# before it is committed. List files, or directories your area owns entirely; a listed path that no longer exists in
# the working tree is deleted from the snapshot. One build at a time across sessions (/tmp/nm-next-build.lock), and
# only while memory allows. Ready when "SERVING" is printed; route sizes (First Load JS) are in <dir>/build.log.
set -euo pipefail
port=${1:?port}; shift
repo=$(git rev-parse --show-toplevel); cd "$repo"
case $port in 3111|4777|43101|43110) echo "port $port is reserved" >&2; exit 1;; esac
dir=/tmp/nm-snap-$port; sha=$(git rev-parse --short HEAD)
rm -rf "$dir" && mkdir -p "$dir"
git archive HEAD web | tar -x -C "$dir"
for p in "$@"; do
  p=${p%/}
  case $p in web|web/node_modules*|web/.next*) echo "refusing overlay $p" >&2; exit 1;; web/*) ;;
    *) echo "skip $p (only web/ is built)" >&2; continue;; esac
  rm -rf "${dir:?}/$p"
  if [ -e "$p" ]; then mkdir -p "$dir/$(dirname "$p")"; cp -a "$p" "$dir/$p"; fi
done
ln -s "$repo/web/node_modules" "$dir/web/node_modules"
cd "$dir/web"

exec 9>/tmp/nm-next-build.lock
echo "HEAD $sha + $# overlay path(s): waiting for build lock..."
flock 9
while :; do
  slice=$(( $(systemctl --user show nm3b.slice -p MemoryCurrent --value) / 1048576 ))
  avail=$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo)
  [ "$slice" -lt 6000 ] && [ "$avail" -gt 3000 ] && break
  echo "$(date +%T) waiting for memory: nm3b.slice ${slice} MB (need <6000), available ${avail} MB (need >3000)"
  sleep 30
done
echo "$(date +%T) building..."
systemd-run --user --scope --quiet --slice=nm3b.slice -p MemoryMax=3G \
  env NEXT_DIST_DIR=.next-snap NEXT_TELEMETRY_DISABLED=1 npx next build > "$dir/build.log" 2>&1 \
  || { exec 9>&-; tail -40 "$dir/build.log"; echo "BUILD FAILED"; exit 1; }
exec 9>&-
cp -r .next-snap/static .next-snap/standalone/.next-snap/static
cp -r public .next-snap/standalone/public
cd .next-snap/standalone
echo "SERVING HEAD $sha + overlay on http://nexusmart.localhost:$port (sites: http://<sub>.nexusmart.localhost:$port)"
exec env PORT="$port" HOSTNAME=localhost NODE_ENV=production NEXT_DIST_DIR=.next-snap GATEWAY_URL=http://127.0.0.1:48080 \
  SITE_ROOT_DOMAIN=nexusmart.localhost COOKIE_SECURE=false \
  systemd-run --user --scope --quiet --slice=nm3b.slice -p MemoryMax=1G node server.js
