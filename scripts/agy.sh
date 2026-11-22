#!/usr/bin/env bash
# Coder agent cho repo này. Chỉ dùng model Gemini mới nhất, mức suy nghĩ cao nhất: đổi một chỗ ở đây khi `agy models` có Gemini mới hơn.
exec agy --dangerously-skip-permissions --model gemini-4-neon-xhigh "$@"
