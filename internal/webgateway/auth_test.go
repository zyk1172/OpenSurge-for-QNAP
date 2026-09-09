package webgateway

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAuthStoreSetupAndVerify(t *testing.T) {
	store := NewAuthStore(filepath.Join(t.TempDir(), "auth"))
	required, err := store.SetupRequired()
	if err != nil {
		t.Fatal(err)
	}
	if !required {
		t.Fatal("SetupRequired() = false before administrator creation")
	}
	if err := store.Setup("admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify("admin", "correct horse battery staple"); err != nil {
		t.Fatalf("Verify(valid) = %v", err)
	}
	if err := store.Verify("other", "correct horse battery staple"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("Verify(wrong user) = %v, want ErrInvalidLogin", err)
	}
	if err := store.Verify("admin", "wrong password"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("Verify(wrong password) = %v, want ErrInvalidLogin", err)
	}
	required, err = store.SetupRequired()
	if err != nil {
		t.Fatal(err)
	}
	if required {
		t.Fatal("SetupRequired() = true after administrator creation")
	}
}

func TestAuthStorePersistsAndRejectsSecondSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth")
	first := NewAuthStore(dir)
	if err := first.Setup("operator", "this password is long enough"); err != nil {
		t.Fatal(err)
	}
	second := NewAuthStore(dir)
	if err := second.Verify("operator", "this password is long enough"); err != nil {
		t.Fatalf("Verify after reopen = %v", err)
	}
	if err := second.Setup("replacement", "another long enough password"); !errors.Is(err, ErrAlreadySetup) {
		t.Fatalf("second Setup() = %v, want ErrAlreadySetup", err)
	}
}

func TestAuthStoreRejectsWeakSetup(t *testing.T) {
	store := NewAuthStore(filepath.Join(t.TempDir(), "auth"))
	if err := store.Setup("ab", "correct horse battery staple"); err == nil {
		t.Fatal("Setup accepted short username")
	}
	if err := store.Setup("admin", "short"); err == nil {
		t.Fatal("Setup accepted short password")
	}
}
