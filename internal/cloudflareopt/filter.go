package cloudflareopt

import (
	"fmt"
	"strings"
)

type RealIPPlan struct {
	Append    []string          `json:"append"`
	Prepend   []string          `json:"prepend"`
	CoveredBy map[string]string `json:"covered_by"`
	Conflicts map[string]string `json:"conflicts"`
}

func PlanRealIP(mode string, existing []string, domains []string) RealIPPlan {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "blacklist"
	}
	plan := RealIPPlan{Append: []string{}, Prepend: []string{}, CoveredBy: map[string]string{}, Conflicts: map[string]string{}}
	seenAppend := map[string]bool{}
	seenPrepend := map[string]bool{}
	for _, domain := range domains {
		domain = normalizeDomain(domain)
		if domain == "" {
			continue
		}
		switch mode {
		case "blacklist":
			matched := ""
			for _, entry := range existing {
				if filterPatternMatches(entry, domain) {
					matched = strings.TrimSpace(entry)
					break
				}
			}
			if matched != "" {
				plan.CoveredBy[domain] = matched
				continue
			}
			if !seenAppend[domain] {
				plan.Append = append(plan.Append, domain)
				seenAppend[domain] = true
			}
		case "whitelist":
			matched := ""
			for _, entry := range existing {
				if filterPatternMatches(entry, domain) {
					matched = strings.TrimSpace(entry)
					break
				}
			}
			if matched != "" {
				plan.Conflicts[domain] = fmt.Sprintf("existing whitelist entry %q makes this domain use fake-IP", matched)
			} else {
				plan.CoveredBy[domain] = "whitelist-unmatched"
			}
		case "rule":
			action, matched := firstRuleAction(existing, domain)
			switch action {
			case "real-ip":
				plan.CoveredBy[domain] = matched
			case "fake-ip":
				plan.Conflicts[domain] = fmt.Sprintf("earlier fake-ip rule %q wins before an optimizer rule can be appended", matched)
			default:
				rule := "DOMAIN," + domain + ",real-ip"
				if !seenPrepend[rule] {
					plan.Prepend = append(plan.Prepend, rule)
					seenPrepend[rule] = true
				}
			}
		default:
			plan.Conflicts[domain] = fmt.Sprintf("unsupported fake-ip-filter-mode %q", mode)
		}
	}
	return plan
}

func filterPatternMatches(entry, domain string) bool {
	pattern := strings.ToLower(strings.TrimSpace(entry))
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, ",") {
		_, matched := parseRuleMatch(pattern, domain)
		return matched
	}
	if strings.HasPrefix(pattern, "+.") {
		base := strings.TrimPrefix(pattern, "+.")
		return domain == base || strings.HasSuffix(domain, "."+base)
	}
	if strings.HasPrefix(pattern, "*.") {
		base := strings.TrimPrefix(pattern, "*.")
		if !strings.HasSuffix(domain, "."+base) {
			return false
		}
		prefix := strings.TrimSuffix(domain, "."+base)
		return prefix != "" && !strings.Contains(prefix, ".")
	}
	if strings.HasPrefix(pattern, ".") {
		base := strings.TrimPrefix(pattern, ".")
		return domain == base || strings.HasSuffix(domain, "."+base)
	}
	return domain == normalizeDomain(pattern)
}

func firstRuleAction(entries []string, domain string) (string, string) {
	for _, entry := range entries {
		action, matched := parseRuleMatch(strings.ToLower(strings.TrimSpace(entry)), domain)
		if matched {
			return action, strings.TrimSpace(entry)
		}
	}
	return "", ""
}

func parseRuleMatch(entry, domain string) (string, bool) {
	parts := strings.Split(entry, ",")
	if len(parts) < 2 {
		return "", false
	}
	kind := strings.ToUpper(strings.TrimSpace(parts[0]))
	domain = normalizeDomain(domain)
	action := ""
	if len(parts) >= 3 {
		action = strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	}
	switch kind {
	case "MATCH":
		if len(parts) == 2 {
			action = strings.ToLower(strings.TrimSpace(parts[1]))
		}
		return action, true
	case "DOMAIN":
		return action, normalizeDomain(parts[1]) == domain
	case "DOMAIN-SUFFIX":
		base := normalizeDomain(parts[1])
		return action, domain == base || strings.HasSuffix(domain, "."+base)
	case "DOMAIN-KEYWORD":
		return action, strings.Contains(domain, strings.ToLower(strings.TrimSpace(parts[1])))
	default:
		return "", false
	}
}
