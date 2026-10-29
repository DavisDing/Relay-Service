package monitor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"relayservice/internal/storage"
)

func TestModelsWorkspaceKeepsInstancesDistinctAndPagesCompleteCatalog(t *testing.T) {
	e, _ := testEngine(t)
	ctx := context.Background()
	for _, kind := range []string{KindLiteLLM, KindWorkbuddy2API} {
		s, err := e.Save(ctx, "admin", ServiceInput{Name: kind, Kind: kind, BaseURL: "http://127.0.0.1:4000", Enabled: true, CollectModels: true})
		if err != nil {
			t.Fatal(err)
		}
		g := &GatewayDetails{CatalogState: "available", DeploymentsState: "available", LogsState: "unauthorized", ModelCount: 62}
		for i := 0; i < 62; i++ {
			g.Models = append(g.Models, GatewayModel{ID: fmt.Sprintf("model-%02d", i)})
		}
		c := Check{ServiceID: s.ID, Generation: s.Generation, Success: true, Status: StatusHealthy, CompletedAt: e.now(), Details: &Details{Gateway: g}}
		if err := e.store.Update(ctx, func(tx storage.Tx) error { return tx.Put(stateBucket, s.ID, state{Current: &c}) }); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]bool{}
	cursor := ""
	for {
		v, err := e.ModelsWorkspace(ctx, ModelsWorkspaceQuery{Section: "models", Limit: 7, Cursor: cursor})
		if err != nil || v.Total != 124 || v.Summary.Models != 124 {
			t.Fatalf("cross-instance catalog was merged: %+v %v", v.Summary, err)
		}
		for _, m := range v.Models {
			if ids[m.ID] || m.InferenceState != "not_tested" {
				t.Fatalf("duplicate identity or fabricated inference: %+v", m)
			}
			ids[m.ID] = true
		}
		if !v.HasMore {
			break
		}
		if v.NextCursor == cursor {
			t.Fatal("pagination did not advance")
		}
		cursor = v.NextCursor
	}
	if len(ids) != 124 {
		t.Fatalf("incomplete pagination: %d", len(ids))
	}
	for _, q := range []ModelsWorkspaceQuery{{Section: "bad"}, {Limit: 201}, {Cursor: "-1"}} {
		if _, err := e.ModelsWorkspace(ctx, q); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid query accepted: %+v %v", q, err)
		}
	}
}

func TestModelsWorkspaceRejectsOldGenerationAndFutureEvidence(t *testing.T) {
	e, _ := testEngine(t)
	ctx := context.Background()
	s, err := e.Save(ctx, "admin", ServiceInput{Name: "models", Kind: KindLiteLLM, BaseURL: "http://127.0.0.1:4000", Enabled: true, CollectModels: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Check{
		{Generation: s.Generation - 1, CompletedAt: e.now()},
		{Generation: s.Generation, CompletedAt: e.now().Add(time.Hour)},
	} {
		c.Details = &Details{Gateway: &GatewayDetails{Models: []GatewayModel{{ID: "old"}}, ModelCount: 1, CatalogState: "available"}}
		if err := e.store.Update(ctx, func(tx storage.Tx) error { return tx.Put(stateBucket, s.ID, state{Current: &c}) }); err != nil {
			t.Fatal(err)
		}
		v, err := e.ModelsWorkspace(ctx, ModelsWorkspaceQuery{Section: "models"})
		if err != nil || v.Total != 0 || len(v.Sources) != 1 || v.Sources[0].Freshness != "never_observed" {
			t.Fatalf("invalid evidence surfaced: %+v %v", v, err)
		}
	}
}
