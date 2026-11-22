#!/usr/bin/env bash
set -euo pipefail

PATH="$HOME/.local/bin:$HOME/go/bin:$HOME/.cargo/bin:$PATH"

ROOT="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
ROOT="$(realpath -m "$ROOT")"

input="$(cat)"
[[ -z "$input" ]] && exit 0

raw_path="$(printf '%s' "$input" | jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' 2>/dev/null || true)"
[[ -z "$raw_path" ]] && exit 0

if [[ "$raw_path" = /* ]]; then
  abs_path="$(realpath -m "$raw_path")"
else
  abs_path="$(realpath -m "$ROOT/$raw_path")"
fi

if [[ "$abs_path" != "$ROOT/"* ]]; then
  exit 0
fi

rel="${abs_path#"$ROOT/"}"
base="$(basename "$rel")"

# 1. Chặn .env và .env.* ở mọi thư mục, trừ .env.example.
if [[ ( "$base" == ".env" || "$base" == .env.* ) && "$base" != ".env.example" ]]; then
  echo "Chặn sửa '$rel': không được sửa file .env hoặc .env.* (chứa cấu hình cục bộ/secret). Nếu thêm biến môi trường mới, hãy sửa .env.example." >&2
  exit 2
fi

# 2. Chặn sửa trực tiếp code sinh từ Protobuf trong gen/go/.
if [[ "$rel" == gen/go/* ]]; then
  echo "Chặn sửa '$rel': code trong gen/go/ được sinh tự động từ Protobuf. Hãy sửa file trong proto/ rồi chạy 'make proto'." >&2
  exit 2
fi

# 3. Chặn file đã tồn tại có cả "Code generated" và "DO NOT EDIT" trong 5 dòng đầu.
if [[ -f "$abs_path" ]]; then
  header="$(head -n 5 "$abs_path" 2>/dev/null | tr -d '\0' || true)"
  if [[ "$header" == *"Code generated"* && "$header" == *"DO NOT EDIT"* ]]; then
    if [[ "$header" == *"sqlc"* && "$rel" =~ ^services/([^/]+)/ ]]; then
      echo "Chặn sửa '$rel': code sinh tự động bởi sqlc. Hãy sửa queries/*.sql hoặc migrations/*.sql rồi chạy 'cd services/${BASH_REMATCH[1]} && sqlc generate'." >&2
    elif [[ "$header" == *"sqlc"* ]]; then
      echo "Chặn sửa '$rel': code sinh tự động bởi sqlc. Hãy sửa queries/*.sql hoặc migrations/*.sql rồi chạy 'sqlc generate'." >&2
    elif [[ "$header" == *"protoc"* ]]; then
      echo "Chặn sửa '$rel': code sinh tự động từ Protobuf. Hãy sửa file trong proto/ rồi chạy 'make proto'." >&2
    else
      echo "Chặn sửa '$rel': file code sinh tự động (Code generated / DO NOT EDIT). Hãy sửa file nguồn và sinh lại (sqlc generate hoặc make proto)." >&2
    fi
    exit 2
  fi
fi

# 4. Chặn sửa migration đã merge trên nhánh main (forward-only).
if [[ "$rel" =~ ^services/[^/]+/migrations/[^/]+\.sql$ || "$rel" =~ ^services/promotion/internal/store/migrations/[^/]+\.sql$ ]]; then
  if git -C "$ROOT" cat-file -e "main:$rel" 2>/dev/null; then
    echo "Chặn sửa '$rel': file migration đã có trên nhánh main (forward-only; Go bỏ qua file đã chạy, sqlx báo lỗi checksum). Hãy tạo file migration mới." >&2
    exit 2
  fi
fi

exit 0
