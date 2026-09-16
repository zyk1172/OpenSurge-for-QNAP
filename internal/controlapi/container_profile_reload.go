package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/gateway"
	"open-mihomo-gateway/internal/mihomo"
)

type containerProfileReconcileCandidate struct {
	Payload       []byte
	SourceDigest  string
	OverlayDigest string
	Revision      string
}

type containerProfileReconcileResult struct {
	Changed  bool
	Reloaded bool
}

// prepareContainerProfileReconcile materializes the latest persisted global
// profile overlay against the raw source that produced the currently desired
// profile. Lifecycle operations must reconcile this desired input before they
// start or restart Mihomo, rather than trusting profile metadata alone.
//
// Returning nil means both the composition metadata and the actual effective
// profile content already match the latest persisted overlay.
func prepareContainerProfileReconcile(configPath, storeDir string) (*containerProfileReconcileCandidate, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(storeDir) == "" {
		storeDir = filepath.Join(filepath.Dir(filepath.Dir(configPath)), "control")
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

	// A managed profile with no enabled overlay is already authoritative and
	// does not need to be converted into an imported effective profile.
	if cfg.Mihomo.ProfileMode == config.MihomoProfileModeManaged && !document.Enabled {
		return nil, nil
	}

	source, sourceDigest, err := rawProfileSourceForReconcile(cfg, store)
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

	currentDigest, digestErr := config.MihomoProfileDigest(cfg)
	if digestErr != nil && !errors.Is(digestErr, os.ErrNotExist) {
		return nil, fmt.Errorf("digest current effective profile: %w", digestErr)
	}
	if cfg.Mihomo.ProfileMode == config.MihomoProfileModeImported &&
		cfg.Mihomo.ProfileSourceDigest == sourceDigest &&
		cfg.Mihomo.ProfileOverlayDigest == effectiveOverlayRevision &&
		currentDigest == composition.Digest {
		return nil, nil
	}

	return &containerProfileReconcileCandidate{
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

func rawProfileSourceForReconcile(cfg config.Config, store *Store) ([]byte, string, error) {
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
	// effective profile because additions would be duplicated on every lifecycle
	// transition.
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
		return nil, "", fmt.Errorf("raw source is unavailable for the currently applied profile; re-import or reselect the source before applying a changed overlay")
	}
	return nil, "", fmt.Errorf("raw source %s is unavailable; re-import or reselect the source before applying the changed overlay", digest)
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

// reconcileLatestPersistedProfile promotes the latest persisted overlay into
// the desired effective profile. start/recovery use materializeOnly because no
// live data-plane transition should happen before the requested start/recovery;
// reload-like actions reuse ApplyProfile's transactional running reload.
func (r ContainerRunner) reconcileLatestPersistedProfile(ctx context.Context, configPath string, materializeOnly bool) (containerProfileReconcileResult, error) {
	candidate, err := prepareContainerProfileReconcile(configPath, r.StoreDir)
	if err != nil {
		return containerProfileReconcileResult{}, err
	}
	if candidate == nil {
		return containerProfileReconcileResult{}, nil
	}
	if materializeOnly {
		if err := materializeContainerProfileCandidate(ctx, configPath, candidate); err != nil {
			return containerProfileReconcileResult{}, fmt.Errorf("materialize latest persisted profile overlay: %w", err)
		}
		return containerProfileReconcileResult{Changed: true}, nil
	}
	result, err := (DirectRunner{}).ApplyProfile(ctx, configPath, candidate.Revision, candidate.Payload, candidate.SourceDigest, candidate.OverlayDigest)
	if err != nil {
		return containerProfileReconcileResult{}, fmt.Errorf("apply latest persisted profile overlay before lifecycle transition: %w", err)
	}
	return containerProfileReconcileResult{Changed: true, Reloaded: result.Reloaded}, nil
}

func materializeContainerProfileCandidate(ctx context.Context, configPath string, candidate *containerProfileReconcileCandidate) error {
	deps := defaultLockedProfileApplyDeps()
	deps.stateExists = func(config.Config) (bool, error) { return false, nil }
	deps.reload = func(context.Context, config.Config) error {
		return fmt.Errorf("unexpected runtime reload while materializing profile")
	}
	deps.start = func(context.Context, config.Config) error {
		return fmt.Errorf("unexpected runtime start while materializing profile")
	}
	return withConfigurationLifecycleLock(configPath, func() error {
		_, err := applyProfile(ctx, configPath, candidate.Revision, candidate.Payload, candidate.SourceDigest, candidate.OverlayDigest, deps)
		return err
	})
}

// RecoverContainerConfigAfterRestart reconciles persistent desired profile
// inputs before gateway recovery. This prevents a recreated container from
// restarting the previously materialized Hosts/rules profile while a newer
// persisted overlay is waiting in the control store.
func RecoverContainerConfigAfterRestart(ctx context.Context, configPath, storeDir string) (bool, error) {
	runner := ContainerRunner{StoreDir: storeDir}
	if _, err := runner.reconcileLatestPersistedProfile(ctx, configPath, true); err != nil {
		return false, fmt.Errorf("reconcile persisted profile before container recovery: %w", err)
	}
	return gateway.RecoverConfigAfterContainerRestart(ctx, configPath)
}
