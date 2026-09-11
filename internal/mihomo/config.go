package mihomo

import (
	"bytes"
	"strings"
	"text/template"

	"open-mihomo-gateway/internal/config"
)

const configTemplate = `mixed-port: {{ .MixedPort }}
allow-lan: {{ .LANProxyEnabled }}
bind-address: "{{ .MihomoBindAddress }}"
mode: rule
log-level: info
ipv6: {{ .IPv6Enabled }}
{{ if .TUNEnabled }}
interface-name: {{ .UpstreamInterface }}
{{ end }}

external-controller: {{ .APIAddr }}
{{- if .Secret }}
secret: {{ .Secret }}
{{- end }}

profile:
  store-selected: true
  store-fake-ip: {{ .StoreFakeIP }}

# Use MetaCubeX's documented CDN endpoints instead of the GitHub release URLs
# baked into mihomo. Imported profiles can contain GEOIP/GEOSITE/GEOASN rules,
# and engine validation must not depend on a slow GitHub asset download.
geox-url:
  geoip: https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.dat
  geosite: https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat
  mmdb: https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.metadb
  asn: https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/GeoLite2-ASN.mmdb

{{ if .Hosts }}hosts:
{{ .Hosts }}

{{ end }}dns:
  enable: true
  listen: 127.0.0.1:1053
  ipv6: {{ .DNSIPv6 }}
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
{{- if .DNSIPv6 }}
  fake-ip-range6: {{ .FakeIPv6Range }}
{{- end }}
{{ .DNSResolverFields }}

{{ if .TUNEnabled }}
tun:
  enable: true
  stack: {{ .TUNStack }}
  device: {{ .TUNDevice }}
  auto-route: {{ .TUNAutoRoute }}
  auto-detect-interface: {{ .TUNAutoDetectInterface }}
  strict-route: {{ .TUNStrictRoute }}
{{- if .TUNIPv6Enabled }}
  inet6-address:
    - {{ .TUNIPv6Address }}
{{- end }}
  dns-hijack:
    - any:53
{{- if .TUNRouteAddresses }}
  route-address:
{{ .TUNRouteAddresses }}
{{- end }}
{{- if not .TUNCustomRoutes }}
  route-exclude-address:
    - {{ .LANPrefix }}
    - 127.0.0.0/8
    - 10.0.0.0/8
    - 172.16.0.0/12
    - 192.168.0.0/16
    - 224.0.0.0/4
    - 255.255.255.255/32
{{- end }}

{{ end }}
{{ .PolicySections }}
`

func RenderConfig(cfg config.Config) (string, error) {
	tmpl, err := template.New("mihomo").Parse(configTemplate)
	if err != nil {
		return "", err
	}
	data, err := newTemplateData(cfg)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

type templateData struct {
	config.MihomoConfig
	TUNEnabled             bool
	TUNDevice              string
	TUNStack               string
	TUNAutoRoute           bool
	TUNAutoDetectInterface bool
	TUNStrictRoute         bool
	TUNRouteAddresses      string
	TUNCustomRoutes        bool
	LANProxyEnabled        bool
	MihomoBindAddress      string
	IPv6Enabled            bool
	TUNIPv6Enabled         bool
	TUNIPv6Address         string
	UpstreamInterface      string
	LANPrefix              string
	UpstreamProxy          config.UpstreamProxyConfig
	DNSIPv6                bool
	FakeIPv6Range          string
	Hosts                  string
	DNSResolverFields      string
	PolicySections         string
}

func newTemplateData(cfg config.Config) (templateData, error) {
	if err := config.PrepareDevicePolicy(&cfg); err != nil {
		return templateData{}, err
	}
	lanPrefix, err := cfg.LANPrefix()
	if err != nil {
		return templateData{}, err
	}
	var imported *importedProfile
	dnsResolverFields := defaultDNSResolverFieldsYAML
	hostsYAML := ""
	if cfg.Mihomo.ProfileMode == config.MihomoProfileModeImported {
		loaded, err := loadImportedProfile(cfg.Mihomo.Profile)
		if err != nil {
			return templateData{}, err
		}
		imported = &loaded
		dnsResolverFields = loaded.dnsResolverFields
		hostsYAML, err = loadProfileHostsYAML(cfg.Mihomo.Profile)
		if err != nil {
			return templateData{}, err
		}
	}
	if err := resolveDevicePolicy(&cfg, imported); err != nil {
		return templateData{}, err
	}
	policySections, err := renderPolicySections(cfg, imported)
	if err != nil {
		return templateData{}, err
	}
	policySections = rewriteQNAPIPv6IdentityRules(policySections, cfg)
	transparent := cfg.Transparent
	tunRouteAddresses := renderTailscaleRouteAddresses(cfg, lanPrefix)
	lanProxyEnabled := !transparent.TUNEnabled()
	mihomoBindAddress := "127.0.0.1"
	if lanProxyEnabled {
		mihomoBindAddress = "*"
	}
	if hostsYAML != "" {
		hostsYAML = indentYAMLBlock(hostsYAML, "  ")
	}
	return templateData{
		MihomoConfig:           cfg.Mihomo,
		TUNEnabled:             transparent.TUNEnabled(),
		TUNDevice:              transparent.TUNDevice,
		TUNStack:               transparent.TUNStack,
		TUNAutoRoute:           transparent.TUNAutoRoute,
		TUNAutoDetectInterface: transparent.TUNAutoDetectInterface,
		TUNStrictRoute:         transparent.TUNStrictRoute,
		TUNRouteAddresses:      tunRouteAddresses,
		TUNCustomRoutes:        tunRouteAddresses != "",
		LANProxyEnabled:        lanProxyEnabled,
		MihomoBindAddress:      mihomoBindAddress,
		IPv6Enabled:            cfg.DNS.IPv6 || transparent.TUNIPv6 != config.TUNIPv6Off,
		TUNIPv6Enabled:         transparent.TUNIPv6 != config.TUNIPv6Off,
		TUNIPv6Address:         config.MihomoTUNIPv6,
		UpstreamInterface:      cfg.Gateway.UpstreamInterface,
		LANPrefix:              lanPrefix,
		UpstreamProxy:          cfg.UpstreamProxy,
		DNSIPv6:                cfg.DNS.IPv6,
		FakeIPv6Range:          config.MihomoFakeIPv6Range,
		Hosts:                  hostsYAML,
		DNSResolverFields:      indentYAMLBlock(dnsResolverFields, "  "),
		PolicySections:         policySections,
	}, nil
}

func indentYAMLBlock(value, indent string) string {
	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}
