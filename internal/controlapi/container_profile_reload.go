package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
)

type containerProfileReloadCandidate struct {
	Payload       []byte
	SourceDigest  string
	OverlayDigest string
	Revision      string
}

// prepareContainerProfileReload materializes the latest persisted global
// profile overlay against the raw source that produced the currently desired
// profile. A plain gateway reload can therefore promote an externally updated
// overlay instead of restarting the previous effective profile.
//
// Returning nil means the persisted overlay already matches the desired
// profile metadata, so the caller should perform the ordinary reload path.
func prepareContainerProfileReload(configPath, storeDir string) (*containerProfileReloadCandidate, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(storeDir) == "" {
		return nil, fmt.Errorf("container control store is not configured")
	}
	store := NewStore(storeDir)
	_, document, overlayRevision, err := loadPersistedProfileOverlay(store)
	if err != nil {
		return nil, err
	}
	effectiveOverlayRevision := ""
	if document.Enabled {
		effectiveOverlayRevision = overlayRevision
	}
	if cfg.Mihomo.ProfileOverlayDigest == effectiveOverlayRevision {
		return nil, nil
	}

	source, sourceDigest, err := rawProfileSourceForReload(cfg, store)
	if err != nil {
		return nil, err
	}
	composition, err := mihomo.ComposeProfileOverlay(source, document)
	if err != nil {
		return nil, fmt.Errorf("compose latest global profile overlay: %w", err)
	}
	payload := []byte(composition.ProfileYAML)
	if len(payload) == 0 {
		return nil, fmt.Errorf("composed profile is empty")
	}
	return &containerProfileReloadCandidate{
		Payload:       payload,
		SourceDigest:  sourceDigest,
		OverlayDigest: effectiveOverlayRevision,
		Revision:      fileDigest(configPath),
	}, nil
}

func loadPersistedProfileOverlay(store *Store) ([]byte, mihomo.ProfileOverlayDocument, string, error) {
	data, err := store.ProfileOverlay()
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(data)) == 0) {
		data, err = mihomo.RenderProfileOverlay(mihomo.DefaultProfileOverlayDocument())
	}
	if err != nil {
		return nil, mihomo.ProfileOverlayDocument{}, "", err
	}
	if len(data) > maxProfileOverlaySize {
		return nil, mihomo.ProfileOverlayDocument{}, "", fmt.Errorf("stored global profile overlay exceeds %d bytes", maxProfileOverlaySize)
	}
	document, err := mihomo.ParseProfileOverlay(data)
	if err != nil {
		return data, mihomo.ProfileOverlayDocument{}, "", fmt.Errorf("stored global profile overlay is invalid: %w", err)
	}
	return data, document, mihomo.ProfileOverlayDigest(data), nil
}

func rawProfileSourceForReload(cfg config.Config, store *Store) ([]byte, string, error) {
	if cfg.Mihomo.ProfileMode == config.MihomoProfileModeManaged {
		text, err := mihomo.RenderManagedBaseProfile(cfg)
		if err != nil {
			return nil, "", err
		}
		data := []byte(text)
		return data, mihomo.ProfileOverlayDigest(data), nil
	}
	if cfg.Mihomo.ProfileMode != config.MihomoProfileModeImported {
		return nil, "", fmt.Errorf("profile mode %q cannot be recomposed with the global overlay", cfg.Mihomo.ProfileMode)
	}

	digest := strings.TrimSpace(cfg.Mihomo.ProfileSourceDigest)
	if digest != "" {
		if data, ok, err := sourceSnapshotByDigest(store, digest); err != nil {
			return nil, "", err
		} else if ok {
			return data, digest, nil
		}
		if data, ok, err := policyWorkspaceBaseSource(cfg, digest); err != nil {
			return nil, "", err
		} else if ok {
			return data, digest, nil
		}
	}

	// An imported profile with no applied overlay is itself a safe raw source.
	// Once an overlay has been applied we must never compose on top of that
	// effective profile because additions would be duplicated on every reload.
	if cfg.Mihomo.ProfileOverlayDigest == "" {
		data, err := os.ReadFile(cfg.Mihomo.Profile)
		if err != nil {
			return nil, "", fmt.Errorf("read current imported profile: %w", err)
		}
		actual := mihomo.ProfileOverlayDigest(data)
		if digest != "" && digest != actual {
			return nil, "", fmt.Errorf("current imported profile no longer matches its recorded source digest")
		}
		return data, actual, nil
	}
	if digest == "" {
		return nil, "", fmt.Errorf("raw source is unavailable for the currently applied profile; re-import or reselect the source before reloading a changed overlay")
	}
	return nil, "", fmt.Errorf("raw source %s is unavailable; re-import or reselect the source before reloading the changed overlay", digest)
}

func sourceSnapshotByDigest(store *Store, digest string) ([]byte, bool, error) {
	sources, err := store.Sources()
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, source := range sources {
		if source.Digest == digest {
			data, _, err := readValidatedSourceSnapshot(store.Dir(), source)
			return data, err == nil, err
		}
		for _, version := range source.Versions {
			if version.Digest != digest {
				continue
			}
			candidate := source
			candidate.Digest = version.Digest
			candidate.SnapshotPath = version.SnapshotPath
			data, _, err := readValidatedSourceSnapshot(store.Dir(), candidate)
			return data, err == nil, err
		}
	}
	return nil, false, nil
}

func policyWorkspaceBaseSource(cfg config.Config, digest string) ([]byte, bool, error) {
	data, err := os.ReadFile(policyWorkspaceBasePath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var base policyWorkspaceBase
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, false, fmt.Errorf("read policy workspace base: %w", err)
	}
	if base.SourceDigest != digest || len(base.Source) == 0 {
		return nil, false, nil
	}
	if mihomo.ProfileOverlayDigest(base.Source) != digest {
		return nil, false, fmt.Errorf("policy workspace base no longer matches its recorded source digest")
	}
	return base.Source, true, nil
}

func (r ContainerRunner) reloadLatestPersistedProfile(ctx context.Context, configPath string) (bool, error) {
	candidate, err := prepareContainerProfileReload(configPath, r.StoreDir)
	if err != nil {
		return false, err
	}
	if candidate == nil {
		return false, nil
	}
	result, err := (DirectRunner{}).ApplyProfile(ctx, configPath, candidate.Revision, candidate.Payload, candidate.SourceDigest, candidate.OverlayDigest)
	if err != nil {
		return false, fmt.Errorf("apply latest persisted profile overlay before reload: %w", err)
	}
	if !result.Reloaded {
		return false, fmt.Errorf("latest persisted profile overlay was saved but the running gateway was not reloaded")
	}
	return true, nil
}
