package config

import (
	"flag"
	"fmt"
)

// Config 应用配置
type Config struct {
	SubscribeURL       string
	ListenPort         string
	WebPort            string
	AutoSwitchDuration int
	DNSResolverIP      string
	BootstrapNode      string
	Version            string
	UpdateRepo         string // GitHub 仓库用于自更新，格式: "owner/repo"
}

// NewConfig 创建新的配置实例
func NewConfig(version string) *Config {
	return &Config{
		Version:    version,
		UpdateRepo: "ghostGPT/naiveswitcher", // 默认仓库
	}
}

// ParseFlags 解析命令行参数
func (c *Config) ParseFlags() bool {
	var showVersion bool

	flag.StringVar(&c.SubscribeURL, "s", "https://example.com/sublink", "Subscribe to a URL")
	flag.StringVar(&c.ListenPort, "l", "0.0.0.0:1080", "Listen port")
	flag.StringVar(&c.WebPort, "w", "0.0.0.0:1081", "Web port")
	flag.StringVar(&c.DNSResolverIP, "r", "1.0.0.1:53", "DNS resolver IP")
	flag.IntVar(&c.AutoSwitchDuration, "a", 30, "Auto switch fastest duration (minutes)")
	flag.StringVar(&c.BootstrapNode, "b", "", "Bootup node (default naive node https://a:b@domain:port)")
	flag.StringVar(&c.UpdateRepo, "u", "ghostGPT/naiveswitcher", "GitHub repository for self-update (owner/repo)")
	flag.BoolVar(&showVersion, "v", false, "Show version")
	flag.Parse()

	if showVersion {
		println(c.Version)
		return true
	}

	return false
}

func (c *Config) Validate() error {
	if c.SubscribeURL == "" {
		return fmt.Errorf("please provide a subscribe URL")
	}

	if c.AutoSwitchDuration < 30 {
		return fmt.Errorf("auto switch duration must be at least 30 minutes")
	}

	return nil
}
