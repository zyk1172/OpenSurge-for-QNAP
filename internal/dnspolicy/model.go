package dnspolicy

const SchemaVersion = 1

type Mode string

const (
	ModeInheritProfile Mode = "inherit_profile"
	ModeManaged        Mode = "managed"
)

type ResolverOwner string

const (
	ResolverOwnerProfile   ResolverOwner = "profile"
	ResolverOwnerOpenSurge ResolverOwner = "opensurge"
)

type CacheAlgorithm string

const (
	CacheAlgorithmLRU CacheAlgorithm = "lru"
	CacheAlgorithmARC CacheAlgorithm = "arc"
)

type FakeIPFilterMode string

const (
	FakeIPFilterBlacklist FakeIPFilterMode = "blacklist"
	FakeIPFilterWhitelist FakeIPFilterMode = "whitelist"
)

// Document is the persistent DNS policy owned by OpenSurge. It deliberately
// does not contain LAN-facing listener settings: dnsmasq listen/port and the
// mihomo loopback listener remain gateway infrastructure owned by the normal
// OpenSurge configuration.
//
// ModeInheritProfile preserves the historical behavior where resolver policy
// comes from the imported mihomo profile and global profile overlay. Managed
// remains persisted while inactive so users can switch ownership without
// losing their OpenSurge-managed draft.
type Document struct {
	SchemaVersion int           `json:"schema_version"`
	Mode          Mode          `json:"mode"`
	Managed       ManagedPolicy `json:"managed"`
}

type ManagedPolicy struct {
	DefaultNameservers           []string               `json:"default_nameservers"`
	Nameservers                  []string               `json:"nameservers"`
	DirectNameservers            []string               `json:"direct_nameservers"`
	DirectNameserverFollowPolicy bool                   `json:"direct_nameserver_follow_policy"`
	ProxyServerNameservers       []string               `json:"proxy_server_nameservers"`
	Fallback                     []string               `json:"fallback"`
	NameserverPolicy             []NameserverPolicyRule `json:"nameserver_policy"`
	FallbackFilter               FallbackFilter         `json:"fallback_filter"`
	FakeIPFilter                 []string               `json:"fake_ip_filter"`
	FakeIPFilterMode             FakeIPFilterMode       `json:"fake_ip_filter_mode"`
	RespectRules                 bool                   `json:"respect_rules"`
	CacheAlgorithm               CacheAlgorithm         `json:"cache_algorithm"`
	PreferH3                     bool                   `json:"prefer_h3"`
}

type NameserverPolicyRule struct {
	Match       string   `json:"match"`
	Nameservers []string `json:"nameservers"`
}

type FallbackFilter struct {
	GeoIP     bool     `json:"geoip"`
	GeoIPCode string   `json:"geoip_code"`
	GeoSite   []string `json:"geosite"`
	IPCIDR    []string `json:"ipcidr"`
	Domain    []string `json:"domain"`
}

// Snapshot is the optimistic-concurrency boundary used by future Control API
// and UI work. Revision is a SHA-256 digest of the normalized document and is
// intentionally not persisted inside Document, avoiding self-referential
// revision data.
type Snapshot struct {
	Document Document `json:"document"`
	Revision string   `json:"revision"`
}

// Effective describes which configuration source owns resolver policy. PR1
// establishes this ownership contract only; it does not alter rendered mihomo
// configuration. The compiler that consumes Managed is introduced separately.
type Effective struct {
	SchemaVersion int            `json:"schema_version"`
	Mode          Mode           `json:"mode"`
	ResolverOwner ResolverOwner  `json:"resolver_owner"`
	Managed       *ManagedPolicy `json:"managed,omitempty"`
}

func DefaultDocument() Document {
	return Document{
		SchemaVersion: SchemaVersion,
		Mode:          ModeInheritProfile,
		Managed: ManagedPolicy{
			DefaultNameservers:     []string{},
			Nameservers:            []string{},
			DirectNameservers:      []string{},
			ProxyServerNameservers: []string{},
			Fallback:               []string{},
			NameserverPolicy:       []NameserverPolicyRule{},
			FallbackFilter: FallbackFilter{
				GeoSite: []string{},
				IPCIDR:  []string{},
				Domain:  []string{},
			},
			FakeIPFilter:     []string{},
			FakeIPFilterMode: FakeIPFilterBlacklist,
			CacheAlgorithm:   CacheAlgorithmARC,
		},
	}
}

func Resolve(document Document) (Effective, error) {
	normalized, err := Normalize(document)
	if err != nil {
		return Effective{}, err
	}
	effective := Effective{
		SchemaVersion: SchemaVersion,
		Mode:          normalized.Mode,
		ResolverOwner: ResolverOwnerProfile,
	}
	if normalized.Mode == ModeManaged {
		managed := normalized.Managed
		effective.ResolverOwner = ResolverOwnerOpenSurge
		effective.Managed = &managed
	}
	return effective, nil
}
