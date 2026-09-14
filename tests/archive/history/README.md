# 历史治理材料归档区（不是当前产品测试）

这个目录放的是 **Evergreen 历史阶段的治理材料原文**。它们被搬到这里、而不是删掉，
是为了「结论可追溯」；但请先记住一句话：

> **这里的任何东西都不是当前产品测试，它们的历史失败与历史通过都不构成当前系统的结论。**

统一 runner 因此把 `tests/archive/**` 排除在执行面之外：不进任何 profile 的执行计划、
不计入 PASS/FAIL 统计、不计入用例数基线。清单里它们的 `layer=archive`、`suite_id=-`
（`suite_id` 是追溯单位，归档材料不参与追溯）。这条约束有机器判据兜着，
不靠这份 README 的自觉：

| 判据 | 位置 | 检什么 |
| --- | --- | --- |
| H7 归档隔离 | `tests/contract/e2e-hygiene/hygiene.sh` | 阶段脚本不得出现在 `tests/e2e/`，e2e 也不得把归档材料当执行入口 |
| 归档不计入统计 | `tests/contract/archive-hygiene/archive_not_counted.sh` | `layer=archive` 行的 `suite_id` 恒为 `-`；不出现在 `coverage.tsv`；不出现在 `runtime_map.tsv`（不物化）；自带反例 |

## 目录内容

### `stage-acceptance/`

M2–M6 五支阶段聚合脚本（`m2_acceptance.sh` … `m6_acceptance.sh`）的**原文**，
逐字节保留，不做任何改写。

它们在历史上承担「一个里程碑收尾时把当阶段全部判据跑一遍」的职责，形态是
**递归委派 + 阶段清单计数 + 里程碑簿记**：脚本内部按 `m*_` 前缀调用其它脚本，
并对「本阶段应有多少支脚本」做算术等式。这三件事在当前测试体系里都已被替换：

- 执行面按**能力**组织（`tests/e2e/<capability>/<scenario>.sh`），不再有阶段前缀入口
  —— 递归委派与阶段计数因此失去指称对象（合同 D5 禁止脚本自组织执行面）；
- 「跑哪些、跑多少」由清单 `tests/manifest/inventory.tsv` 派生，由统一 runner 执行；
- 里程碑判据簿记属规划域，落在 teamwork 侧的 milestone / task 正文，不在产品仓。

其中**仍然成立的产品断言**已按 `tests/manifest/migration.tsv` 的 `merge` 行逐条并入
当前门禁（例如 `internal/reconcile` 零索引符号、`internal/cli` 非对账族零对账引用、
`os.Exit(5)` 唯一调用点 → `tests/contract/static-boundaries/*`）。
并入关系可核对：`python3 tests/tools/check_migration.py` 的 C4 会检查合并目标里
真的能检索到来源文件名，检索不到就红。

**没有迁移的部分**是阶段冻结算术等式（例如"本阶段应有 N 支脚本 / N 条断言"），
已登记为 `I-…-009`，不在当前体系重放。

### 与 teamwork 侧 21 个历史 `.py` 工具的关系

那 21 个脚本（`m*_final_gate.py`、`round*_final_gate.py`、`m*_mutation_test.py`、
`validate_m*_tasks.py` 等）**留在 teamwork 仓原地**，一个字都没动，也没有复制进本仓。
它们的分类与处置逐条登记在 `tests/manifest/legacy_gate_disposition.tsv`：

| 归属（EPIC 四类） | disposition | 条数 | 去向 |
| --- | --- | --- | --- |
| a 当前静态合同门禁 | `assertions-migrated` | 5 | 有效断言并入 `tests/contract/**` |
| b mutation 判据 | `defer-005` | 4 | 机制继承到 `tests/mutation/`（靶子换成当前判据） |
| c 规划结构自检 | `archive-in-place` | 9 | 留 teamwork 侧作规划工具，不进产品测试 |
| d 历史排期 / 冻结基线 / 阶段结论 | `archive-in-place` | 3 | 原地保留原文，仅作历史材料引用 |

归属写在 `legacy_gate_disposition.tsv` 的 `epic_class` 列（a/b/c/d，不许留空），
与 `disposition`、机器判据 `klass`、文件名形状三向交叉校验：`d` 必须是 `*_final_gate.py`
本体，`c` 必须不是；`klass=mutation-harness` 只能是 `b`。改一列换绿会当场失败。

`python3 tests/tools/legacy_gates.py --check` 会校验 21 个一个不漏、四类归属与机器判据
（`klass`）自洽、`assertions-migrated` 的 `target_suites` 必须真实存在于清单。

## 引用这里的东西时的规矩

1. 可以引用**原文**说明"历史上是怎么判的"；
2. 不可以用它的历史结论替代当前判据 —— 当前结论只能来自当前 profile 的实跑；
3. 不可以把这里的脚本重新接进执行面（H7 会红）；
4. 历史与当前的差异（冻结基线差异、合同漂移）只在**新文档**里登记，
   不回改历史阶段的验收结论。
