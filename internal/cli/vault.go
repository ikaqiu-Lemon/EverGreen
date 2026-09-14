package cli

// vault 定位与 evergreen.yml 的**只读**解析，供框架层守卫使用。
//
// 写路径硬约束：YAML 库**只读**。本文件只用 yaml.Unmarshal，
// 序列化类 API（Marshal / NewEncoder 系列）一律不出现——make lint 的 guard 会 grep 反证。
// `eg config set` 的写入由 T-…-008 走 mdfile/store 的字节区间写路径完成。

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// loadConfigReadOnly 只读解析 <root>/evergreen.yml。
// 未知键落进 model.Config.Extra，不做任何回写，因此注释 / 键顺序 / 空行天然保留。
func loadConfigReadOnly(root string) (model.Config, error) {
	raw, err := os.ReadFile(filepath.Join(root, ConfigFileName))
	if err != nil {
		return model.Config{}, err
	}
	var cfg model.Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return model.Config{}, fmt.Errorf("%s 解析失败：%w", ConfigFileName, err)
	}
	return cfg, nil
}
