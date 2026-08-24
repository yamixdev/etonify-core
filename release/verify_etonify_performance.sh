#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repository_root}"

# shellcheck disable=SC1091
source release/ETONIFY_PERFORMANCE_BASELINE

go test -count=1 -run '^TestXHTTPResourceSoak$' ./transport/v2rayxhttp

benchmark_output="$(mktemp)"
trap 'rm -f "${benchmark_output}"' EXIT
go test \
  -run '^$' \
  -bench '^BenchmarkEncryptedRoundTrip$' \
  -benchmem \
  -benchtime "${ETONIFY_BENCHTIME:-30x}" \
  -count "${ETONIFY_BENCHMARK_RUNS:-3}" \
  ./protocol/vless/encryption | tee "${benchmark_output}"

awk \
  -v max_ns="${VLESS_MAX_NS_PER_OP}" \
  -v max_bytes="${VLESS_MAX_BYTES_PER_OP}" \
  -v max_allocs="${VLESS_MAX_ALLOCS_PER_OP}" '
  /^BenchmarkEncryptedRoundTrip-/ {
    found = 1
    ns = $3 + 0
    bytes = $5 + 0
    allocs = $7 + 0
    if (ns > max_ns || bytes > max_bytes || allocs > max_allocs) {
      printf "performance gate failed: %.0f ns/op, %.0f B/op, %.0f allocs/op\n", ns, bytes, allocs > "/dev/stderr"
      failed = 1
    }
  }
  END {
    if (!found) {
      print "performance gate failed: benchmark result was not found" > "/dev/stderr"
      exit 2
    }
    if (failed) {
      exit 1
    }
  }
' "${benchmark_output}"
