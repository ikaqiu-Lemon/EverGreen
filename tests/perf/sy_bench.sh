#!/usr/bin/env bash
# Reproducible .sy acceptance benchmark for T-008 A6.
#
# The immutable workload and thresholds live in sy_thresholds.json. The harness
# generates the corpus twice, checks its digest, then records nearest-rank
# P50/P95/P99 and maximum RSS for each production-facing operation.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${REPO_ROOT}/tests/perf/sy_thresholds.json"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-sy-perf.XXXXXX")"
OUTPUT="${SY_PERF_OUTPUT:-${WORK}/results}"
READ_ROOT="${WORK}/read-corpus"
READ_DUP_ROOT="${WORK}/read-corpus-duplicate"
WRITE_ROOT="${WORK}/write-corpus"
PROBE_ROOT="${WORK}/write-probe-1000"
BENCH="${WORK}/sy-bench"

cleanup() {
  rm -rf "${WORK}"
}
trap cleanup EXIT

json_get() {
  python3 - "${CONFIG}" "$1" <<'PY'
import json
import sys

value = json.load(open(sys.argv[1], encoding="utf-8"))
for part in sys.argv[2].split("."):
    value = value[part]
print(value)
PY
}

READ_CLAIMS="$(json_get corpus.read.claims)"
READ_RELATIONS="$(json_get corpus.read.relations)"
WRITE_CLAIMS="$(json_get corpus.write.claims)"
WRITE_RELATIONS="$(json_get corpus.write.relations)"
PROBE_CLAIMS="$(json_get corpus.write_capacity_probes.intermediate.claims)"
PROBE_RELATIONS="$(json_get corpus.write_capacity_probes.intermediate.relations)"
WARMUP="$(json_get sampling.warmup)"
SAMPLES="$(json_get sampling.samples)"
METRICS=(startup_scan claims_query relation_query note_edit plan_apply index_rebuild)

mkdir -p "${OUTPUT}"
BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain=v1)"

printf 'Building .sy performance harness...\n'
CGO_ENABLED=0 go build -o "${BENCH}" ./tests/lib/sybench

printf 'Generating deterministic read corpus A (%s Claims / %s edges)...\n' \
  "${READ_CLAIMS}" "${READ_RELATIONS}"
"${BENCH}" -mode prepare -root "${READ_ROOT}" -claims "${READ_CLAIMS}" \
  -relations "${READ_RELATIONS}" >"${OUTPUT}/read-corpus.json"

printf 'Generating deterministic read corpus B...\n'
"${BENCH}" -mode prepare -root "${READ_DUP_ROOT}" -claims "${READ_CLAIMS}" \
  -relations "${READ_RELATIONS}" >"${OUTPUT}/read-corpus-duplicate.json"

python3 - "${OUTPUT}/read-corpus.json" "${OUTPUT}/read-corpus-duplicate.json" <<'PY'
import json
import sys

left = json.load(open(sys.argv[1], encoding="utf-8"))
right = json.load(open(sys.argv[2], encoding="utf-8"))
keys = ("claims", "relations", "locations", "sy_files", "sy_bytes", "digest")
different = [key for key in keys if left[key] != right[key]]
if different:
    raise SystemExit("corpus mismatch: " + ", ".join(different))
print("Corpus digest:", left["digest"])
PY

printf 'Generating percentile write corpus (%s Claims / %s edges)...\n' \
  "${WRITE_CLAIMS}" "${WRITE_RELATIONS}"
"${BENCH}" -mode prepare -root "${WRITE_ROOT}" -claims "${WRITE_CLAIMS}" \
  -relations "${WRITE_RELATIONS}" >"${OUTPUT}/write-corpus.json"
"${BENCH}" -mode init-git -root "${WRITE_ROOT}" -claims "${WRITE_CLAIMS}" \
  -relations "${WRITE_RELATIONS}" >"${OUTPUT}/write-git.json"

printf 'Generating intermediate write capacity probe (%s Claims / %s edges)...\n' \
  "${PROBE_CLAIMS}" "${PROBE_RELATIONS}"
"${BENCH}" -mode prepare -root "${PROBE_ROOT}" -claims "${PROBE_CLAIMS}" \
  -relations "${PROBE_RELATIONS}" >"${OUTPUT}/write-probe-1000-corpus.json"
"${BENCH}" -mode init-git -root "${PROBE_ROOT}" -claims "${PROBE_CLAIMS}" \
  -relations "${PROBE_RELATIONS}" >"${OUTPUT}/write-probe-1000-git.json"

"${BENCH}" -mode init-git -root "${READ_DUP_ROOT}" -claims "${READ_CLAIMS}" \
  -relations "${READ_RELATIONS}" >"${OUTPUT}/write-probe-10000-git.json"

for metric in "${METRICS[@]}"; do
  root="${READ_ROOT}"
  claims="${READ_CLAIMS}"
  relations="${READ_RELATIONS}"
  if [ "${metric}" = "plan_apply" ]; then
    root="${WRITE_ROOT}"
    claims="${WRITE_CLAIMS}"
    relations="${WRITE_RELATIONS}"
  fi
  printf 'Sampling %-16s (%s warmup + %s recorded)...\n' "${metric}" "${WARMUP}" "${SAMPLES}"
  "${BENCH}" -mode "${metric}" -root "${root}" -claims "${claims}" -relations "${relations}" \
    -warmup "${WARMUP}" -samples "${SAMPLES}" >"${OUTPUT}/${metric}.json"
done

printf 'Sampling plan/apply capacity probes (one real Git operation each)...\n'
"${BENCH}" -mode plan_apply -root "${PROBE_ROOT}" -claims "${PROBE_CLAIMS}" \
  -relations "${PROBE_RELATIONS}" -warmup 0 -samples 1 >"${OUTPUT}/plan_apply_capacity_1000.json"
"${BENCH}" -mode plan_apply -root "${READ_DUP_ROOT}" -claims "${READ_CLAIMS}" \
  -relations "${READ_RELATIONS}" -warmup 0 -samples 1 >"${OUTPUT}/plan_apply_capacity_10000.json"

"${BENCH}" -mode inspect -root "${READ_ROOT}" >"${OUTPUT}/read-corpus-after.json"

AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain=v1)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '%s\n' "Source repository status changed during benchmark." >&2
  diff <(printf '%s\n' "${BEFORE_STATUS}") <(printf '%s\n' "${AFTER_STATUS}") >&2 || true
  exit 1
fi

python3 - "${CONFIG}" "${OUTPUT}" <<'PY'
import json
import os
import sys

config_path, output = sys.argv[1:3]
config = json.load(open(config_path, encoding="utf-8"))
metrics = [
    "startup_scan", "claims_query", "relation_query",
    "note_edit", "plan_apply", "index_rebuild",
]
corpus_before = json.load(open(os.path.join(output, "read-corpus.json"), encoding="utf-8"))
corpus_after = json.load(open(os.path.join(output, "read-corpus-after.json"), encoding="utf-8"))
if corpus_before["digest"] != corpus_after["digest"]:
    raise SystemExit("read corpus changed during benchmark")

results = {}
failed = []
print()
print(f'{"metric":18} {"p50 ms":>10} {"p95 ms":>10} {"p99 ms":>10} {"RSS MiB":>10} result')
for metric in metrics:
    result = json.load(open(os.path.join(output, metric + ".json"), encoding="utf-8"))
    threshold = config["thresholds"][metric]
    rss_mib = result["max_rss_bytes"] / 1024 / 1024
    checks = [
        result["p95_ms"] <= threshold["p95_ms"],
        result["p99_ms"] <= threshold["p99_ms"],
        rss_mib <= threshold["max_rss_mib"],
        result["samples"] == config["sampling"]["samples"],
    ]
    status = "PASS" if all(checks) else "FAIL"
    if status == "FAIL":
        failed.append(metric)
    result["status"] = status
    result["threshold"] = threshold
    results[metric] = result
    print(
        f'{metric:18} {result["p50_ms"]:10.3f} {result["p95_ms"]:10.3f} '
        f'{result["p99_ms"]:10.3f} {rss_mib:10.1f} {status}'
    )

capacity = []
plan_budget = config["thresholds"]["plan_apply"]["p99_ms"]
for claims in (1000, 10000):
    result = json.load(open(
        os.path.join(output, f"plan_apply_capacity_{claims}.json"), encoding="utf-8"
    ))
    result["status"] = "PASS" if result["max_ms"] <= plan_budget else "FAIL"
    result["p99_budget_ms"] = plan_budget
    if result["status"] == "FAIL":
        failed.append(f"plan_apply_capacity_{claims}")
    capacity.append(result)

print()
print(f'{"plan/apply capacity":18} {"single ms":>10} {"budget ms":>10} result')
for result in capacity:
    print(
        f'{str(result["claims"]) + " Claims":18} {result["max_ms"]:10.3f} '
        f'{result["p99_budget_ms"]:10.0f} {result["status"]}'
    )

summary = {
    "schema": "evergreen.sy-perf-summary/v1",
    "config": config,
    "corpus": {
        "read": corpus_before,
        "write": json.load(open(os.path.join(output, "write-corpus.json"), encoding="utf-8")),
    },
    "results": results,
    "write_capacity_probes": capacity,
    "overall": "FAIL" if failed else "PASS",
    "failed_metrics": failed,
}
with open(os.path.join(output, "summary.json"), "w", encoding="utf-8", newline="\n") as stream:
    json.dump(summary, stream, ensure_ascii=False, indent=2, sort_keys=True)
    stream.write("\n")

print()
print("Summary:", os.path.join(output, "summary.json"))
print("Overall:", summary["overall"])
if failed:
    raise SystemExit("threshold failures: " + ", ".join(failed))
PY

printf '[PASS] .sy performance thresholds and capacity probes passed\n'
