package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAuditAppendContinuesExistingSequenceAndShape(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	at := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if err := store.Update(ctx, func(tx Tx) error {
		n, err := tx.Next("monitor.audit")
		if err != nil {
			return err
		}
		if err := tx.Put("monitor.audit", fmt.Sprintf("%020d", n), map[string]any{"actor_id": "admin", "target_id": "service", "action": "service.save", "at": at}); err != nil {
			return err
		}
		return AppendAudit(tx, AuditRecord{ActorID: "admin", TargetID: "device", Action: "device.paired", At: at})
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.View(ctx, func(tx Tx) error {
		rows, err := tx.Scan(AuditBucket, Scan{Limit: 10})
		if err != nil {
			return err
		}
		if len(rows) != 2 || rows[0].Key != "00000000000000000001" || rows[1].Key != "00000000000000000002" {
			t.Fatalf("existing sequence not preserved: %#v", rows)
		}
		for _, row := range rows {
			var record AuditRecord
			if err := tx.Get(AuditBucket, row.Key, &record); err != nil {
				return err
			}
			if record.ActorID != "admin" || record.TargetID == "" || record.Action == "" || !record.At.Equal(at) {
				t.Fatalf("audit fields changed: %#v", record)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
