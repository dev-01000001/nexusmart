#!/usr/bin/env bash
# Coder agent cho repo này. Chỉ dùng model Gemini mới nhất, mức suy nghĩ cao nhất: đổi một chỗ ở đây khi `agy models` có Gemini mới hơn.
# Mỗi phiên agy có một codebase-memory tự index lại khi file đổi; nhiều phiên song song cùng index bằng mọi nhân làm load > 100.
export CBM_WORKERS=1
# Máy ít RAM: 2026-11-24 bốn agent cùng build làm OOM killer giết dịch vụ khác. Giới hạn build song song và nhận bị giết trước.
export GOFLAGS="${GOFLAGS:--p=4}" CARGO_BUILD_JOBS="${CARGO_BUILD_JOBS:-4}"
exec choom -n 500 -- agy --dangerously-skip-permissions --model gemini-4-neon-xhigh "$@"
