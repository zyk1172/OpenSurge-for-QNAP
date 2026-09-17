package dnspolicy

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultDocumentPreservesProfileOwnership(t *testing.T) {
	document := DefaultDocument()
	if document.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", document.SchemaVersion, SchemaVersion)
	}
	if document.Mode != ModeInheritProfile {
		t.Fatalf("mode = %q, want %q", document.Mode, ModeInheritProfile)
	}
	effective, err := Resolve(document)
	if err != nil {
		t.Fatal(err)
	}
	if effective.ResolverOwner != ResolverOwnerProfile {
		t.Fatalf("resolver owner = %q, want %q", effective.ResolverOwner, ResolverOwnerProfile)
	}
	if effective.Managed != nil {
		t.Fatal("inherit_profile must not expose managed policy as effective")
	}
}

func TestManagedModeOwnsResolverPolicy(t *testing.T) {
	document := DefaultDocument()
	document.Mode = ModeManaged
	document.Managed.Nameservers = []string{" https://dns.example/dns-query "}
	document.Managed.NameserverPolicy = []NameserverPolicyRule{{
		Match:       " +.example.com ",
		Nameservers: []string{" 1.1.1.1 "},
	}}
	effective, err := Resolve(document)
	if err != nil {
		t.Fatal(err)
	}
	if effective.ResolverOwner != ResolverOwnerOpenSurge {
		t.Fatalf("resolver owner = %q, want %q", effective.ResolverOwner, ResolverOwnerOpenSurge)
	}
	if effective.Managed == nil {
		t.Fatal("managed mode must expose an effective managed policy")
	}
	if got := effective.Managed.Nameservers[0]; got != "https://dns.example/dns-query" {
		t.Fatalf("normalized nameserver = %q", got)
	}
	if got := effective.Managed.NameserverPolicy[0].Match; got != "+.example.com" {
		t.Fatalf("normalized matcher = %q", got)
	}
}

func TestManagedDraftMayRemainInactive(t *testing.T) {
	document := DefaultDocument()
	document.Managed.NameserverPolicy = []NameserverPolicyRule{{
		Match:       "+.example.com",
		Nameservers: []string{"https://dns.example/dns-query"},
	}}
	if _, err := Normalize(document); err != nil {
		t.Fatalf("inactive managed draft should validate: %v", err)
	}
}

func TestManagedModeRequiresNameserver(t *testing.T) {
	document := DefaultDocument()
	document.Mode = ModeManaged
	_, err := Normalize(document)
	if err == nil || !strings.Contains(err.Error(), "at least one nameserver") {
		t.Fatalf("error = %v, want missing nameserver", err)
	}
}

func TestValidateRejectsDuplicatePolicyMatcher(t *testing.T) {
	document := DefaultDocument()
	document.Managed.NameserverPolicy = []NameserverPolicyRule{
		{Match: "+.example.com", Nameservers: []string{"1.1.1.1"}},
		{Match: "+.example.com", Nameservers: []string{"8.8.8.8"}},
	}
	_, err := Normalize(document)
	if err == nil || !strings.Contains(err.Error(), "duplicate matcher") {
		t.Fatalf("error = %v, want duplicate matcher", err)
	}
}

func TestValidateRejectsMalformedFallbackCIDR(t *testing.T) {
	document := DefaultDocument()
	document.Managed.FallbackFilter.IPCIDR = []string{"not-a-cidr"}
	_, err := Normalize(document)
	if err == nil || !strings.Contains(err.Error(), "valid CIDR") {
		t.Fatalf("error = %v, want invalid CIDR", err)
	}
}

func TestValidateRejectsDuplicateResolver(t *testing.T) {
	document := DefaultDocument()
	document.Managed.Nameservers = []string{"1.1.1.1", " 1.1.1.1 "}
	_, err := Normalize(document)
	if err == nil || !strings.Contains(err.Error(), "duplicate value") {
		t.Fatalf("error = %v, want duplicate resolver", err)
	}
}

func TestValidateDirectFollowRequiresDirectResolver(t *testing.T) {
	document := DefaultDocument()
	document.Managed.DirectNameserverFollowPolicy = true
	_, err := Normalize(document)
	if err == nil || !strings.Contains(err.Error(), "direct_nameserver_follow_policy") {
		t.Fatalf("error = %v, want direct resolver dependency", err)
	}
}

func TestRevisionUsesNormalizedDocument(t *testing.T) {
	left := DefaultDocument()
	left.Managed.Nameservers = []string{"1.1.1.1"}
	right := DefaultDocument()
	right.Managed.Nameservers = []string{" 1.1.1.1 "}
	leftRevision, err := Revision(left)
	if err != nil {
		t.Fatal(err)
	}
	rightRevision, err := Revision(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftRevision != rightRevision {
		t.Fatalf("normalized revisions differ: %s != %s", leftRevision, rightRevision)
	}
	right.Managed.Nameservers = []string{"8.8.8.8"}
	changedRevision, err := Revision(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedRevision == leftRevision {
		t.Fatal("semantic DNS policy change must change revision")
	}
}

func TestRevisionConflictSentinelIsMatchable(t *testing.T) {
	wrapped := errors.Join(errors.New("save failed"), ErrRevisionConflict)
	if !errors.Is(wrapped, ErrRevisionConflict) {
		t.Fatal("ErrRevisionConflict must support errors.Is")
	}
}
