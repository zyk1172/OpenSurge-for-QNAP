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

// DefaultTableName is the only nftables table OpenSurge is allowed to touch.
// Everything in this backend is scoped to it.
const DefaultTableName = "opensurge"

// NFTables family OpenSurge uses. `inet` covers IPv4 and IPv6 in one table, so a
// future IPv6 phase does not need a second table.
const nftFamily = "inet"

// chain names inside the OpenSurge table.
const (
	chainPrerouting  = "prerouting"
	chainPostrouting = "postrouting"
)

// commentPrefix marks every rule OpenSurge creates, so an operator (and our own
// rollback) can tell them apart from QNAP firewall, Container Station or user
// rules at a glance.
const commentPrefix = "opensurge:"

// renderRuleset builds the complete nftables ruleset for OpenSurge.
//
// Design constraints enforced here:
//
//   - Only `table inet opensurge` is declared. Nothing else is referenced.
//   - There is no `flush ruleset`, and no `flush table` of anything we do not own.
//   - The ruleset is applied as one nft transaction, so a syntax error or a
//     missing hook cannot leave a half-written table behind.
//   - Every rule carries a comment identifying it as ours.
//
// nftables does two jobs for us, and only two: mark forwarded LAN traffic so
// policy routing can steer it into the TUN, and optionally masquerade it during
// an explicit direct-fallback. There is no TPROXY and no REDIRECT; transparent
// proxying is entirely mihomo's TUN.
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

	var b strings.Builder
	// The bare declaration guarantees the table exists so the following delete
	// is always valid; the pair is applied in a single transaction, making this
	// an atomic replace of OpenSurge's own table and nothing else.
	fmt.Fprintf(&b, "table %s %s\n", nftFamily, table)
	fmt.Fprintf(&b, "delete table %s %s\n", nftFamily, table)
	fmt.Fprintf(&b, "table %s %s {\n", nftFamily, table)
	fmt.Fprintf(&b, "\tchain %s {\n", chainPrerouting)
	fmt.Fprintf(&b, "\t\ttype filter hook prerouting priority filter; policy accept;\n")
	fmt.Fprintf(&b, "\t\tiifname %q ip daddr != %s meta mark set %s comment %q\n",
		cfg.LANInterface, cfg.LANCIDR, mark,
		fmt.Sprintf("%s forward LAN traffic to the OpenSurge routing table", commentPrefix))
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\tchain %s {\n", chainPostrouting)
	fmt.Fprintf(&b, "\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	if cfg.Masquerade {
		// Direct fallback only. Without masquerade the upstream router would
		// reply straight to the LAN client, producing an asymmetric path that
		// breaks stateful middleboxes.
		fmt.Fprintf(&b, "\t\tmeta mark %s oifname %q masquerade comment %q\n",
			mark, cfg.LANInterface,
			fmt.Sprintf("%s source NAT for direct fallback egress", commentPrefix))
	}
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "}\n")
	return b.String(), nil
}

// applyNAT renders and applies the ruleset atomically.
func (b *Backend) applyNAT(ctx context.Context, cfg platform.NATConfig) error {
	if b.runner.nftPath == "" {
		return platform.NewError(platform.CodeNFTablesUnavailable, "nft not found in PATH")
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
		file.Close()
		return platform.NewError(platform.CodeCommandFailed, "write nftables ruleset file").Wrap(err)
	}
	if err := file.Close(); err != nil {
		return platform.NewError(platform.CodeCommandFailed, "close nftables ruleset file").Wrap(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return platform.NewError(platform.CodeCommandFailed, "chmod nftables ruleset file").Wrap(err)
	}
	// nft -f submits the whole file as a single netlink batch.
	return b.runner.run(ctx, b.runner.nftPath, "-f", path)
}

// removeNAT deletes only the OpenSurge table, and is a no-op when it is absent.
func (b *Backend) removeNAT(ctx context.Context) error {
	if b.runner.nftPath == "" {
		return platform.NewError(platform.CodeNFTablesUnavailable, "nft not found in PATH")
	}
	exists, err := b.tableExists(ctx, b.tableName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return b.runner.run(ctx, b.runner.nftPath, "delete", "table", nftFamily, b.tableName)
}

// tableExists reports whether the OpenSurge table is present.
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

// nftTables is the subset of `nft -j list tables` we use.
type nftTables struct {
	NFTables []struct {
		Table struct {
			Name   string `json:"name"`
			Family string `json:"family"`
		} `json:"table"`
	} `json:"nftables"`
}

// listTables enumerates tables in the inet family using the JSON output.
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

// describeTable returns the rendered ruleset of the OpenSurge table for
// diagnostics and for the diagnostic bundle.
func (b *Backend) describeTable(ctx context.Context) (string, error) {
	exists, err := b.tableExists(ctx, b.tableName)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	out, err := b.runner.output(ctx, b.runner.nftPath, "list", "table", nftFamily, b.tableName)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// nftCapabilities probes what the installed nft can actually do. JSON output is
// optional in older builds; when it is missing the backend still works but
// diagnostics degrade, so it is reported rather than fatal.
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
