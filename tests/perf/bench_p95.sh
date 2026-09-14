#!/usr/bin/env bash
# `eg bench` 性能门槛 端到端脚本（M5 · T-evergreen.s1_main_flow-158614-068 · 阶段 B）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §7.2（`--json` 的 data 恰五键）
#   §7.3（采样口径冻结：10,000 卡 / 30,000 关系语料、healthy 索引、预热 3 + 计入 50 轮、
#         P95 取第 48 小值不插值、构建两指标各 3 次取中位数、固定 20 词 / 20 id、
#         进程墙钟、门槛 = ceil(实测 × 1.5 / 10) × 10、门槛与环境绑定）
#   §8.1（bench 行：写 `.index/` = 否、写权威 Markdown = 否、退出码 {0,1,4}）
# 以及 milestone `M-005` 判据 14（五键齐全且各 ≤ 合同门槛；实测与门槛双列输出）。
#
# 本脚本守六件事：
#   ① **语料可复算**：同参数两次生成，manifest digest 相同且两棵目录树逐字节相同
#      （`diff -r`）—— 语料一变，门槛就失去意义，所以这条排在性能之前；
#   ② **语料选择度符合口径**：20 个固定关键词各恰 `cards/20 = 500` 条命中
#      （既不是 0 命中的假性变快，也不是全库命中的退化）；
#   ③ **五键齐全 + 各 ≤ 门槛**：门槛值取自合同 §7 的回填列（写死在下方 THRESH_*，
#      与合同逐字对应），并把「实测 / 门槛」**双列**打出来，供 T-…-069 验收报告直接引用；
#   ④ **只读边界**：bench 跑完后权威 Markdown 与 `.index/` 的字节清单逐字不变、commit +0；
#   ⑤ **前置与退出码**：索引不健康时退 1 且零副作用（不在降级路径上采样）；
#      多余位置参数退 1；`eg bench` 不接受任何命令私有 flag；
#   ⑥ **真实仓库零污染**：一切读写都在 mktemp 沙箱内。
#
# 运行时长：约 9–11 分钟（3 × 53 次真实 fork 的读采样 + 3 次全量构建 + 3 次增量）。
#   这是合同冻结口径的必然代价：把轮数调小就等于换了一把尺子，实测值与门槛不再同源。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；只用 bash / coreutils / awk / git / go，
#   无 jq / sqlite3 依赖；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/perf/bench_p95.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-bench.XXXXXX")"
VAULT="${WORK}/vault"
VAULT2="${WORK}/vault2"
TINY="${WORK}/tiny"
EG="${WORK}/eg"

# —— 门槛 / 语料规模：唯一真源是 tests/perf/thresholds.yaml（T-…-005 外置）——
# 脚本**不带内置默认值**：读不到键即失败。否则"读不到就用内置值"等于门槛可被静默旁路。
THRESH_YAML="${EG_TESTS_ROOT}/perf/thresholds.yaml"
[ -f "${THRESH_YAML}" ] || { printf '[FAIL] 门槛真源缺失：%s\n' "${THRESH_YAML}" >&2; exit 1; }
# 极简取值器：只认 "  <key>: <int>" 这一种形态，取指定顶层块内的键（不引入 YAML 依赖）。
yget() { # yget <块> <键>
  python3 - "${THRESH_YAML}" "$1" "$2" <<'PYEOF'
import sys, re
path, block, key = sys.argv[1], sys.argv[2], sys.argv[3]
cur, val = None, None
for line in open(path, encoding="utf-8"):
    if re.match(r"^[A-Za-z_]", line):
        cur = line.split(":", 1)[0].strip()
        continue
    m = re.match(r"^\s+([A-Za-z0-9_]+):\s*(\S+)", line)
    if m and cur == block and m.group(1) == key:
        val = m.group(2)
        break
if val is None:
    sys.exit(1)
print(val)
PYEOF
}
yreq() { # yreq <块> <键>：缺键即 fail-closed
  local v; v="$(yget "$1" "$2")" || { printf '[FAIL] thresholds.yaml 缺 %s.%s\n' "$1" "$2" >&2; exit 1; }
  [ -n "${v}" ] || { printf '[FAIL] thresholds.yaml 的 %s.%s 为空\n' "$1" "$2" >&2; exit 1; }
  printf '%s' "${v}"
}

CARDS="$(yreq corpus cards)"
RELS="$(yreq corpus relations)"
DOMAIN="$(yreq corpus domain)"

THRESH_SEARCH="$(yreq thresholds search_p95_ms)"
THRESH_CARD_SHOW="$(yreq thresholds card_show_p95_ms)"
THRESH_REL="$(yreq thresholds rel_p95_ms)"
THRESH_INDEX_BUILD="$(yreq thresholds index_build_ms)"
THRESH_INDEX_INCR="$(yreq thresholds index_incremental_ms)"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "$1" "${@:2}" </dev/null; }
eg_code() { local v="$1"; shift; local c=0; eg "${v}" "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "$1" -c user.email=eg@example.com -c user.name=eg "${@:2}"; }
commits() { gitv "$1" log --oneline | wc -l | tr -d ' '; }
authority_sha() { ( cd "$1" && { find domains sources proposals reviews -type f 2>/dev/null || true; } | sort | xargs -r sha256sum ); }
index_sha() { ( cd "$1" && { find .index -type f 2>/dev/null || true; } | sort | xargs -r sha256sum ); }
# metric <文件> <键>：从 --json 的 data 里取一个整数指标（键序固定，切片确定）。
metric() { sed "s/.*\"$2\"://; s/[,}].*//" "$1" | tr -d ' '; }
hits() { { grep -o '"id":"' "$1" || true; } | wc -l | tr -d ' '; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 + 语料可复算
step "CGO_ENABLED=0 构建；生成 ${CARDS} 卡 / ${RELS} 关系语料两遍，反证逐字节可复算"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
for V in "${VAULT}" "${VAULT2}"; do
  mkdir -p "${V}"
  [ "$(eg_code "${V}" init --domain "${DOMAIN}")" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
  [ "$(eg_code "${V}" config set default_domain "${DOMAIN}")" = "0" ] || die "config set 失败"
done
(cd "${REPO_ROOT}" && go run ./tests/perf/corpus_gen.go -cards "${CARDS}" -rels "${RELS}" -out "${VAULT}") \
  >"${WORK}/gen1.json" || die "corpus_gen 第一遍失败"
(cd "${REPO_ROOT}" && go run ./tests/perf/corpus_gen.go -cards "${CARDS}" -rels "${RELS}" -out "${VAULT2}") \
  >"${WORK}/gen2.json" || die "corpus_gen 第二遍失败"
diff "${WORK}/gen1.json" "${WORK}/gen2.json" >/dev/null || die "两遍生成的 manifest 不一致：语料不可复算"
grep -Fq "\"cards\":${CARDS}" "${WORK}/gen1.json" || { cat "${WORK}/gen1.json"; die "卡数不是 ${CARDS}"; }
grep -Fq "\"relations\":${RELS}" "${WORK}/gen1.json" || die "关系数不是 ${RELS}"
diff -r "${VAULT}/domains/${DOMAIN}" "${VAULT2}/domains/${DOMAIN}" >/dev/null ||
  die "两遍生成的语料目录不逐字节相同：固定种子失效"
FILES="$(find "${VAULT}/domains/${DOMAIN}/knowledge" -name '*.md' | wc -l | tr -d ' ')"
[ "${FILES}" = "${CARDS}" ] || die "落盘卡文件 ${FILES} 个，期望 ${CARDS}"
rm -rf "${VAULT2}" # 第二份只为反证可复算，用完即删（省磁盘）
ok "语料两遍逐字节相同（manifest + diff -r 双证），${CARDS} 张卡 / ${RELS} 条关系已落盘"

# ---------------------------------------------------------------- 2. 建索引 + 语料选择度
step "eg index build 后索引 healthy；20 个固定关键词各恰 $((CARDS / 20)) 条命中"
# 先 commit 再建索引：A-44 的新鲜度水位线含 Git HEAD，先建后提交会把索引判成 stale(W22)，
# 而 bench 明确拒绝在降级路径上采样（合同 §7.3）。顺序本身就是被测语义的一部分。
gitv "${VAULT}" add -A >/dev/null && gitv "${VAULT}" commit -q -m "seed: m5 性能语料"
[ "$(eg_code "${VAULT}" index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "index build 应退 0"; }
[ "$(eg_code "${VAULT}" index status --json)" = "0" ] || die "index status 应退 0"
grep -Fq '"health":"healthy"' "${WORK}/out.txt" || { head -c 400 "${WORK}/out.txt"; die "索引应为 healthy"; }
grep -Fq '"freshness":"fresh"' "${WORK}/out.txt" || die "索引应为 fresh（采样前置）"
EXPECT_HITS=$((CARDS / 20))
for KW in 索引 retrieval 召回率; do
  [ "$(eg_code "${VAULT}" search "${KW}" --json --limit 0)" = "0" ] || die "search ${KW} 应退 0"
  N="$(hits "${WORK}/out.txt")"
  [ "${N}" = "${EXPECT_HITS}" ] ||
    die "关键词 ${KW} 命中 ${N} 条，期望恰 ${EXPECT_HITS}（选择度不符则采样与门槛不同源）"
done
BASE_COMMITS="$(commits "${VAULT}")"
authority_sha "${VAULT}" >"${WORK}/authority.before.txt"
index_sha "${VAULT}" >"${WORK}/index.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空"
[ -s "${WORK}/index.before.txt" ] || die "索引快照为空"
ok "索引 healthy+fresh；三个抽查关键词各恰 ${EXPECT_HITS} 条命中"

# ---------------------------------------------------------------- 3. 采样：五键齐全 + 各 ≤ 门槛
step "eg bench --json：五键齐全，实测 / 门槛双列，逐个 ≤ 合同回填门槛（约 9–11 分钟）"
[ "$(eg_code "${VAULT}" bench --json)" = "0" ] ||
  { head -c 800 "${WORK}/out.txt"; cat "${WORK}/err.txt"; die "eg bench 必须退 0"; }
cp "${WORK}/out.txt" "${WORK}/bench.json"
DATA="$(sed 's/.*"data":{//; s/},"warnings".*//' "${WORK}/bench.json")"
KEYS_N="$(printf '%s' "${DATA}" | { grep -o '"[a-z0-9_]*":' || true; } | wc -l | tr -d ' ')"
[ "${KEYS_N}" = "5" ] || { printf '%s\n' "${DATA}"; die "data 键数 = ${KEYS_N}，合同 §7.2 要求恰 5 键"; }

printf '  %-22s %12s %12s %s\n' 指标 实测ms 门槛ms 结论
FAILED=0
check_metric() { # <键> <门槛>
  local key="$1" thresh="$2" got
  got="$(metric "${WORK}/bench.json" "${key}")"
  case "${got}" in
    ''|*[!0-9]*) die "指标 ${key} 取值 '${got}' 不是非负整数" ;;
  esac
  if [ "${got}" -le "${thresh}" ]; then
    printf '  %-22s %12s %12s %s\n' "${key}" "${got}" "${thresh}" 通过
  else
    printf '  %-22s %12s %12s %s\n' "${key}" "${got}" "${thresh}" '超门槛(P0 回归)'
    FAILED=$((FAILED + 1))
  fi
}
check_metric search_p95_ms        "${THRESH_SEARCH}"
check_metric card_show_p95_ms     "${THRESH_CARD_SHOW}"
check_metric rel_p95_ms           "${THRESH_REL}"
check_metric index_build_ms       "${THRESH_INDEX_BUILD}"
check_metric index_incremental_ms "${THRESH_INDEX_INCR}"
[ "${FAILED}" = "0" ] ||
  die "${FAILED} 个指标超门槛：按合同 §7 只能优化实现或重新回填实测值，**不得**放宽门槛公式或缩小语料"
ok "五键齐全，且五个指标逐个 ≤ 合同回填门槛"

# ---------------------------------------------------------------- 4. 只读边界
step "采样只读：权威 Markdown 与 .index/ 字节清单逐字不变、commit +0"
authority_sha "${VAULT}" >"${WORK}/authority.after.txt"
index_sha "${VAULT}" >"${WORK}/index.after.txt"
diff "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >/dev/null ||
  { diff "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" || true
    die "eg bench 改写了权威 Markdown（它必须只读）"; }
diff "${WORK}/index.before.txt" "${WORK}/index.after.txt" >/dev/null ||
  { diff "${WORK}/index.before.txt" "${WORK}/index.after.txt" || true
    die "eg bench 改写了 .index/（构建耗时必须在临时副本上测）"; }
[ "$(commits "${VAULT}")" = "${BASE_COMMITS}" ] || die "commit 数变了：采样恒零 commit"
[ "$(gitv "${VAULT}" status --porcelain | wc -l | tr -d ' ')" = "0" ] || die "工作区被污染"
grep -Fq '只读采样' "${WORK}/bench.json" || die "输出里应有只读采样的明文交代"
ok "权威与 .index/ 字节清单逐字不变；commit +0；工作区干净"

# ---------------------------------------------------------------- 5. 前置与退出码
step "索引不健康 → 退 1 且零副作用；多余位置参数 → 退 1；无命令私有 flag"
mkdir -p "${TINY}"
[ "$(eg_code "${TINY}" init --domain "${DOMAIN}")" = "0" ] || die "tiny vault init 失败"
[ "$(eg_code "${TINY}" config set default_domain "${DOMAIN}")" = "0" ] || die "tiny config set 失败"
mkdir -p "${TINY}/domains/${DOMAIN}/knowledge"
printf '%s\n' '---' 'id: k-20261201-tiny-a' 'status: active' "created_at: '2026-12-01'" \
  "updated_at: '2026-12-01T10:00:00+08:00'" 'title: 小语料卡' 'sources: []' '---' '' \
  '## 知识内容' '' '索引 / retrieval：小语料。' >"${TINY}/domains/${DOMAIN}/knowledge/k-20261201-tiny-a.md"
TINY_BEFORE="$(authority_sha "${TINY}")"
# 索引尚未构建（.index/eg.db 不在场）⇒ 不在降级路径上采样，退 1、零副作用。
#
# ★ 现态口径（M6 A-52，`internal/txn` 的 run.lock）：上面两条 **A 类写命令**（`init` /
#   `config set`）会取 run.lock，而锁的落点就是 `.index/run.lock`，因此此刻 `.index/`
#   目录**必然已经存在** —— 这与「bench 不得建索引」是两件事。判据要钉的是
#   「**bench 自己**不产生任何 `.index/` 字节变化」，故这里用双证代替原先的「目录不存在」：
#     ① `.index/eg.db` 前后都不在场（bench 绝不建索引 —— 原判据语义，逐字保留）；
#     ② `.index/` 的**逐文件 sha 清单**在 bench 前后逐字不变（连 run.lock 被动一下都会红）。
#   这比「目录不存在」**更严**：目录形态只能表达「有/无」，字节清单能抓住任何新增与改写。
index_sha "${TINY}" >"${WORK}/tiny_index.before.txt"
[ ! -e "${TINY}/.index/eg.db" ] || die "前置：采样之前 tiny vault 不应存在索引文件"
[ "$(eg_code "${TINY}" bench --json)" = "1" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "索引缺失时 eg bench 必须退 1"; }
[ ! -e "${TINY}/.index/eg.db" ] || die "前置不满足时不得建索引（.index/eg.db 绝不许被 bench 创建）"
index_sha "${TINY}" >"${WORK}/tiny_index.after.txt"
diff "${WORK}/tiny_index.before.txt" "${WORK}/tiny_index.after.txt" >/dev/null ||
  { diff "${WORK}/tiny_index.before.txt" "${WORK}/tiny_index.after.txt" || true
    die "前置不满足时 eg bench 不得改动 .index/ 的任何字节"; }
[ "$(authority_sha "${TINY}")" = "${TINY_BEFORE}" ] || die "前置不满足时必须零副作用"
# 建好索引后同一个 vault 就能采样（证明上一条拒绝的是「索引状态」，不是命令本身坏了）。
[ "$(eg_code "${TINY}" index build --json)" = "0" ] || die "tiny index build 应退 0"
[ "$(eg_code "${TINY}" bench --json)" = "0" ] || { cat "${WORK}/err.txt"; die "健康索引下 eg bench 应退 0"; }
# 多余位置参数 / 未声明 flag 一律退 1（bench 的参数面恰 [--json]）。
[ "$(eg_code "${TINY}" bench extra --json)" = "1" ] || die "eg bench extra 必须退 1"
[ "$(eg_code "${TINY}" bench --limit 5 --json)" = "1" ] || die "eg bench --limit 必须退 1（无此参数）"
[ "$(eg_code "${TINY}" bench --rounds 3 --json)" = "1" ] || die "eg bench --rounds 必须退 1（口径不可调）"
ok "索引不健康退 1 零副作用；健康后退 0；多余参数与不存在的口径 flag 一律退 1"

# ---------------------------------------------------------------- 6. 仓库零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] m5_bench_p95.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
