package platform

import (
	"errors"
	"fmt"
)

// Error codes for the network domain. The control API emits these verbatim so a
// Web UI can branch on a stable identifier instead of matching human-readable
// text that changes with translations.
const (
	CodeInterfaceNotFound       = "NETWORK_INTERFACE_NOT_FOUND"
	CodeInterfaceNotUp          = "NETWORK_INTERFACE_NOT_UP"
	CodeLANIPMissing            = "NETWORK_LAN_IP_MISSING"
	CodeLANIPConflict           = "NETWORK_LAN_IP_CONFLICT"
	CodeSubnetInvalid           = "NETWORK_SUBNET_INVALID"
	CodeUpstreamUnreachable     = "NETWORK_UPSTREAM_UNREACHABLE"
	CodeUpstreamConflict        = "NETWORK_UPSTREAM_CONFLICT"
	CodeForwardingUnavailable   = "NETWORK_FORWARDING_UNAVAILABLE"
	CodeTUNUnavailable          = "NETWORK_TUN_UNAVAILABLE"
	CodeTUNTimeout              = "NETWORK_TUN_TIMEOUT"
	CodeNFTablesUnavailable     = "NETWORK_NFTABLES_UNAVAILABLE"
	CodeNFTablesForeignTable    = "NETWORK_NFTABLES_FOREIGN_TABLE"
	CodePolicyRoutingConflict   = "NETWORK_POLICY_ROUTING_CONFLICT"
	CodeIPRoute2Unavailable     = "NETWORK_IPROUTE2_UNAVAILABLE"
	CodeCapabilityMissing       = "NETWORK_CAPABILITY_MISSING"
	CodeNetworkModeUnsupported  = "NETWORK_MODE_UNSUPPORTED"
	CodeSnapshotInvalid         = "NETWORK_SNAPSHOT_INVALID"
	CodeSnapshotBackendMismatch = "NETWORK_SNAPSHOT_BACKEND_MISMATCH"
	CodeCommandFailed           = "NETWORK_COMMAND_FAILED"
	CodePermissionDenied        = "NETWORK_PERMISSION_DENIED"
	CodeInvalidArgument         = "NETWORK_INVALID_ARGUMENT"
)

// Error is a network failure with a machine-readable code and structured
// details. Wrapping with %w is preserved so callers can still use errors.Is.
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
	Err     error             `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause.
func (e *Error) Unwrap() error { return e.Err }

// Is allows errors.Is(err, target) to match by code.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	if !ok {
		return false
	}
	return other.Code == e.Code
}

// NewError builds a network error.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap attaches a cause.
func (e *Error) Wrap(err error) *Error {
	e.Err = err
	return e
}

// WithDetail adds one structured detail field and returns the receiver.
func (e *Error) WithDetail(key, value string) *Error {
	if e.Details == nil {
		e.Details = map[string]string{}
	}
	e.Details[key] = value
	return e
}

// WithDetails adds several structured detail fields.
func (e *Error) WithDetails(details map[string]string) *Error {
	if len(details) == 0 {
		return e
	}
	if e.Details == nil {
		e.Details = map[string]string{}
	}
	for k, v := range details {
		e.Details[k] = v
	}
	return e
}

// CodeOf extracts a machine-readable code from any error, falling back to
// CodeCommandFailed. The control API should never leak a bare error string.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var netErr *Error
	if errors.As(err, &netErr) {
		return netErr.Code
	}
	return CodeCommandFailed
}
