package cloudflareopt

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ApplyResultsToProfile injects optimizer-selected addresses into the effective
// Mihomo profile without mutating the user's imported source or persisted
// profile overlay. Recomposition always starts from those user-owned inputs, so
// removing a target automatically restores any previous host/filter value.
func ApplyResultsToProfile(profile []byte, results []TargetResult) ([]byte, error) {
	if len(results) == 0 {
		return profile, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(profile, &document); err != nil {
		return nil, fmt.Errorf("parse effective profile for Cloudflare optimizer: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("effective profile must be a YAML mapping")
	}
	root := document.Content[0]

	selected := map[string]string{}
	for _, result := range results {
		domain := normalizeDomain(result.Domain)
		ip := net.ParseIP(strings.TrimSpace(result.Selected.IP))
		if domain == "" || ip == nil || ip.To4() == nil {
			continue
		}
		selected[domain] = ip.To4().String()
	}
	if len(selected) == 0 {
		return profile, nil
	}

	hosts := ensureMapping(root, "hosts")
	for _, domain := range sortedKeys(selected) {
		setMappingString(hosts, domain, selected[domain])
	}

	dns := ensureMapping(root, "dns")
	mode := mappingString(dns, "fake-ip-filter-mode")
	if mode == "" {
		mode = "blacklist"
	}
	existing, err := mappingStringSequence(dns, "fake-ip-filter")
	if err != nil {
		return nil, err
	}
	domains := sortedKeys(selected)
	plan := PlanRealIP(mode, existing, domains)
	if len(plan.Conflicts) > 0 {
		keys := make([]string, 0, len(plan.Conflicts))
		for domain := range plan.Conflicts {
			keys = append(keys, domain)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, domain := range keys {
			parts = append(parts, domain+": "+plan.Conflicts[domain])
		}
		return nil, fmt.Errorf("Cloudflare optimizer real-IP conflict: %s", strings.Join(parts, "; "))
	}
	if len(plan.Prepend) > 0 || len(plan.Append) > 0 {
		merged := make([]string, 0, len(plan.Prepend)+len(existing)+len(plan.Append))
		merged = append(merged, plan.Prepend...)
		merged = append(merged, existing...)
		merged = append(merged, plan.Append...)
		setMappingStringSequence(dns, "fake-ip-filter", dedupeStrings(merged))
	}

	out, err := yaml.Marshal(&document)
	if err != nil {
		return nil, fmt.Errorf("render effective profile with Cloudflare optimizer: %w", err)
	}
	return out, nil
}

func ensureMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			value := parent.Content[i+1]
			if value.Kind == yaml.MappingNode {
				return value
			}
			value.Kind = yaml.MappingNode
			value.Tag = "!!map"
			value.Content = nil
			return value
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, value)
	return value
}

func mappingIndex(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

func setMappingString(mapping *yaml.Node, key, value string) {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
	if index := mappingIndex(mapping, key); index >= 0 {
		mapping.Content[index] = node
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, Style: yaml.DoubleQuotedStyle}, node)
}

func mappingString(mapping *yaml.Node, key string) string {
	if index := mappingIndex(mapping, key); index >= 0 {
		return strings.ToLower(strings.TrimSpace(mapping.Content[index].Value))
	}
	return ""
}

func mappingStringSequence(mapping *yaml.Node, key string) ([]string, error) {
	index := mappingIndex(mapping, key)
	if index < 0 {
		return []string{}, nil
	}
	value := mapping.Content[index]
	if value.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("dns.%s must be a sequence", key)
	}
	out := make([]string, 0, len(value.Content))
	for _, item := range value.Content {
		if item.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("dns.%s must contain only scalar strings", key)
		}
		out = append(out, strings.TrimSpace(item.Value))
	}
	return out, nil
}

func setMappingStringSequence(mapping *yaml.Node, key string, values []string) {
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		sequence.Content = append(sequence.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	if index := mappingIndex(mapping, key); index >= 0 {
		mapping.Content[index] = sequence
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, sequence)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
