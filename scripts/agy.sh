#!/usr/bin/env bash
# Coder agent cho repo này. Chỉ dùng model Gemini mới nhất, mức suy nghĩ cao nhất: đổi một chỗ ở đây khi `agy models` có Gemini mới hơn.
# Mỗi phiên agy có một codebase-memory tự index lại khi file đổi; nhiều phiên song song cùng index bằng mọi nhân làm load > 100.
export CBM_WORKERS=1
exec agy --dangerously-skip-permissions --model gemini-4-neon-xhigh "$@"
