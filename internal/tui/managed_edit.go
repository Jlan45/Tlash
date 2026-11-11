package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Jlan45/Tlash/internal/appsettings"
)

// managedRow is one editable managed setting.
type managedRow struct {
	label   string
	hint    string
	options []string // enum values cycled with enter; nil means free-form or bool
	isBool  bool
	isList  bool // comma-separated in the edit form
	get     func(m appsettings.Managed) (value string, set bool)
	set     func(m *appsettings.Managed, v string) error
	clear   func(m *appsettings.Managed) // nil when the row can't fall back to the profile's value
}

func boolRow(label string, field func(*appsettings.Managed) **bool) managedRow {
	return managedRow{
		label:  label,
		isBool: true,
		get: func(m appsettings.Managed) (string, bool) {
			if v := *field(&m); v != nil {
				return boolLabel(*v), true
			}
			return "", false
		},
		set: func(m *appsettings.Managed, v string) error {
			b := v == "是"
			*field(m) = &b
			return nil
		},
		clear: func(m *appsettings.Managed) { *field(m) = nil },
	}
}

func stringRow(label, hint string, options []string, field func(*appsettings.Managed) **string) managedRow {
	return managedRow{
		label:   label,
		hint:    hint,
		options: options,
		get: func(m appsettings.Managed) (string, bool) {
			if v := *field(&m); v != nil {
				return *v, true
			}
			return "", false
		},
		set: func(m *appsettings.Managed, v string) error {
			if options != nil && !slices.Contains(options, v) {
				return fmt.Errorf("可选值: %s", strings.Join(options, " / "))
			}
			*field(m) = &v
			return nil
		},
		clear: func(m *appsettings.Managed) { *field(m) = nil },
	}
}

// plainBoolRow edits a non-optional bool inside a managed block (e.g. DNS).
func plainBoolRow(label, hint string, field func(*appsettings.Managed) *bool) managedRow {
	return managedRow{
		label:  label,
		hint:   hint,
		isBool: true,
		get:    func(m appsettings.Managed) (string, bool) { return boolLabel(*field(&m)), true },
		set: func(m *appsettings.Managed, v string) error {
			*field(m) = v == "是"
			return nil
		},
	}
}

func plainStringRow(label, hint string, options []string, field func(*appsettings.Managed) *string) managedRow {
	return managedRow{
		label:   label,
		hint:    hint,
		options: options,
		get: func(m appsettings.Managed) (string, bool) {
			v := *field(&m)
			return v, v != ""
		},
		set: func(m *appsettings.Managed, v string) error {
			if options != nil && !slices.Contains(options, v) {
				return fmt.Errorf("可选值: %s", strings.Join(options, " / "))
			}
			*field(m) = v
			return nil
		},
	}
}

func listRow(label, hint string, field func(*appsettings.Managed) *[]string) managedRow {
	return managedRow{
		label:  label,
		hint:   hint,
		isList: true,
		get: func(m appsettings.Managed) (string, bool) {
			v := *field(&m)
			return strings.Join(v, ", "), len(v) > 0
		},
		set: func(m *appsettings.Managed, v string) error {
			*field(m) = splitList(v)
			return nil
		},
	}
}

// splitList accepts commas (ASCII or full-width) or whitespace as separators.
func splitList(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\t' })
	if len(f) == 0 {
		return nil
	}
	return f
}

// editManaged toggles bools and cycles enums in place; other rows open a one-field form.
func editManaged(sess *session, row managedRow, openForm func(*inputForm)) tea.Cmd {
	cur, set := row.get(sess.ws.Settings().Managed)
	switch {
	case row.isBool:
		next := "是"
		if set && cur == "是" {
			next = "否"
		}
		return applyManaged(sess, row.label+" → "+next, func(m *appsettings.Managed) { _ = row.set(m, next) })
	case row.options != nil:
		idx := slices.Index(row.options, cur)
		next := row.options[(idx+1)%len(row.options)]
		return applyManaged(sess, row.label+" → "+next, func(m *appsettings.Managed) { _ = row.set(m, next) })
	}
	if cur == "关闭" {
		cur = ""
	}
	hint := row.hint
	if row.isList {
		hint = strings.TrimSpace(hint + "  多项用逗号分隔")
	}
	openForm(newInputForm(row.label, func(v []string) tea.Cmd {
		var probe appsettings.Managed
		if err := row.set(&probe, v[0]); err != nil {
			return notifyErr(row.label, err)
		}
		return applyManaged(sess, row.label+" 已更新", func(m *appsettings.Managed) { _ = row.set(m, v[0]) })
	}).addText(row.label, cur, "", hint))
	return nil
}

// applyManaged saves a managed-setting change and hot-reloads it into the running core.
func applyManaged(sess *session, okText string, fn func(*appsettings.Managed)) tea.Cmd {
	ws := sess.ws
	return sess.job("core-config", "应用设置", func(ctx context.Context) (string, error) {
		return okText, ws.UpdateManaged(ctx, fn)
	}, sess.refreshAll())
}

// renderManagedRows draws a settings list with the cursor row highlighted and its hint shown.
func renderManagedRows(rows []managedRow, m appsettings.Managed, cursor, labelW int) string {
	var b strings.Builder
	for i, r := range rows {
		val, set := r.get(m)
		if !set {
			val = styleDim.Render("使用订阅值")
			if r.clear == nil {
				val = styleDim.Render("未设置")
			}
		}
		line := "  " + padRight(r.label, labelW) + val
		if i == cursor {
			line = styleSelected.Render(padRight("▸ "+r.label, labelW+2)) + val
			if r.hint != "" {
				line += styleDim.Render("  " + r.hint)
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
