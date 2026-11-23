#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${COVERAGE_TARGET:-60}"
ENFORCE="${COVERAGE_ENFORCE:-0}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# Summarize a Go coverprofile: prints total%, effective% (excluding generated db/cmd),
# and top 5 largest uncovered files by missed statements.
summarize_go_profile() {
	local svc="$1"
	local profile="$2"
	local target="$3"

	awk -v svc="$svc" -v target="$target" '
	NR == 1 && /^mode:/ { next }
	{
		# With -coverpkg=./..., the same block key ($1 = file:start,end) appears once per
		# tested package. Merge counts across packages exactly like `go tool cover -func`.
		key = $1
		stmts = $2 + 0
		cnt = $3 + 0
		if (!(key in block_stmts)) {
			split(key, a, ":")
			block_file[key] = a[1]
			block_stmts[key] = stmts
		}
		block_cnt[key] += cnt
	}
	END {
		for (key in block_stmts) {
			file = block_file[key]
			stmts = block_stmts[key]
			cnt = block_cnt[key]

			total += stmts
			file_total[file] += stmts
			if (cnt > 0) {
				covered += stmts
				file_cov[file] += stmts
			} else {
				file_miss[file] += stmts
			}

			is_gen = (file ~ /\/cmd\// || file ~ /\/internal\/db\/.*\.sql\.go$/ || file ~ /\/internal\/db\/(db|models)\.go$/)
			if (!is_gen) {
				eff_total += stmts
				if (cnt > 0) eff_cov += stmts
			}
		}

		pct = (total > 0) ? (covered * 100.0 / total) : 0.0
		eff_pct = (eff_total > 0) ? (eff_cov * 100.0 / eff_total) : 0.0
		status = (pct >= target) ? "PASS" : "BELOW_TARGET"
		printf "  %-10s total=%.1f%% (%d/%d stmts) | non-gen=%.1f%% (%d/%d stmts) | target=%s%% [%s]\n",
			svc, pct, covered, total, eff_pct, eff_cov, eff_total, target, status

		if (pct < target) {
			print "    Top 5 uncovered files (by missed statements):"
			n = 0
			for (f in file_miss) {
				if (file_miss[f] > 0) {
					n++
					files[n] = f
				}
			}
			for (i = 1; i <= n && i <= 5; i++) {
				best = i
				for (j = i + 1; j <= n; j++) {
					if (file_miss[files[j]] > file_miss[files[best]] ||
					    (file_miss[files[j]] == file_miss[files[best]] && files[j] < files[best])) {
						best = j
					}
				}
				tmp = files[i]; files[i] = files[best]; files[best] = tmp
				f = files[i]
				fpct = (file_total[f] > 0) ? (file_cov[f] * 100.0 / file_total[f]) : 0.0
				short_f = f
				sub(/^github\.com\/nexusmart\/core\//, "", short_f)
				printf "      %d. %s — %d/%d stmts uncovered (%.1f%% covered)\n",
					i, short_f, file_miss[f], file_total[f], fpct
			}
		}
	}
	' "$profile"
}

# Optional unit-test mode: parse an existing Go coverprofile without running tests.
if [[ "${1:-}" == "--summarize-go-profile" ]]; then
	summarize_go_profile "${2:?service required}" "${3:?profile required}" "$TARGET"
	exit 0
fi

# Report top 5 largest production Rust source files in services/payment/src/ (excluding #[cfg(test)]).
report_rust_largest_files() {
	local src_dir="$1"
	echo "    Top 5 largest production Rust files (production LOC vs unit-test blocks):"
	awk '
	FNR == 1 {
		in_test = 0
		file = FILENAME
		sub(/^.*services\/payment\//, "services/payment/", file)
		files[++n] = file
	}
	/^[[:space:]]*#\[cfg\(test\)\]/ { in_test = 1 }
	{
		if (in_test) {
			test_loc[file]++
		} else if ($0 !~ /^[[:space:]]*$/ && $0 !~ /^[[:space:]]*\/\//) {
			prod_loc[file]++
		}
	}
	END {
		for (i = 1; i <= n && i <= 5; i++) {
			best = i
			for (j = i + 1; j <= n; j++) {
				if (prod_loc[files[j]] > prod_loc[files[best]]) {
					best = j
				}
			}
			tmp = files[i]; files[i] = files[best]; files[best] = tmp
			f = files[i]
			printf "      %d. %s — %d prod LOC, %d inline test LOC\n", i, f, prod_loc[f] + 0, test_loc[f] + 0
		}
	}
	' "$src_dir"/*.rs
}

echo "==> NexusMart Coverage Report (target >= ${TARGET}% for order, promotion, payment)"
below=0

for svc in order promotion; do
	cov_file="$TMP_DIR/${svc}.cov"
	(cd "$ROOT_DIR/services/$svc" && go test -coverpkg=./... -coverprofile="$cov_file" ./... >/dev/null)
	out="$(summarize_go_profile "$svc" "$cov_file" "$TARGET")"
	echo "$out"
	if grep -q '\[BELOW_TARGET\]' <<<"$out"; then
		below=1
	fi
done

# Rust payment coverage: use whichever local coverage tool is installed (no new deps).
if (cd "$ROOT_DIR/services/payment" && cargo llvm-cov --version >/dev/null 2>&1); then
	rust_summary="$(cd "$ROOT_DIR/services/payment" && cargo llvm-cov --summary-only 2>&1)"
	rust_pct="$(awk '/^TOTAL/ { for (i=NF; i>=1; i--) if ($i ~ /%$/) { sub(/%/, "", $i); print $i; exit } }' <<<"$rust_summary")"
	rust_status="$(awk -v p="${rust_pct:-0}" -v t="$TARGET" 'BEGIN { print (p + 0 >= t + 0) ? "PASS" : "BELOW_TARGET" }')"
	printf "  %-10s total=%s%% (cargo-llvm-cov) | target=%s%% [%s]\n" "payment" "${rust_pct:-0.0}" "$TARGET" "$rust_status"
	if [[ "$rust_status" == "BELOW_TARGET" ]]; then
		below=1
		report_rust_largest_files "$ROOT_DIR/services/payment/src"
	fi
elif (cd "$ROOT_DIR/services/payment" && cargo tarpaulin --version >/dev/null 2>&1); then
	rust_summary="$(cd "$ROOT_DIR/services/payment" && cargo tarpaulin --skip-clean --out Stdout 2>&1)"
	rust_pct="$(awk '/coverage/ { for (i=1; i<=NF; i++) if ($i ~ /%$/) { sub(/%/, "", $i); print $i; exit } }' <<<"$rust_summary")"
	rust_status="$(awk -v p="${rust_pct:-0}" -v t="$TARGET" 'BEGIN { print (p + 0 >= t + 0) ? "PASS" : "BELOW_TARGET" }')"
	printf "  %-10s total=%s%% (cargo-tarpaulin) | target=%s%% [%s]\n" "payment" "${rust_pct:-0.0}" "$TARGET" "$rust_status"
	if [[ "$rust_status" == "BELOW_TARGET" ]]; then
		below=1
		report_rust_largest_files "$ROOT_DIR/services/payment/src"
	fi
else
	test_out="$(cd "$ROOT_DIR/services/payment" && cargo test 2>&1)"
	passed_count="$(awk '/test result: ok\./ { sum += $4 } END { print sum + 0 }' <<<"$test_out")"
	printf "  %-10s total=N/A (cargo test: %s passed; no local cargo-llvm-cov/cargo-tarpaulin installed) | target=%s%% [UNMEASURED]\n" \
		"payment" "$passed_count" "$TARGET"
	report_rust_largest_files "$ROOT_DIR/services/payment/src"
	below=1
fi

if [[ "$ENFORCE" == "1" && "$below" -ne 0 ]]; then
	echo "Coverage threshold (${TARGET}%) not met (COVERAGE_ENFORCE=1)." >&2
	exit 1
fi
