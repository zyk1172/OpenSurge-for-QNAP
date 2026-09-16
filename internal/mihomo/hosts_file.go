package mihomo

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	profileHostsNativeBegin = "# >>> OPENSURGE NATIVE MIHOMO HOSTS >>>"
	profileHostsNativeEnd   = "# <<< OPENSURGE NATIVE MIHOMO HOSTS <<<"
)

// SplitProfileHostsInputs separates the backwards-compatible conventional
// hosts-file text from the optional native Mihomo hosts YAML block persisted in
// the same overlay field. Existing overlays without the markers are returned
// unchanged as conventional hosts-file content.
func SplitProfileHostsInputs(content string) (string, string, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	begin := strings.Index(content, profileHostsNativeBegin)
	end := strings.Index(content, profileHostsNativeEnd)
	if begin < 0 && end < 0 {
		return strings.TrimRight(content, "\r\n"), "", nil
	}
	if begin < 0 || end < 0 || end < begin {
		return "", "", fmt.Errorf("native Mihomo hosts block markers are incomplete")
	}
	if strings.Contains(content[begin+len(profileHostsNativeBegin):], profileHostsNativeBegin) || strings.Contains(content[end+len(profileHostsNativeEnd):], profileHostsNativeEnd) {
		return "", "", fmt.Errorf("native Mihomo hosts block may appear only once")
	}
	nativeStart := begin + len(profileHostsNativeBegin)
	standard := strings.TrimRight(content[:begin]+content[end+len(profileHostsNativeEnd):], "\r\n")
	native := strings.TrimSpace(content[nativeStart:end])
	return standard, native, nil
}

// JoinProfileHostsInputs persists both editor surfaces without changing the
// profile-overlay schema. The native block is OpenSurge-only and is consumed
// before the final top-level Mihomo hosts mapping is rendered.
func JoinProfileHostsInputs(standard, native string) string {
	standard = strings.TrimRight(strings.TrimPrefix(standard, "\ufeff"), "\r\n")
	native = strings.TrimSpace(strings.TrimPrefix(native, "\ufeff"))
	if native == "" {
		return standard
	}
	var out strings.Builder
	if standard != "" {
		out.WriteString(standard)
		out.WriteString("\n\n")
	}
	out.WriteString(profileHostsNativeBegin)
	out.WriteByte('\n')
	out.WriteString(native)
	out.WriteByte('\n')
	out.WriteString(profileHostsNativeEnd)
	return out.String()
}

// ValidateNativeProfileHostsYAML validates the native Mihomo hosts editor
// payload. The editor accepts either the mapping body or a full `hosts:`
// wrapper, which makes the same API convenient for both humans and agents.
func ValidateNativeProfileHostsYAML(content string) error {
	_, err := parseNativeProfileHostsYAML(content)
	return err
}

// parseHostsFileContent accepts the conventional hosts-file format plus the
// OpenSurge native Mihomo hosts block produced by JoinProfileHostsInputs.
// Native YAML is merged after conventional entries, so an explicit native key
// wins when both inputs define the same hostname or wildcard.
func parseHostsFileContent(content string) (*yaml.Node, error) {
	standard, native, err := SplitProfileHostsInputs(content)
	if err != nil {
		return nil, err
	}
	root, err := parseTraditionalHostsFileContent(standard)
	if err != nil {
		return nil, err
	}
	if native == "" {
		return root, nil
	}
	nativeHosts, err := parseNativeProfileHostsYAML(native)
	if err != nil {
		return nil, fmt.Errorf("native Mihomo hosts YAML: %w", err)
	}
	return mergeProfileHosts(root, nativeHosts), nil
}

// parseTraditionalHostsFileContent parses the conventional hosts-file format:
//
//   IP hostname [alias ...] # optional comment
//
// Blank lines and comments are ignored. Repeated hostnames with different IPs
// are emitted as a mihomo hosts array; exact duplicate mappings are collapsed.
func parseTraditionalHostsFileContent(content string) (*yaml.Node, error) {
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

func parseNativeProfileHostsYAML(content string) (*yaml.Node, error) {
	content = strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	if content == "" {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root, err := decodeSingleYAMLMapping([]byte(content))
	if err != nil {
		return nil, err
	}
	// Accept a pasted full Mihomo snippet as well as the mapping body shown by
	// the guided editor.
	if len(root.Content) == 2 && isStringScalar(root.Content[0]) && root.Content[0].Value == "hosts" {
		root = resolveAlias(root.Content[1])
		if root == nil || root.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("hosts must be a mapping")
		}
	}
	if err := validateNodeAliases(root); err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}
	for index := 0; index < len(root.Content); index += 2 {
		key := resolveAlias(root.Content[index])
		value := resolveAlias(root.Content[index+1])
		if key == nil || !isStringScalar(key) || strings.TrimSpace(key.Value) == "" {
			return nil, fmt.Errorf("hosts keys must be non-empty domain strings")
		}
		if value == nil {
			return nil, fmt.Errorf("hosts entry %q has an empty value", key.Value)
		}
		switch value.Kind {
		case yaml.ScalarNode:
			if !isStringScalar(value) || strings.TrimSpace(value.Value) == "" {
				return nil, fmt.Errorf("hosts entry %q must be a string or string array", key.Value)
			}
		case yaml.SequenceNode:
			if len(value.Content) == 0 {
				return nil, fmt.Errorf("hosts entry %q array must not be empty", key.Value)
			}
			for _, item := range value.Content {
				item = resolveAlias(item)
				if item == nil || !isStringScalar(item) || strings.TrimSpace(item.Value) == "" {
					return nil, fmt.Errorf("hosts entry %q array must contain only non-empty strings", key.Value)
				}
			}
		default:
			return nil, fmt.Errorf("hosts entry %q must be a string or string array", key.Value)
		}
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
