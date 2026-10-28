package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func gatewayNow() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }

func gatewayLogBody(rows []map[string]any, total, page int) []byte {
	b, _ := json.Marshal(map[string]any{"data": rows, "total": total, "page": page, "page_size": 100, "total_pages": (total + 99) / 100})
	return b
}

func gatewayRow(id, model, deployment string, status any) map[string]any {
	return map[string]any{"request_id": id, "model_group": model, "model": "openai/vendor-model", "model_id": deployment, "status": status, "startTime": "2026-10-28T11:58:00Z", "endTime": "2026-10-28T11:58:01Z"}
}

func TestGatewayIndependentSourcesAndSafeProjection(t *testing.T) {
	var paths []string
	rows := []map[string]any{gatewayRow("a", "shared", "deploy-a", "success"), gatewayRow("b", "shared", "deploy-b", "failure"), gatewayRow("c", "shared", "deploy-a", nil), gatewayRow("d", "shared", "deploy-a", "success")}
	rows[3]["cache_hit"] = "True"
	for _, row := range rows {
		row["api_key"], row["user"], row["metadata"] = "SECRET-KEY", "PRIVATE-USER", map[string]any{"messages": "PRIVATE-PROMPT", "response": "PRIVATE-ANSWER"}
	}
	fetch := func(_ context.Context, path string) ([]byte, error) {
		paths = append(paths, path)
		switch {
		case path == "/v1/models":
			return []byte(`{"data":[{"id":"shared","owned_by":"SECRET-OWNER"}]}`), nil
		case path == "/model/info":
			return []byte(`{"data":[{"model_name":"shared","litellm_params":{"model":"openai/a","api_key":"SECRET-KEY","api_base":"https://channel-a.example/v1?token=SECRET-URL"},"model_info":{"id":"deploy-a"}},{"model_name":"shared","litellm_params":{"model":"openai/a","api_base":"https://channel-b.example/v1"},"model_info":{"id":"deploy-b"}}]}`), nil
		case strings.HasPrefix(path, "/spend/logs/v2?"):
			u, _ := url.Parse(path)
			if u.Query().Get("exclude_internal_health_checks") != "true" || u.Query().Get("start_date") != "2026-10-27 12:00:00" || u.Query().Get("end_date") != "2026-10-28 12:00:00" || u.Query().Get("sort_order") != "desc" {
				t.Fatalf("unsafe/unbounded log query: %s", path)
			}
			return gatewayLogBody(rows, len(rows), 1), nil
		default:
			t.Fatalf("unexpected endpoint %s", path)
			return nil, nil
		}
	}
	d := CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Models: true, Logs: true}, fetch, gatewayNow())
	if len(paths) != 3 || d.CatalogState != "available" || d.DeploymentsState != "available" || d.LogsState != "available" || d.Sampled || d.Rows != 4 {
		t.Fatalf("unexpected source summary: %+v", d)
	}
	if len(d.Traffic) != 2 || d.Traffic[0].Success != 1 || d.Traffic[0].Unknown != 1 || d.Traffic[0].CacheHits != 1 || d.Traffic[1].Failure != 1 || d.Traffic[0].Channel == d.Traffic[1].Channel {
		t.Fatalf("deployment or evidence merged: %+v", d.Traffic)
	}
	b, _ := json.Marshal(d)
	for _, secret := range []string{"SECRET", "PRIVATE", "channel-a.example", "request_id", "api_base", "messages"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("projection leaked %s: %s", secret, b)
		}
	}
	if d.LatestAt == nil || d.LatestAt.Format(time.RFC3339) != "2026-10-28T11:58:01Z" {
		t.Fatalf("missing evidence timestamp: %+v", d)
	}
}

func TestGatewayOptionsAndWB2APICapabilities(t *testing.T) {
	called := 0
	fetch := func(_ context.Context, path string) ([]byte, error) {
		called++
		if path != "/v1/models" {
			t.Fatalf("wb2api speculative endpoint %s", path)
		}
		return []byte(`{"data":[{"id":"wb-auto"},{"id":"wb-deep"}]}`), nil
	}
	d := CollectGateway(context.Background(), KindWorkbuddy2API, GatewayOptions{}, fetch, gatewayNow())
	if called != 0 || d.CatalogState != "disabled" || d.LogsState != "disabled" {
		t.Fatal("disabled options still fetched")
	}
	d = CollectGateway(context.Background(), KindWorkbuddy2API, GatewayOptions{Models: true, Logs: true}, fetch, gatewayNow())
	if called != 1 || d.ModelCount != 2 || d.LogsState != "unsupported" || d.DeploymentsState != "unsupported" {
		t.Fatalf("unexpected wb2api capabilities: %+v", d)
	}
}

func TestGatewayMissingPermissionsDoesNotHideCatalog(t *testing.T) {
	fetch := func(_ context.Context, path string) ([]byte, error) {
		if path == "/v1/models" {
			return []byte(`{"data":[]}`), nil
		}
		return nil, &FetchError{Status: StatusUnauthorized, Class: "authentication_failed", Message: "SECRET"}
	}
	d := CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Models: true, Logs: true}, fetch, gatewayNow())
	if d.CatalogState != "available" || d.DeploymentsState != "unauthorized" || d.LogsState != "unauthorized" || len(d.Issues) != 2 {
		t.Fatalf("permissions collapsed independent sources: %+v", d)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("raw error message leaked")
	}
}

func TestGatewayLogPagesBoundedDeduplicatedAndPartial(t *testing.T) {
	first := make([]map[string]any, 100)
	for i := range first {
		first[i] = gatewayRow(fmt.Sprint(i), "alias", "deployment", "success")
	}
	calls := 0
	fetch := func(_ context.Context, path string) ([]byte, error) {
		calls++
		u, _ := url.Parse(path)
		if u.Query().Get("page") == "1" {
			return gatewayLogBody(first, 350, 1), nil
		}
		second := []map[string]any{gatewayRow("99", "alias", "deployment", "success"), gatewayRow("new", "alias", "deployment", "failure")}
		return gatewayLogBody(second, 350, 2), nil
	}
	d := CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Logs: true}, fetch, gatewayNow())
	if calls != 2 || d.Rows != 101 || !d.Sampled || d.Traffic[0].Success != 100 || d.Traffic[0].Failure != 1 {
		t.Fatalf("bad pagination/dedup: %+v calls=%d", d, calls)
	}
	calls = 0
	d = CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Logs: true}, func(ctx context.Context, path string) ([]byte, error) {
		calls++
		if calls == 1 {
			return gatewayLogBody(first, 350, 1), nil
		}
		return nil, errors.New("SECRET upstream url")
	}, gatewayNow())
	if d.Rows != 100 || d.LogsState != "partial" || !d.Sampled || len(d.Issues) != 1 {
		t.Fatalf("partial prefix lost: %+v", d)
	}
}

func TestGatewayInvalidRowsCannotProveAvailability(t *testing.T) {
	rows := []map[string]any{gatewayRow("valid-unknown", "alias", "", "unexpected"), gatewayRow("outside", "alias", "dep", "success"), gatewayRow("future", "alias", "dep", "success"), gatewayRow("missing-id", "alias", "dep", "success")}
	rows[1]["startTime"] = "2026-10-26T00:00:00Z"
	rows[2]["endTime"] = "2026-10-29T00:00:00Z"
	delete(rows[3], "request_id")
	d := CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Logs: true}, func(context.Context, string) ([]byte, error) { return gatewayLogBody(rows, 4, 1), nil }, gatewayNow())
	if d.Rows != 1 || d.Skipped != 3 || !d.Sampled || len(d.Traffic) != 1 || d.Traffic[0].Unknown != 1 || d.Traffic[0].Success != 0 || d.Traffic[0].Channel != "" {
		t.Fatalf("invalid evidence accepted: %+v", d)
	}
}

func TestGatewayInvalidContractAndSize(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[],"total":0,"page":2,"page_size":100,"total_pages":0}`, strings.Repeat(" ", gatewayLimit+1)} {
		d := CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Logs: true}, func(context.Context, string) ([]byte, error) { return []byte(body), nil }, gatewayNow())
		if d.LogsState != "invalid" || d.Rows != 0 {
			t.Fatalf("invalid log contract accepted: %+v", d)
		}
	}
	for _, body := range []string{`{"data":[{"id":"same"},{"id":"same"}]}`, `{"data":[{"id":"https://secret.example/?key=SECRET"}]}`} {
		if _, _, err := parseGatewayCatalog([]byte(body)); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}

func TestGatewayTruncationAndNoAliasDeploymentGuess(t *testing.T) {
	models := make([]map[string]any, 201)
	for i := range models {
		models[i] = map[string]any{"id": fmt.Sprint(i)}
	}
	b, _ := json.Marshal(map[string]any{"data": models})
	d := CollectGateway(context.Background(), KindLiteLLM, GatewayOptions{Models: true, Logs: true}, func(_ context.Context, path string) ([]byte, error) {
		switch path {
		case "/v1/models":
			return b, nil
		case "/model/info":
			return []byte(`{"data":[{"model_name":"alias","litellm_params":{"model":"openai/a","api_base":"https://a.example"},"model_info":{"id":"known"}}]}`), nil
		default:
			return gatewayLogBody([]map[string]any{gatewayRow("a", "alias", "unknown", "success")}, 1, 1), nil
		}
	}, gatewayNow())
	if len(d.Models) != 200 || d.ModelCount != 201 || !d.Truncated || d.Traffic[0].Channel != "" {
		t.Fatalf("truncation/alias attribution incorrect: %+v", d)
	}
}
