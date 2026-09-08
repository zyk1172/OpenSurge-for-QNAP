//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"open-mihomo-gateway/internal/platform"
)

const DefaultTableName = "opensurge"
const nftFamily = "inet"

const (
	chainPrerouting  = "prerouting"
	chainPostrouting = "postrouting"
)

const commentPrefix = "opensurge:"

// renderRuleset builds the complete nftables ruleset owned by OpenSurge. The
// table is replaced atomically, but callers must prove an existing table is ours
// before applying this replacement.
func renderRuleset(cfg platform.NATConfig) (string, error) {
	if err := validateInterfaceName(cfg.LANInterface); err != nil {
		return "", err
	}
	table := strings.TrimSpace(cfg.TableName)
	if table == "" {
		table = DefaultTableName
	}
	if err := validateTableName(table); err != nil {
		return "", err
	}
	if _, err := validateCIDR(cfg.LANCIDR); err != nil {
		return "", err
	}
	if cfg.TUNDevice != "" {
		if err := validateInterfaceName(cfg.TUNDevice); err != nil {
			return "", err
		}
	}
	mark := fmt.Sprintf("0x%08x", cfg.FwMark)

	var out strings.Builder
	// A bare declaration followed by delete+create lets nft apply a complete
	// replacement in one netlink transaction. applyNAT verifies ownership first.
	fmt.Fprintf(&out, "table %s %s\n", nftFamily, table)
	fmt.Fprintf(&out, "delete table %s %s\n", nftFamily, table)
	fmt.Fprintf(&out, "table %s %s {\n", nftFamily, table)
	fmt.Fprintf(&out, "\tchain %s {\n", chainPrerouting)
	fmt.Fprintf(&out, "\t\ttype filter hook prerouting priority filter; policy accept;\n")
	fmt.Fprintf(&out, "\t\tiifname %q ip daddr != %s meta mark set %s comment %q\n",
		cfg.LANInterface, cfg.LANCIDR, mark,
		fmt.Sprintf("%s forward LAN traffic to the OpenSurge routing table", commentPrefix))
	fmt.Fprintf(&out, "\t}\n")
	fmt.Fprintf(&out, "\tchain %s {\n", chainPostrouting)
	fmt.Fprintf(&out, "\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	if cfg.Masquerade {
		egress := cfg.UpstreamInterface
		if strings.TrimSpace(egress) == "" {
			egress = cfg.LANInterface
		}
		if err := validateInterfaceName(egress); err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "\t\tmeta mark %s oifname %q masquerade comment %q\n",
			mark, egress,
			fmt.Sprintf("%s source NAT for direct fallback egress", commentPrefix))
	}
	fmt.Fprintf(&out, "\t}\n")
	fmt.Fprintf(&out, "}\n")
	return out.String(), nil
}

func (b *Backend) applyNAT(ctx context.Context, cfg platform.NATConfig) error {
	if b.runner.nftPath == "" {
		return platform.NewError(platform.CodeNFTablesUnavailable, "nft not found in PATH")
	}
	table := strings.TrimSpace(cfg.TableName)
	if table == "" {
		table = DefaultTableName
		cfg.TableName = table
	}
	if exists, err := b.tableExists(ctx, table); err != nil {
		return err
	} else if exists {
		owned, err := b.tableOwned(ctx, table)
		if err != nil {
			return err
		}
		if !owned {
			return platform.NewError(platform.CodeNFTablesForeignTable,
				"refusing to replace an nftables table whose OpenSurge ownership cannot be proven").
				WithDetail("table", table)
		}
	}

	ruleset, err := renderRuleset(cfg)
	if err != nil {
		return err
	}
	dir := b.tempDir
	if dir == "" {
		dir = os.TempDir()
	}
	file, err := os.CreateTemp(dir, "opensurge-nft-*.ruleset")
	if err != nil {
		return platform.NewError(platform.CodeCommandFailed, "create nftables ruleset file").Wrap(err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.WriteString(ruleset); err != nil {
		_ = file.Close()
		return platform.NewError(platform.CodeCommandFailed, "write nftables ruleset file").Wrap(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return platform.NewError(platform.CodeCommandFailed, "sync nftables ruleset file").Wrap(err)
	}
	if err := file.Close(); err != nil {
		return platform.NewError(platform.CodeCommandFailed, "close nftables ruleset file").Wrap(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return platform.NewError(platform.CodeCommandFailed, "chmod nftables ruleset file").Wrap(err)
	}
	return b.runner.run(ctx, b.runner.nftPath, "-f", path)
}

// removeNAT is the process-local convenience path. Cross-process Stop/restore
// uses removeNATTable with the table name persisted in NetworkSnapshot.
func (b *Backend) removeNAT(ctx context.Context) error {
	return b.removeNATTable(ctx, b.tableName)
}

func (b *Backend) removeNATTable(ctx context.Context, table string) error {
	if b.runner.nftPath == "" {
		return platform.NewError(platform.CodeNFTablesUnavailable, "nft not found in PATH")
	}
	if strings.TrimSpace(table) == "" {
		table = DefaultTableName
	}
	if err := validateTableName(table); err != nil {
		return err
	}
	exists, err := b.tableExists(ctx, table)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	owned, err := b.tableOwned(ctx, table)
	if err != nil {
		return err
	}
	if !owned {
		return platform.NewError(platform.CodeNFTablesForeignTable,
			"refusing to delete an nftables table whose OpenSurge ownership cannot be proven").
			WithDetail("table", table)
	}
	return b.runner.run(ctx, b.runner.nftPath, "delete", "table", nftFamily, table)
}

func (b *Backend) tableExists(ctx context.Context, table string) (bool, error) {
	tables, err := b.listTables(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range tables {
		if name == table {
			return true, nil
		}
	}
	return false, nil
}

type nftTables struct {
	NFTables []struct {
		Table struct {
			Name   string `json:"name"`
			Family string `json:"family"`
		} `json:"table"`
	} `json:"nftables"`
}

func (b *Backend) listTables(ctx context.Context) ([]string, error) {
	out, err := b.runner.output(ctx, b.runner.nftPath, "-j", "list", "tables")
	if err != nil {
		return nil, err
	}
	var parsed nftTables
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, platform.NewError(platform.CodeCommandFailed, "parse nft list tables output").Wrap(err)
	}
	names := make([]string, 0, len(parsed.NFTables))
	for _, entry := range parsed.NFTables {
		if entry.Table.Family == nftFamily {
			names = append(names, entry.Table.Name)
		}
	}
	return names, nil
}

// tableOwned verifies the live table still has the minimal shape OpenSurge
// creates. This protects Stop from deleting a foreign table that replaced ours
// after startup. Every rule must carry the OpenSurge comment prefix and no
// unexpected nft object types may be present.
func (b *Backend) tableOwned(ctx context.Context, table string) (bool, error) {
	out, err := b.runner.output(ctx, b.runner.nftPath, "-j", "list", "table", nftFamily, table)
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var payload struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return false, platform.NewError(platform.CodeCommandFailed, "parse nft table ownership output").Wrap(err)
	}
	seenRule := false
	seenPrerouting := false
	seenPostrouting := false
	for _, entry := range payload.NFTables {
		for kind, raw := range entry {
			switch kind {
			case "metainfo", "table":
				continue
			case "chain":
				var chain struct {
					Table string `json:"table"`
					Name  string `json:"name"`
				}
				if err := json.Unmarshal(raw, &chain); err != nil || chain.Table != table {
					return false, nil
				}
				switch chain.Name {
				case chainPrerouting:
					seenPrerouting = true
				case chainPostrouting:
					seenPostrouting = true
				default:
					return false, nil
				}
			case "rule":
				var rule struct {
					Table   string `json:"table"`
					Chain   string `json:"chain"`
					Comment string `json:"comment"`
				}
				if err := json.Unmarshal(raw, &rule); err != nil || rule.Table != table {
					return false, nil
				}
				if rule.Chain != chainPrerouting && rule.Chain != chainPostrouting {
					return false, nil
				}
				if !strings.HasPrefix(rule.Comment, commentPrefix) {
					return false, nil
				}
				seenRule = true
			default:
				return false, nil
			}
		}
	}
	return seenRule && seenPrerouting && seenPostrouting, nil
}

func (b *Backend) describeTableNamed(ctx context.Context, table string) (string, error) {
	exists, err := b.tableExists(ctx, table)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	out, err := b.runner.output(ctx, b.runner.nftPath, "list", "table", nftFamily, table)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (b *Backend) describeTable(ctx context.Context) (string, error) {
	return b.describeTableNamed(ctx, b.tableName)
}

func (b *Backend) nftCapabilities(ctx context.Context) (present bool, jsonOK bool, version string) {
	if b.runner.nftPath == "" {
		return false, false, ""
	}
	present = true
	if out, err := b.runner.output(ctx, b.runner.nftPath, "--version"); err == nil {
		version = strings.TrimSpace(string(out))
	}
	if _, err := b.runner.output(ctx, b.runner.nftPath, "-j", "list", "tables"); err == nil {
		jsonOK = true
	}
	return present, jsonOK, version
}
