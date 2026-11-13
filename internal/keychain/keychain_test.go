package keychain

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// The round trip talks to the real login Keychain, which may be locked or
// unavailable in CI, so it only runs when explicitly requested.
func TestRoundTrip(t *testing.T) {
	if os.Getenv("MOUNTKEEPER_KEYCHAIN_TEST") == "" {
		t.Skip("set MOUNTKEEPER_KEYCHAIN_TEST=1 to test against the login Keychain")
	}
	account := fmt.Sprintf("mountkeeper-selftest-%d", os.Getpid())
	t.Cleanup(func() { Delete(account) })

	if err := Available(); err != nil {
		t.Fatalf("Available: %v", err)
	}
	if Has(account) {
		t.Fatal("test item already exists")
	}
	if _, err := Get(account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on missing item: %v", err)
	}
	if err := Set(account, "self test", "s3cr3t pässword"); err != nil {
		t.Fatal(err)
	}
	if !Has(account) {
		t.Fatal("Has = false after Set")
	}
	if got, err := Get(account); err != nil || got != "s3cr3t pässword" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := Set(account, "self test", "changed"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(account); got != "changed" {
		t.Fatalf("after update Get = %q", got)
	}
	if err := Delete(account); err != nil {
		t.Fatal(err)
	}
	if err := Delete(account); err != nil {
		t.Fatalf("deleting a missing item: %v", err)
	}
	if Has(account) {
		t.Fatal("Has = true after Delete")
	}
}
