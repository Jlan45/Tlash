package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/Jlan45/Tlash/internal/mihomoapi"
)

type proxySort int

const (
	sortConfigOrder proxySort = iota
	sortByDelay
	sortByName
)

func (s proxySort) label() string {
	return [...]string{"配置顺序", "延迟", "名称"}[s]
}

// orderedGroups lists proxy groups in config order. mihomo returns a map, but the
// GLOBAL group's member list preserves the order groups were declared in.
func orderedGroups(proxies map[string]mihomoapi.Proxy, includeGlobal bool) []mihomoapi.Proxy {
	var groups []mihomoapi.Proxy
	seen := map[string]bool{}
	add := func(name string) {
		p, ok := proxies[name]
		if !ok || !p.IsGroup() || p.Hidden || seen[name] {
			return
		}
		seen[name] = true
		groups = append(groups, p)
	}
	if global, ok := proxies["GLOBAL"]; ok {
		for _, name := range global.All {
			add(name)
		}
	}
	// Groups GLOBAL doesn't reference (shouldn't happen, but don't lose them).
	var rest []string
	for name, p := range proxies {
		if p.IsGroup() && !seen[name] && name != "GLOBAL" {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	for _, name := range rest {
		add(name)
	}
	if includeGlobal {
		add("GLOBAL")
	}
	return groups
}

// sortedMembers applies the sort and filter to a group's members.
func sortedMembers(group mihomoapi.Proxy, proxies map[string]mihomoapi.Proxy, sort proxySort, filter string) []string {
	members := make([]string, 0, len(group.All))
	filter = strings.ToLower(filter)
	for _, name := range group.All {
		if filter == "" || strings.Contains(strings.ToLower(name), filter) {
			members = append(members, name)
		}
	}
	switch sort {
	case sortByName:
		slices.SortStableFunc(members, strings.Compare)
	case sortByDelay:
		// Successful tests first by latency, then failures, then never tested.
		rank := func(name string) (int, int) {
			d, tested := proxies[name].LastDelay()
			switch {
			case !tested:
				return 2, 0
			case d <= 0:
				return 1, 0
			default:
				return 0, d
			}
		}
		slices.SortStableFunc(members, func(a, b string) int {
			ra, da := rank(a)
			rb, db := rank(b)
			return cmp.Or(cmp.Compare(ra, rb), cmp.Compare(da, db))
		})
	}
	return members
}

// canSelect reports whether a group accepts a manual choice (URLTest/Fallback pin it).
func canSelect(groupType string) bool {
	switch groupType {
	case "Selector", "URLTest", "Fallback":
		return true
	}
	return false
}
