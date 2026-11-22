#!/usr/bin/env bash
set -euo pipefail

PATH="$HOME/.local/bin:$HOME/go/bin:$HOME/.cargo/bin:$PATH"

ROOT="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
ROOT="$(realpath -m "$ROOT" 2>/dev/null || printf '%s' "$ROOT")"

input="$(cat 2>/dev/null || true)"
[[ -z "$input" ]] && exit 0

if ! command -v jq >/dev/null 2>&1; then
  echo "Cảnh báo format.sh: thiếu jq trên PATH, bỏ qua format." >&2
  exit 0
fi

raw_path="$(printf '%s' "$input" | jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' 2>/dev/null || true)"
[[ -z "$raw_path" ]] && exit 0

if [[ "$raw_path" = /* ]]; then
  abs_path="$(realpath -m "$raw_path" 2>/dev/null || printf '%s' "$raw_path")"
else
  abs_path="$(realpath -m "$ROOT/$raw_path" 2>/dev/null || printf '%s' "$ROOT/$raw_path")"
fi

[[ -f "$abs_path" ]] || exit 0

rel=""
if [[ "$abs_path" == "$ROOT/"* ]]; then
  rel="${abs_path#"$ROOT/"}"
fi

case "$abs_path" in
  *.go)
    if command -v goimports >/dev/null 2>&1; then
      goimports -w "$abs_path" || echo "Cảnh báo format.sh: goimports lỗi với '$abs_path'." >&2
    elif command -v gofmt >/dev/null 2>&1; then
      gofmt -w "$abs_path" || echo "Cảnh báo format.sh: gofmt lỗi với '$abs_path'." >&2
    else
      echo "Cảnh báo format.sh: không tìm thấy goimports hay gofmt trên PATH." >&2
    fi
    ;;
  *.rs)
    if command -v rustfmt >/dev/null 2>&1; then
      rustfmt --edition 2021 "$abs_path" || echo "Cảnh báo format.sh: rustfmt lỗi với '$abs_path'." >&2
    else
      echo "Cảnh báo format.sh: không tìm thấy rustfmt trên PATH." >&2
    fi
    ;;
  *.ts|*.tsx|*.js|*.mjs|*.css)
    if [[ "$rel" == web/* && "$rel" != */node_modules/* && "$rel" != */.next/* ]]; then
      prettier_bin="$ROOT/web/node_modules/.bin/prettier"
      if [[ -x "$prettier_bin" ]]; then
        "$prettier_bin" --config "$ROOT/web/.prettierrc" --log-level warn --write "$abs_path" \
          || echo "Cảnh báo format.sh: prettier lỗi với '$rel'." >&2
      else
        echo "Cảnh báo format.sh: chưa có web/node_modules; chạy 'cd web && npm ci' để bật Prettier." >&2
      fi
    fi
    ;;
esac

exit 0
