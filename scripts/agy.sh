#!/usr/bin/env bash
# Coder agent cho repo này. Model và effort cố định ở đây: đổi một chỗ khi có model mạnh hơn (`agy models`).
exec agy --dangerously-skip-permissions --model claude-opus-5-5-high "$@"
