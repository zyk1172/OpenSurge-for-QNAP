package dnspolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
)

const maxResolverLength = 2048

func Normalize(document Document) (Document, error) {
	document.Mode = Mode(strings.TrimSpace(string(document.Mode)))
	managed := &document.Managed
	managed.DefaultNameservers = normalizeStrings(managed.DefaultNameservers)
	managed.Nameservers = normalizeStrings(managed.Nameservers)
	managed.DirectNameservers = normalizeStrings(managed.DirectNameservers)
	managed.ProxyServerNameservers = normalizeStrings(managed.ProxyServerNameservers)
	managed.Fallback = normalizeStrings(managed.Fallback)
	managed.FakeIPFilter = normalizeStrings(managed.FakeIPFilter)
	managed.FallbackFilter.GeoIPCode = strings.TrimSpace(managed.FallbackFilter.GeoIPCode)
	managed.FallbackFilter.GeoSite = normalizeStrings(managed.FallbackFilter.GeoSite)
	managed.FallbackFilter.IPCIDR = normalizeStrings(managed.FallbackFilter.IPCIDR)
	managed.FallbackFilter.Domain = normalizeStrings(managed.FallbackFilter.Domain)
	managed.FakeIPFilterMode = FakeIPFilterMode(strings.TrimSpace(string(managed.FakeIPFilterMode)))
	managed.CacheAlgorithm = CacheAlgorithm(strings.TrimSpace(string(managed.CacheAlgorithm)))
	if managed.FakeIPFilterMode == "" {
		managed.FakeIPFilterMode = FakeIPFilterBlacklist
	}
	if managed.CacheAlgorithm == "" {
		managed.CacheAlgorithm = CacheAlgorithmARC
	}
	if managed.NameserverPolicy == nil {
		managed.NameserverPolicy = []NameserverPolicyRule{}
	}
	for index := range managed.NameserverPolicy {
		managed.NameserverPolicy[index].Match = strings.TrimSpace(managed.NameserverPolicy[index].Match)
		managed.NameserverPolicy[index].Nameservers = normalizeStrings(managed.NameserverPolicy[index].Nameservers)
	}
	if err := Validate(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func Validate(document Document) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("dns policy schema_version must be %d", SchemaVersion)
	}
	switch document.Mode {
	case ModeInheritProfile, ModeManaged:
	default:
		return fmt.Errorf("dns policy mode must be %q or %q", ModeInheritProfile, ModeManaged)
	}
	managed := document.Managed
	if document.Mode == ModeManaged && len(managed.Nameservers) == 0 {
		return fmt.Errorf("managed DNS mode requires at least one nameserver")
	}
	resolverLists := []struct {
		name   string
		values []string
	}{
		{name: "default_nameservers", values: managed.DefaultNameservers},
		{name: "nameservers", values: managed.Nameservers},
		{name: "direct_nameservers", values: managed.DirectNameservers},
		{name: "proxy_server_nameservers", values: managed.ProxyServerNameservers},
		{name: "fallback", values: managed.Fallback},
	}
	for _, list := range resolverLists {
		if err := validateResolverList(list.name, list.values); err != nil {
			return err
		}
	}
	if managed.DirectNameserverFollowPolicy && len(managed.DirectNameservers) == 0 {
		return fmt.Errorf("direct_nameserver_follow_policy requires at least one direct_nameserver")
	}
	if err := validateUniqueStrings("fake_ip_filter", managed.FakeIPFilter); err != nil {
		return err
	}
	for index, value := range managed.FakeIPFilter {
		if err := validateSingleLineValue(fmt.Sprintf("fake_ip_filter[%d]", index), value); err != nil {
			return err
		}
	}
	switch managed.FakeIPFilterMode {
	case FakeIPFilterBlacklist, FakeIPFilterWhitelist:
	default:
		return fmt.Errorf("fake_ip_filter_mode must be %q or %q", FakeIPFilterBlacklist, FakeIPFilterWhitelist)
	}
	switch managed.CacheAlgorithm {
	case CacheAlgorithmLRU, CacheAlgorithmARC:
	default:
		return fmt.Errorf("cache_algorithm must be %q or %q", CacheAlgorithmLRU, CacheAlgorithmARC)
	}
	if err := validateNameserverPolicy(managed.NameserverPolicy); err != nil {
		return err
	}
	if err := validateFallbackFilter(managed.FallbackFilter); err != nil {
		return err
	}
	return nil
}

func Revision(document Document) (string, error) {
	normalized, err := Normalize(document)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal normalized DNS policy: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	normalized := make([]string, len(values))
	for index, value := range values {
		normalized[index] = strings.TrimSpace(value)
	}
	return normalized
}

func validateResolverList(name string, values []string) error {
	if err := validateUniqueStrings(name, values); err != nil {
		return err
	}
	for index, value := range values {
		if err := validateResolver(fmt.Sprintf("%s[%d]", name, index), value); err != nil {
			return err
		}
	}
	return nil
}

func validateResolver(name, value string) error {
	if err := validateSingleLineValue(name, value); err != nil {
		return err
	}
	if len(value) > maxResolverLength {
		return fmt.Errorf("%s exceeds %d bytes", name, maxResolverLength)
	}
	if !strings.Contains(value, "://") {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a valid resolver URL: %w", name, err)
	}
	if parsed.Scheme == "" {
		return fmt.Errorf("%s resolver URL requires a scheme", name)
	}
	// Some mihomo resolver forms such as rcode://success and dhcp://system use
	// a logical target rather than a conventional IP/hostname. Requiring a
	// non-empty target catches broken URLs without narrowing the future compiler
	// to today's transport set.
	if parsed.Host == "" && parsed.Opaque == "" {
		target := strings.TrimPrefix(value, parsed.Scheme+"://")
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("%s resolver URL requires a target", name)
		}
	}
	return nil
}

func validateNameserverPolicy(rules []NameserverPolicyRule) error {
	seen := map[string]bool{}
	for index, rule := range rules {
		name := fmt.Sprintf("nameserver_policy[%d]", index)
		if err := validateSingleLineValue(name+".match", rule.Match); err != nil {
			return err
		}
		if seen[rule.Match] {
			return fmt.Errorf("nameserver_policy contains duplicate matcher %q", rule.Match)
		}
		seen[rule.Match] = true
		if len(rule.Nameservers) == 0 {
			return fmt.Errorf("%s requires at least one nameserver", name)
		}
		if err := validateResolverList(name+".nameservers", rule.Nameservers); err != nil {
			return err
		}
	}
	return nil
}

func validateFallbackFilter(filter FallbackFilter) error {
	if err := validateUniqueStrings("fallback_filter.geosite", filter.GeoSite); err != nil {
		return err
	}
	if err := validateUniqueStrings("fallback_filter.ipcidr", filter.IPCIDR); err != nil {
		return err
	}
	if err := validateUniqueStrings("fallback_filter.domain", filter.Domain); err != nil {
		return err
	}
	for index, value := range filter.GeoSite {
		if err := validateSingleLineValue(fmt.Sprintf("fallback_filter.geosite[%d]", index), value); err != nil {
			return err
		}
	}
	for index, value := range filter.Domain {
		if err := validateSingleLineValue(fmt.Sprintf("fallback_filter.domain[%d]", index), value); err != nil {
			return err
		}
	}
	for index, value := range filter.IPCIDR {
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("fallback_filter.ipcidr[%d] must be a valid CIDR: %w", index, err)
		}
	}
	if err := validateSingleLineOptional("fallback_filter.geoip_code", filter.GeoIPCode); err != nil {
		return err
	}
	return nil
}

func validateUniqueStrings(name string, values []string) error {
	seen := map[string]bool{}
	for index, value := range values {
		if value == "" {
			return fmt.Errorf("%s[%d] must not be empty", name, index)
		}
		if seen[value] {
			return fmt.Errorf("%s contains duplicate value %q", name, value)
		}
		seen[value] = true
	}
	return nil
}

func validateSingleLineValue(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	return validateSingleLineOptional(name, value)
}

func validateSingleLineOptional(name, value string) error {
	for _, r := range value {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return fmt.Errorf("%s must be a single-line value", name)
		}
	}
	return nil
}
