package cli

// evergreen.yml 的原子落盘（tmp + fsync + rename + fsync 目录）。
//
// 为什么不走 store：store 是**知识产物**的唯一状态写口，导出的写形态恰三个
// （新建 / 分区追加 / 守卫写入），刻意不提供「整文件替换」能力（安全底线 B1）。
// evergreen.yml 是**工程配置而不是知识产物**，且 `eg config set` 是用户显式命令，
// 所以它的落盘留在 cli 层，并且同样满足：字节区间编辑产出内容、原子替换、不用序列化器。
//
// **当前无调用方（I-…-021 修复之后）**：`eg config set` 已被纳入 A 类写路径，
// evergreen.yml 的落盘改由 `txn.Commit` 的多文件原子提交承接（先发 intent、再 rename、
// commit marker 在盘才算生效），因此本函数不再被业务代码调用。文件**刻意保留**：
//   - 它是 M1 起冻结的「CLI 层唯一原子替换口径」，也是 U-01 反证用例
//     （`tests/_staged/internal/cli/userrequest_test.go`）唯一允许出现 `os.Remove` 的落点；
//   - 历史阶段门禁 `tests/archive/history/stage-acceptance/m3_acceptance.sh` 对
//     `os.Remove` 落点集合做**全等**断言，其中逐字含 `internal/cli/ymlwrite.go`。
//
// 删除它属于「为了清理死代码而动历史冻结门禁」，不在 C1 修复批次的授权范围内。

import (
	"os"
	"path/filepath"
)

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".eg-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(e error) error {
		_ = os.Remove(name)
		return e
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fail(err)
	}
	if err := os.Rename(name, path); err != nil {
		return fail(err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
