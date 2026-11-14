package gateway

import "fmt"

type ReloadPhase string

const (
	ReloadValidation ReloadPhase = "validation"
	ReloadStop       ReloadPhase = "stop"
	ReloadStart      ReloadPhase = "start"
)

// ReloadError preserves the failed lifecycle phase across configuration and
// helper boundaries. It does not establish final gateway health.
type ReloadError struct {
	Phase ReloadPhase
	Err   error
}

func (e *ReloadError) Error() string {
	label := "reload " + string(e.Phase) + " failed"
	if e.Phase == ReloadValidation {
		label = "reload candidate validation failed"
	} else if e.Phase == ReloadStart {
		label += " after gateway stop"
	}
	return fmt.Sprintf("%s: %v", label, e.Err)
}

func (e *ReloadError) Unwrap() error { return e.Err }
