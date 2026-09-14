package model

// Config 是 `vault/evergreen.yml`：**工程配置而不是知识产物**，不属于任何领域，纳入 Git。
//
// 只有三个顶层键：version / domains / default_domain。
// 未配置 default_domain 时，除 `init` / `config` 外所有命令提示配置并退 1，
// **CLI 绝不自选默认领域**（EG-DOM-03）。
type Config struct {
	Version       int      `yaml:"version" json:"version"`
	Domains       []string `yaml:"domains" json:"domains"`
	DefaultDomain string   `yaml:"default_domain" json:"default_domain"`

	// Extra 承接未知配置键（原样透传容器；写路径只做字节区间编辑，不重排不丢弃）。
	Extra map[string]interface{} `yaml:",inline" json:"-"`
}

// ConfigVersion 是 S1 唯一支持的配置版本。
const ConfigVersion = 1

// ConfigKeys 是 `eg config get|set` 在 S1 允许操作的键（封闭集合）。
func ConfigKeys() []string { return []string{"default_domain", "domains"} }

// HasDomain 报告领域是否已登记。
func (c Config) HasDomain(d string) bool {
	for _, x := range c.Domains {
		if x == d {
			return true
		}
	}
	return false
}

// DefaultDomainConfigured 报告 default_domain 是否已配置。
func (c Config) DefaultDomainConfigured() bool { return c.DefaultDomain != "" }
