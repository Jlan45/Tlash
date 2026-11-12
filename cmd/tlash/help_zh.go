package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// Command groups shown in `tlash --help`.
const (
	groupDaily = "daily"
	groupMaint = "maint"
)

// usageTemplate is cobra's default usage template with Chinese labels and grouped commands.
const usageTemplate = `用法：{{if .Runnable}}
  {{.UseLine | zhUse}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [命令]{{end}}{{if gt (len .Aliases) 0}}

别名：
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

示例：
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

命令：{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

其他：{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

选项：
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces | zhDefaults}}{{end}}{{if .HasAvailableInheritedFlags}}

全局选项：
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces | zhDefaults}}{{end}}{{if .HasAvailableSubCommands}}

运行 "{{.CommandPath}} [命令] --help" 查看每个命令的说明。{{end}}
`

var flagDefault = regexp.MustCompile(` ?\(default ([^\n]*)\)$`)

// zhDefaults rewrites pflag's "(default x)" suffixes.
func zhDefaults(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = flagDefault.ReplaceAllString(l, "（默认 $1）")
	}
	return strings.Join(lines, "\n")
}

// localizeHelp switches cobra's built-in English (usage, help flag and command, completion,
// error prefix) to Chinese for cmd and everything under it.
func localizeHelp(cmd *cobra.Command) {
	cobra.AddTemplateFunc("zhDefaults", zhDefaults)
	cobra.AddTemplateFunc("zhUse", func(s string) string { return strings.ReplaceAll(s, "[flags]", "[选项]") })
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return fmt.Errorf("%s（运行 %s --help 查看可用选项）", zhFlagError(err.Error()), c.CommandPath())
	})
	cmd.SetUsageTemplate(usageTemplate)
	cmd.SetErrPrefix("错误：")
	cmd.AddGroup(&cobra.Group{ID: groupDaily, Title: "日常："}, &cobra.Group{ID: groupMaint, Title: "维护："})
	cmd.PersistentFlags().BoolP("help", "h", false, "显示帮助")
	cmd.SetHelpCommand(&cobra.Command{
		Use:   "help [命令]",
		Short: "显示某个命令的帮助",
		RunE: func(c *cobra.Command, args []string) error {
			target, _, err := c.Root().Find(args)
			if err != nil || target == nil {
				return fmt.Errorf("未知命令：%s", strings.Join(args, " "))
			}
			return target.Help()
		},
	})
	cmd.InitDefaultCompletionCmd()
	for _, c := range cmd.Commands() {
		if c.Name() != "completion" {
			continue
		}
		c.Short = "生成 shell 自动补全脚本"
		c.Long = "生成 bash / zsh / fish / powershell 的自动补全脚本，例如：\n  tlash completion bash > /etc/bash_completion.d/tlash"
		for _, sub := range c.Commands() {
			sub.Short = "生成 " + sub.Name() + " 的自动补全脚本"
			sub.Flags().Lookup("no-descriptions").Usage = "不包含命令说明"
		}
	}
}

// zhFlagError translates pflag's error prefixes.
func zhFlagError(msg string) string {
	for en, zh := range map[string]string{
		"unknown flag: ":           "未知选项：",
		"unknown shorthand flag: ": "未知短选项：",
		"flag needs an argument: ": "选项缺少参数：",
		"invalid argument ":        "参数无效：",
	} {
		if strings.HasPrefix(msg, en) {
			return zh + strings.TrimPrefix(msg, en)
		}
	}
	return msg
}
