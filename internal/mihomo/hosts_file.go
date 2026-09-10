package mihomo

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseHostsFileContent parses the conventional hosts-file format:
//
//   IP hostname [alias ...] # optional comment
//
// Blank lines and comments are ignored. Repeated hostnames with different IPs
// are emitted as a mihomo hosts array; exact duplicate mappings are collapsed.
func parseHostsFileContent(content string) (*yaml.Node, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	entries := map[string][]string{}
	seen := map[string]map[string]bool{}

	for index, raw := range strings.Split(content, "\n") {
		lineNumber := index + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
			if line == "" {
				continue
			}
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("hosts line %d must contain an IP address and at least one hostname", lineNumber)
		}
		parsedIP := net.ParseIP(fields[0])
		if parsedIP == nil {
			return nil, fmt.Errorf("hosts line %d has invalid IP address %q", lineNumber, fields[0])
		}
		ip := parsedIP.String()
		for _, hostname := range fields[1:] {
			hostname = strings.TrimSpace(hostname)
			if hostname == "" || strings.ContainsAny(hostname, " \t\r\n") {
				return nil, fmt.Errorf("hosts line %d has invalid hostname %q", lineNumber, hostname)
			}
			if seen[hostname] == nil {
				seen[hostname] = map[string]bool{}
			}
			if seen[hostname][ip] {
				continue
			}
			seen[hostname][ip] = true
			entries[hostname] = append(entries[hostname], ip)
		}
	}

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := make([]string, 0, len(entries))
	for hostname := range entries {
		keys = append(keys, hostname)
	}
	sort.Strings(keys)
	for _, hostname := range keys {
		ips := entries[hostname]
		root.Content = append(root.Content, quotedStringNode(hostname))
		if len(ips) == 1 {
			root.Content = append(root.Content, quotedStringNode(ips[0]))
			continue
		}
		values := make([]*yaml.Node, 0, len(ips))
		for _, ip := range ips {
			values = append(values, quotedStringNode(ip))
		}
		root.Content = append(root.Content, sequenceNode(values...))
	}
	return root, nil
}

func profileHostsFromYAML(data []byte) (*yaml.Node, error) {
	root, err := decodeSingleYAMLMapping(data)
	if err != nil {
		return nil, err
	}
	index := mappingValueIndex(root, "hosts")
	if index < 0 {
		return nil, nil
	}
	hosts := resolveAlias(root.Content[index])
	if hosts == nil || hosts.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("hosts must be a mapping")
	}
	if err := validateNodeAliases(hosts); err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}
	return hosts, nil
}

func mergeProfileHosts(base, overlay *yaml.Node) *yaml.Node {
	if base == nil {
		base = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if overlay == nil || len(overlay.Content) == 0 {
		return base
	}
	deepMergeYAMLMapping(base, overlay)
	return base
}

func injectProfileHosts(data []byte, hosts *yaml.Node) ([]byte, error) {
	if hosts == nil || len(hosts.Content) == 0 {
		return data, nil
	}
	root, err := decodeSingleYAMLMapping(data)
	if err != nil {
		return nil, err
	}
	if index := mappingValueIndex(root, "hosts"); index >= 0 {
		root.Content[index] = hosts
	} else {
		root.Content = append(root.Content, stringNode("hosts"), hosts)
	}
	rendered, err := encodeYAMLNode(root)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

func loadProfileHostsYAML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read imported mihomo profile hosts: %w", err)
	}
	hosts, err := profileHostsFromYAML(data)
	if err != nil {
		return "", fmt.Errorf("parse imported mihomo profile hosts: %w", err)
	}
	if hosts == nil || len(hosts.Content) == 0 {
		return "", nil
	}
	return encodeYAMLNode(hosts)
}
