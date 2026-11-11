package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

var managedRows = []managedRow{
	{
		label: "混合端口",
		hint:  "HTTP + SOCKS5 代理端口",
		get: func(m appsettings.Managed) (string, bool) {
			if m.MixedPort != nil {
				return strconv.Itoa(*m.MixedPort), true
			}
			return "", false
		},
		set: func(m *appsettings.Managed, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 65535 {
				return fmt.Errorf("端口需要是 0-65535 的数字")
			}
			m.MixedPort = &n
			return nil
		},
		clear: func(m *appsettings.Managed) { m.MixedPort = nil },
	},
	boolRow("允许局域网", func(m *appsettings.Managed) **bool { return &m.AllowLan }),
	stringRow("绑定地址", "允许局域网时监听的地址，* 表示全部", nil, func(m *appsettings.Managed) **string { return &m.BindAddress }),
	boolRow("IPv6", func(m *appsettings.Managed) **bool { return &m.IPv6 }),
	stringRow("日志级别", "", []string{"debug", "info", "warning", "error", "silent"}, func(m *appsettings.Managed) **string { return &m.LogLevel }),
	boolRow("统一延迟", func(m *appsettings.Managed) **bool { return &m.UnifiedDelay }),
	boolRow("TCP 并发", func(m *appsettings.Managed) **bool { return &m.TCPConcurrent }),
	stringRow("进程匹配", "", []string{"off", "strict", "always"}, func(m *appsettings.Managed) **string { return &m.FindProcessMode }),
	{
		label: "TCP 控制接口",
		hint:  "供 Web 面板使用，如 127.0.0.1:9090；留空关闭。公网监听务必配合密钥",
		get: func(m appsettings.Managed) (string, bool) {
			if m.ExternalController == "" {
				return "关闭", true
			}
			return m.ExternalController, true
		},
		set: func(m *appsettings.Managed, v string) error {
			if v == "关闭" {
				v = ""
			}
			m.ExternalController = v
			if v != "" && m.Secret == "" {
				m.Secret = appsettings.RandomSecret()
			}
			return nil
		},
	},
	{
		label: "API 密钥",
		hint:  "TCP 控制接口的 secret；留空则随机生成",
		get: func(m appsettings.Managed) (string, bool) {
			return maskSecret(m.Secret), true
		},
		set: func(m *appsettings.Managed, v string) error {
			if v == "" || v == maskSecret(m.Secret) {
				v = appsettings.RandomSecret()
			}
			m.Secret = v
			return nil
		},
	},
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return "****" + s[len(s)-4:]
}

var dnsRows = []managedRow{
	plainBoolRow("启用", "", func(m *appsettings.Managed) *bool { return &m.DNS.Enable }),
	plainStringRow("监听地址", "仅 TUN / 透明代理需要，如 0.0.0.0:1053；留空不监听", nil, func(m *appsettings.Managed) *string { return &m.DNS.Listen }),
	plainStringRow("增强模式", "", []string{"fake-ip", "redir-host", "normal"}, func(m *appsettings.Managed) *string { return &m.DNS.EnhancedMode }),
	plainStringRow("Fake-IP 范围", "", nil, func(m *appsettings.Managed) *string { return &m.DNS.FakeIPRange }),
	listRow("Fake-IP 过滤", "这些域名返回真实 IP", func(m *appsettings.Managed) *[]string { return &m.DNS.FakeIPFilter }),
	listRow("默认 DNS", "用于解析其他 DNS 服务器的域名，必须是 IP", func(m *appsettings.Managed) *[]string { return &m.DNS.DefaultNameserver }),
	listRow("主 DNS", "支持 UDP / DoH / DoT，如 https://doh.pub/dns-query", func(m *appsettings.Managed) *[]string { return &m.DNS.Nameserver }),
	listRow("备用 DNS", "留空不使用", func(m *appsettings.Managed) *[]string { return &m.DNS.Fallback }),
	listRow("代理节点 DNS", "解析节点域名专用", func(m *appsettings.Managed) *[]string { return &m.DNS.ProxyServerNameserver }),
}

var snifferRows = []managedRow{
	plainBoolRow("启用", "", func(m *appsettings.Managed) *bool { return &m.Sniffer.Enable }),
	plainBoolRow("覆盖目标地址", "用嗅探到的域名替换连接目标", func(m *appsettings.Managed) *bool { return &m.Sniffer.OverrideDestination }),
	listRow("HTTP 端口", "如 80, 8080-8880", func(m *appsettings.Managed) *[]string { return &m.Sniffer.HTTPPorts }),
	listRow("TLS 端口", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.TLSPorts }),
	listRow("QUIC 端口", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.QUICPorts }),
	listRow("强制嗅探域名", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.ForceDomain }),
	listRow("跳过嗅探域名", "", func(m *appsettings.Managed) *[]string { return &m.Sniffer.SkipDomain }),
}
