package workspace

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// LoginEnvPath is sourced by login shells on most distros (root installs only).
const LoginEnvPath = "/etc/profile.d/tlash-proxy.sh"

// MixedPort is the port clients should use: the managed one, else what the runtime config says.
func (w *Workspace) MixedPort() (int, error) {
	if p := w.Settings().Managed.MixedPort; p != nil && *p > 0 {
		return *p, nil
	}
	b, err := os.ReadFile(w.ConfigPath)
	if err != nil {
		return 0, errors.New("还没有生成运行配置")
	}
	var cfg struct {
		MixedPort int `yaml:"mixed-port"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil || cfg.MixedPort == 0 {
		return 0, errors.New("运行配置中没有 mixed-port")
	}
	return cfg.MixedPort, nil
}

// ProxyEnv is shell code exporting (or unsetting) proxy variables for the mixed port.
func (w *Workspace) ProxyEnv(unset bool) (string, error) {
	names := []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	if unset {
		all := append(names, strings.ToUpper(strings.Join(names, " ")))
		return "unset " + strings.Join(all, " ") + "\n", nil
	}
	port, err := w.MixedPort()
	if err != nil {
		return "", err
	}
	vals := map[string]string{
		"http_proxy":  fmt.Sprintf("http://127.0.0.1:%d", port),
		"https_proxy": fmt.Sprintf("http://127.0.0.1:%d", port),
		"all_proxy":   fmt.Sprintf("socks5h://127.0.0.1:%d", port),
		"no_proxy":    "localhost,127.0.0.1,::1",
	}
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "export %s=%s %s=%s\n", n, vals[n], strings.ToUpper(n), vals[n])
	}
	return b.String(), nil
}

func LoginEnvEnabled() bool {
	_, err := os.Stat(LoginEnvPath)
	return err == nil
}

// SetLoginEnv makes login shells pick up the proxy variables via `tlash env`.
func (w *Workspace) SetLoginEnv(on bool) error {
	if !on {
		if err := os.Remove(LoginEnvPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("需要 root 才能写入 " + LoginEnvPath + "；普通用户可在 ~/.bashrc 中加入：eval \"$(tlash env)\"")
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`# Managed by Tlash: proxy variables for login shells. Set TLASH_NO_PROXY_ENV=1 to skip.
if [ -z "$TLASH_NO_PROXY_ENV" ] && [ -x %[1]q ]; then
	eval "$(%[1]q --dir %[2]q env 2>/dev/null)"
fi
`, bin, w.Dir)
	return os.WriteFile(LoginEnvPath, []byte(script), 0o644)
}
