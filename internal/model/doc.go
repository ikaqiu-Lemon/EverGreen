// [S1] internal/model：产物结构体、ID 类型、枚举、时间格式。
//
// 允许依赖：无（零依赖，不得导入本仓任何其他 internal/ 包）。
// 首次落地：S1（施工索引 §13）。
// 本包只用标准库：`UnmarshalYAML(unmarshal func(interface{}) error) error` 是
// yaml.v3 仍然支持的 v2 风格接口，因此枚举与时间类型能在 YAML 反序列化时立刻拒绝
// 非法取值，而本包无需 import 任何 YAML 库（读路径由 mdfile 负责，写路径只做字节区间拼接）。
package model
