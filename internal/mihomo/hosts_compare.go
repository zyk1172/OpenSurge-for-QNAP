package mihomo

import (
	"fmt"
	"net"
	"reflect"
)

// HostsConfigurationEqual compares only the hosts mapping and dns.use-hosts.
// Formatting, mapping order, and unrelated profile settings are not a reason
// for the automatic host-hosts importer to reload the entire gateway.
func HostsConfigurationEqual(left, right []byte) (bool, error) {
	a, err := profileHostsConfiguration(left)
	if err != nil {
		return false, fmt.Errorf("read current profile hosts: %w", err)
	}
	b, err := profileHostsConfiguration(right)
	if err != nil {
		return false, fmt.Errorf("read candidate profile hosts: %w", err)
	}
	return reflect.DeepEqual(a, b), nil
}

type hostsConfiguration struct {
	Hosts map[string]any `yaml:"hosts"`
	DNS   struct {
		UseHosts *bool `yaml:"use-hosts"`
	} `yaml:"dns"`
}

func profileHostsConfiguration(data []byte) (hostsConfiguration, error) {
	value := hostsConfiguration{}
	root, err := decodeSingleYAMLMapping(data)
	if err != nil {
		return value, err
	}
	if err := validateNodeAliases(root); err != nil {
		return value, err
	}
	// Decode only the selected fields, including ordinary YAML aliases/merges.
	// Nil and empty hosts both represent an empty mapping. An absent use-hosts
	// remains distinct from explicit true/false; do not guess engine defaults.
	if err := root.Decode(&value); err != nil {
		return value, err
	}
	if value.Hosts == nil {
		value.Hosts = map[string]any{}
	}
	for name, entry := range value.Hosts {
		value.Hosts[name] = normalizedHostAddresses(entry)
	}
	return value, nil
}

func normalizedHostAddresses(value any) any {
	if text, ok := value.(string); ok {
		if ip := net.ParseIP(text); ip != nil {
			return ip.String()
		}
		return value
	}
	if sequence, ok := value.([]any); ok {
		// Preserve per-domain address order, which may express operator intent.
		// Only normalize address spelling; do not silently discard list edits.
		normalized := make([]any, len(sequence))
		for i, entry := range sequence {
			normalized[i] = normalizedHostAddresses(entry)
		}
		return normalized
	}
	return value
}
