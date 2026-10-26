package access

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func readAudits(t *testing.T, store storage.Store) []storage.AuditRecord {
	t.Helper()
	var audits []storage.AuditRecord
	if err := store.View(context.Background(), func(tx storage.Tx) error {
		rows, err := tx.Scan(storage.AuditBucket, storage.Scan{Limit: 500})
		if err != nil {
			return err
		}
		for _, row := range rows {
			var record storage.AuditRecord
			if err := json.Unmarshal(row.Value, &record); err != nil {
				return err
			}
			audits = append(audits, record)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return audits
}

func TestDeviceAuthorizationLifecycleHasAtomicRedactedAudit(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	pair, err := svc.CreatePairing(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	device, err := svc.Pair(ctx, strings.ToLower(pair.Code), "客厅 Mac")
	if err != nil {
		t.Fatal(err)
	}
	devices, err := svc.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].IssuedBy != "admin" {
		t.Fatalf("issuer not retained: %#v %v", devices, err)
	}
	if err := svc.Revoke(ctx, device.DeviceID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(ctx, device.DeviceID); err != nil {
		t.Fatal(err)
	}
	audits := readAudits(t, store)
	if len(audits) != 3 {
		t.Fatalf("want issue/pair/revoke once each, got %#v", audits)
	}
	for i, action := range []string{"device.pairing_issued", "device.paired", "device.revoked"} {
		if audits[i].Action != action || audits[i].ActorID != "admin" || audits[i].At.IsZero() {
			t.Fatalf("invalid audit %d: %#v", i, audits[i])
		}
		if i > 0 && audits[i].TargetID != device.DeviceID {
			t.Fatalf("audit did not identify authorized/revoked device: %#v", audits[i])
		}
	}
	raw := string(mustJSON(t, audits))
	for _, secret := range []string{pair.Code, device.Token, digestToken(pair.Code), digestToken(device.Token), "客厅 Mac"} {
		if strings.Contains(raw, secret) {
			t.Fatal("audit contains a secret or untrusted device display name")
		}
	}
}

func TestPairingCodeRetainsAll64RandomBits(t *testing.T) {
	// Each source bit must survive encoding. The old uppercased base64url
	// encoding was not reversible and could collapse distinct random inputs.
	seen := map[string]bool{}
	for bit := -1; bit < 64; bit++ {
		raw := make([]byte, 8)
		if bit >= 0 {
			raw[bit/8] = 1 << (bit % 8)
		}
		svc, _ := newTestService(t)
		svc.random = bytes.NewReader(raw)
		pair, err := svc.CreatePairing(context.Background(), "admin")
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(pair.Code)
		if err != nil || len(pair.Code) != 13 || !bytes.Equal(decoded, raw) || seen[pair.Code] {
			t.Fatalf("random source bit %d lost: code=%q decoded=%x err=%v", bit, pair.Code, decoded, err)
		}
		seen[pair.Code] = true
	}
}

func TestDeviceNamesRejectControlsAndBidiBeforeConsumingCode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	pair, err := svc.CreatePairing(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	invalid := []string{"", "  ", strings.Repeat("a", 81), "line\nbreak", "\rMac", "Mac\t", "Mac\x00", "Mac\x7f", "Mac\u0085", "Mac\u202eexe", "Mac\u202a", "Mac\u2066", "Mac\u2069", "Mac\u200e", "Mac\u061c", "Mac\u2028", string([]byte{'M', 0xff})}
	for i, name := range invalid {
		if _, err := svc.Pair(ctx, pair.Code, name); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid name %d accepted: %v", i, err)
		}
	}
	if _, err := svc.Pair(ctx, pair.Code, "  客厅 Mac  "); err != nil {
		t.Fatalf("validation consumed code or rejected ordinary Chinese name: %v", err)
	}
	devices, err := svc.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Name != "客厅 Mac" {
		t.Fatalf("name trim changed: %#v %v", devices, err)
	}
}

func TestLegacyPairingAndDeviceIssuerIsExplicitlyUnknown(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t)
	code := "ABCDEFG_HI-"
	if err := store.Update(ctx, func(tx storage.Tx) error {
		return tx.Put(pairingBucket, digestToken(code), pairing{Digest: digestToken(code), ExpiresAt: time.Now().Add(time.Minute)})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pair(ctx, code, "legacy"); err != nil {
		t.Fatalf("legacy code no longer works: %v", err)
	}
	if err := store.Update(ctx, func(tx storage.Tx) error {
		return tx.Put(deviceBucket, "legacy-device", deviceRecord{Device: Device{ID: "old", Name: "old"}, ExpiresAt: time.Now().Add(time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	devices, err := svc.Devices(ctx)
	if err != nil || len(devices) != 2 {
		t.Fatalf("legacy device listing failed: %#v %v", devices, err)
	}
	for _, device := range devices {
		if device.IssuedBy != legacyIssuer {
			t.Fatalf("legacy issuer fabricated: %#v", device)
		}
	}
	audits := readAudits(t, store)
	if len(audits) != 1 || audits[0].ActorID != legacyIssuer {
		t.Fatalf("legacy pairing audit attributed to known admin: %#v", audits)
	}
}

type auditFaultStore struct {
	storage.Store
	err error
}

func (s auditFaultStore) Update(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.Update(ctx, func(tx storage.Tx) error { return fn(auditFaultTx{Tx: tx, err: s.err}) })
}

type auditFaultTx struct {
	storage.Tx
	err error
}

func (tx auditFaultTx) Put(bucket, key string, value any) error {
	if bucket == storage.AuditBucket {
		return tx.err
	}
	return tx.Tx.Put(bucket, key, value)
}

func TestPairingAuditFailureRollsBackAllAuthorizationChanges(t *testing.T) {
	ctx := context.Background()
	fault := errors.New("audit write failed")
	for _, action := range []string{"issue", "pair", "revoke"} {
		t.Run(action, func(t *testing.T) {
			svc, store := newTestService(t)
			var pair Pairing
			var device DeviceToken
			var err error
			if action != "issue" {
				pair, err = svc.CreatePairing(ctx, "admin")
				if err != nil {
					t.Fatal(err)
				}
			}
			if action == "revoke" {
				device, err = svc.Pair(ctx, pair.Code, "device")
				if err != nil {
					t.Fatal(err)
				}
			}
			before := len(readAudits(t, store))
			svc.store = auditFaultStore{Store: store, err: fault}
			switch action {
			case "issue":
				_, err = svc.CreatePairing(ctx, "admin")
			case "pair":
				_, err = svc.Pair(ctx, pair.Code, "device")
			case "revoke":
				err = svc.Revoke(ctx, device.DeviceID)
			}
			if !errors.Is(err, fault) {
				t.Fatalf("audit failure hidden: %v", err)
			}
			svc.store = store
			if len(readAudits(t, store)) != before {
				t.Fatal("failed audit appended a record")
			}
			if err := store.View(ctx, func(tx storage.Tx) error {
				rows, err := tx.Scan(pairingBucket, storage.Scan{Limit: 500})
				if err != nil {
					return err
				}
				want := 0
				if action == "pair" {
					want = 1
				}
				if len(rows) != want {
					return fmt.Errorf("pairing mutation survived rollback: got %d want %d", len(rows), want)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			devices, err := svc.Devices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if action == "revoke" {
				if len(devices) != 1 || devices[0].RevokedAt != nil {
					t.Fatal("failed audit committed revocation")
				}
				if _, err := svc.Authenticate(ctx, device.Token); err != nil {
					t.Fatalf("failed revocation invalidated device: %v", err)
				}
			} else if len(devices) != 0 {
				t.Fatal("failed audit created a device")
			}
			if action == "pair" {
				if _, err := svc.Pair(ctx, pair.Code, "device"); err != nil {
					t.Fatalf("audit rollback consumed code: %v", err)
				}
			}
		})
	}
}
