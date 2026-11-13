package mihomo

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/metacubex/bbolt"
	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/device"
	"open-mihomo-gateway/internal/runtime"
)

// PrepareDevicePolicy binds the gateway's applied snapshot to the same target
// inventory used by the renderer. Call before constructing lifecycle services.
func PrepareDevicePolicy(cfg *config.Config) error {
	if err := config.PrepareDevicePolicy(cfg); err != nil {
		return err
	}
	if cfg.DevicePolicy.Bundle == nil {
		return nil
	}
	var imported *importedProfile
	if cfg.Mihomo.ProfileMode == config.MihomoProfileModeImported {
		loaded, err := loadImportedProfile(cfg.Mihomo.Profile)
		if err != nil {
			return err
		}
		imported = &loaded
	}
	return resolveDevicePolicy(cfg, imported)
}

func resolveDevicePolicy(cfg *config.Config, imported *importedProfile) error {
	bundle := cfg.DevicePolicy.Bundle
	if bundle == nil {
		return nil
	}
	targets := []string{}
	if imported != nil {
		for target := range imported.inventory.targets {
			targets = append(targets, target)
		}
	} else if cfg.UpstreamProxy.Enabled {
		targets = append(targets, cfg.UpstreamProxy.Name, "open-surge-egress")
	}
	if cfg.Tailscale.Enabled && cfg.Tailscale.ExitNode != "" {
		targets = append(targets, config.TailscaleProxyName, config.TailscaleExitGroupName)
	}
	slices.Sort(targets)
	targets = slices.Compact(targets)
	if bundle.Resolution != nil && slices.Equal(bundle.Resolution.AvailableTargets, targets) {
		return nil
	}
	resolution := device.PolicyResolution{AvailableTargets: targets}
	if bundle.Resolution != nil {
		resolution.Selections = maps.Clone(bundle.Resolution.Selections)
		resolution.SelectionsUnavailable = bundle.Resolution.SelectionsUnavailable
	}
	// Healthy policies require no controller request or cache read. Only an
	// obsolete selector candidate makes the historical choice relevant.
	hasMissing := false
	for _, group := range bundle.Compiled.SelectorGroups {
		for _, target := range group.Policies {
			if !builtinPolicyTarget(target) && !slices.Contains(targets, target) {
				hasMissing = true
				break
			}
		}
		if hasMissing {
			break
		}
	}
	if hasMissing {
		selected, unavailable := devicePolicySelections(*cfg)
		if resolution.Selections == nil {
			resolution.Selections = map[string]string{}
		}
		maps.Copy(resolution.Selections, selected)
		resolution.SelectionsUnavailable = unavailable
	}
	resolved, err := device.ResolvePolicyBundle(*bundle, resolution)
	if err != nil {
		return err
	}
	cfg.DevicePolicy.Bundle = &resolved
	return nil
}

func devicePolicySelections(cfg config.Config) (map[string]string, bool) {
	selections := map[string]string{}
	paths := runtime.NewPaths(cfg)
	// A running fallback omits its selector. Keep that original selection from
	// the applied compilation context until the source is restored.
	if applied, err := device.LoadPolicyBundleSnapshot(paths.DevicePolicyApplied); err == nil && applied.Resolution != nil {
		for group, selected := range applied.Resolution.Selections {
			selections[group] = selected
		}
	}
	apiConfig := cfg
	_, running, _ := runtime.LoadState(paths.StateFile)
	if !running {
		if prepared, exists, err := LoadPrepared(cfg); err == nil && exists {
			if prepared.DevicePolicyResolution != nil {
				maps.Copy(selections, prepared.DevicePolicyResolution.Selections)
			}
			apiConfig, running = PreparedConfig(cfg, prepared), true
		}
	}
	if running {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		groups, err := FetchProxyGroups(ctx, apiConfig)
		cancel()
		if err == nil {
			for _, group := range groups {
				if strings.HasPrefix(group.Name, "device/") && group.Selected != "" {
					selections[group.Name] = group.Selected
				}
			}
			return selections, false
		}
	}
	cached, err := readDevicePolicySelections(cfg.DevicePolicy.SelectionCachePath)
	for group, selected := range cached {
		selections[group] = selected
	}
	return selections, err != nil
}

// The selected bucket is the format used by the pinned Mihomo 1.19.33 core.
// Open read-only with a bounded lock wait; never create, repair or rewrite its
// cache, and never open a second writer alongside the running core.
func readDevicePolicySelections(path string) (map[string]string, error) {
	selections := map[string]string{}
	if path == "" {
		return selections, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return selections, nil
	} else if err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: 50 * time.Millisecond})
	if err != nil {
		return nil, err
	}
	defer db.Close()
	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("selected"))
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(key, value []byte) error {
			if strings.HasPrefix(string(key), "device/") && value != nil {
				selections[string(key)] = string(value)
			}
			return nil
		})
	})
	return selections, err
}
