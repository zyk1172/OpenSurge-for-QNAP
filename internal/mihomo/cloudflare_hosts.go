package mihomo

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	cloudflareHostsBegin = "# >>> OPENSURGE CLOUDFLARE OPTIMIZER >>>"
	cloudflareHostsEnd   = "# <<< OPENSURGE CLOUDFLARE OPTIMIZER <<<"
)

// SplitCloudflareManagedHosts removes the optimizer-owned block from the native
// Mihomo hosts editor payload. The remaining text is user-owned and is returned
// byte-for-byte apart from the structural newline surrounding the managed block.
func SplitCloudflareManagedHosts(native string) (string, map[string]string, error) {
	native = strings.TrimPrefix(native, "\ufeff")
	begin := strings.Index(native, cloudflareHostsBegin)
	end := strings.Index(native, cloudflareHostsEnd)
	if begin < 0 && end < 0 {
		return native, map[string]string{}, nil
	}
	if begin < 0 || end < 0 || end < begin {
		return "", nil, fmt.Errorf("Cloudflare optimizer hosts block markers are incomplete")
	}
	if strings.Contains(native[begin+len(cloudflareHostsBegin):], cloudflareHostsBegin) || strings.Contains(native[end+len(cloudflareHostsEnd):], cloudflareHostsEnd) {
		return "", nil, fmt.Errorf("Cloudflare optimizer hosts block may appear only once")
	}
	managedText := native[begin+len(cloudflareHostsBegin) : end]
	managedText = strings.TrimSpace(managedText)
	managed := map[string]string{}
	if managedText != "" {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(managedText), &node); err != nil {
			return "", nil, fmt.Errorf("parse Cloudflare optimizer hosts: %w", err)
		}
		if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
			return "", nil, fmt.Errorf("Cloudflare optimizer hosts must be a mapping")
		}
		root := node.Content[0]
		for index := 0; index+1 < len(root.Content); index += 2 {
			domain := strings.ToLower(strings.TrimSpace(root.Content[index].Value))
			ipText := strings.TrimSpace(root.Content[index+1].Value)
			ip := net.ParseIP(ipText)
			if domain == "" || ip == nil || ip.To4() == nil {
				return "", nil, fmt.Errorf("invalid optimizer hosts entry %q: %q", domain, ipText)
			}
			managed[domain] = ip.To4().String()
		}
	}
	before := native[:begin]
	after := native[end+len(cloudflareHostsEnd):]
	if strings.HasSuffix(before, "\n\n") {
		before = strings.TrimSuffix(before, "\n")
	}
	if strings.HasPrefix(after, "\n") {
		after = strings.TrimPrefix(after, "\n")
	}
	return before + after, managed, nil
}

// JoinCloudflareManagedHosts replaces only the optimizer-owned native hosts
// block. User native hosts and conventional hosts-file text remain untouched.
func JoinCloudflareManagedHosts(native string, managed map[string]string) (string, error) {
	userNative, _, err := SplitCloudflareManagedHosts(native)
	if err != nil {
		return "", err
	}
	if len(managed) == 0 {
		return userNative, nil
	}
	if err := ValidateNativeProfileHostsYAML(userNative); err != nil {
		return "", err
	}
	conflicts, err := NativeProfileHostConflicts(userNative, managed)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		return "", fmt.Errorf("optimizer targets conflict with user native hosts: %s", strings.Join(conflicts, ", "))
	}
	keys := make([]string, 0, len(managed))
	for domain, ipText := range managed {
		ip := net.ParseIP(strings.TrimSpace(ipText))
		if strings.TrimSpace(domain) == "" || ip == nil || ip.To4() == nil {
			return "", fmt.Errorf("invalid optimizer hosts entry %q: %q", domain, ipText)
		}
		keys = append(keys, strings.ToLower(strings.TrimSpace(domain)))
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString(strings.TrimRight(userNative, "\r\n"))
	if out.Len() > 0 {
		out.WriteString("\n\n")
	}
	out.WriteString(cloudflareHostsBegin)
	out.WriteByte('\n')
	for _, domain := range keys {
		out.WriteString(yamlQuote(domain))
		out.WriteString(": ")
		out.WriteString(yamlQuote(net.ParseIP(strings.TrimSpace(managed[domain])).To4().String()))
		out.WriteByte('\n')
	}
	out.WriteString(cloudflareHostsEnd)
	return out.String(), nil
}

func NativeProfileHostConflicts(native string, managed map[string]string) ([]string, error) {
	root, err := parseNativeProfileHostsYAML(native)
	if err != nil {
		return nil, err
	}
	conflicts := []string{}
	for index := 0; index+1 < len(root.Content); index += 2 {
		key := strings.ToLower(strings.TrimSpace(root.Content[index].Value))
		if _, exists := managed[key]; exists {
			conflicts = append(conflicts, key)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}
