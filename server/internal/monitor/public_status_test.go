package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func publicTestNow() time.Time { return time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC) }
func TestPipioPublicStatus(t *testing.T) {
	body := `{"success":true,"data":[{"categoryName":"模型可用性","monitors":[{"name":"model-a","status":1,"group":"Example Models","uptime":0.95,"heartbeats":[0,1,2,3,8],"secret":"never-copy","url":"https://private.example"},{"name":"model-b","status":0},{"name":"model-c","status":2},{"name":"model-d","status":3},{"name":"model-e","status":9}]}]}`
	p, err := ParsePublicStatus("pipio", []byte(body), "application/json", nil, publicTestNow())
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 5 || !p.Unhealthy || !p.Unknown || p.Source != "/api/uptime/status" || p.UpdatedAt != nil || p.ObservedAt != publicTestNow() {
		t.Fatalf("unexpected projection: %+v", p)
	}
	want := []string{"healthy", "unhealthy", "unknown", "unhealthy", "unknown"}
	for i, s := range want {
		if p.Components[i].Status != s {
			t.Fatalf("component %d = %+v", i, p.Components[i])
		}
	}
	if p.Components[0].Uptime == nil || *p.Components[0].Uptime != 0.95 || strings.Join(p.Components[0].Heartbeats, ",") != "unhealthy,healthy,unknown,unhealthy,unknown" {
		t.Fatalf("lost uptime/heartbeat: %+v", p.Components[0])
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "private.example") {
		t.Fatal("private fields leaked")
	}
	auto, err := ParsePublicStatus("auto", []byte(body), "application/json", nil, publicTestNow())
	if err != nil || auto.Source != "/api/uptime/status" {
		t.Fatalf("auto Pipio: %+v %v", auto, err)
	}
}
func TestPipioPublicStatusMalformed(t *testing.T) {
	for _, body := range []string{`{"success":false,"data":[]}`, `{"data":[]}`, `{"success":true,"data":[{"categoryName":"x"}]}`, `{"success":true,"data":[{"categoryName":"x","monitors":[{"status":1}]}]}`, `{"success":true,"data":[{"categoryName":"x","monitors":[{"name":"\n","status":1}]}]}`} {
		if _, err := ParsePublicStatus("pipio", []byte(body), "application/json", nil, publicTestNow()); !errors.Is(err, ErrPublicStatusUnsupported) {
			t.Errorf("accepted %s: %v", body, err)
		}
	}
	p, err := ParsePublicStatus("pipio", []byte(`{"success":true,"data":[{"categoryName":"x","monitors":[{"name":"a","status":"1","uptime":10}]}]}`), "application/json", nil, publicTestNow())
	if err != nil || !p.Unknown || p.Components[0].Uptime != nil {
		t.Fatalf("type confusion: %+v %v", p, err)
	}
}
func TestPublicStatusStandardAndConfiguredJSON(t *testing.T) {
	p, err := ParsePublicStatus("statuspage", []byte(`{"page":{"updated_at":"2026-10-28T07:59:00Z"},"components":[{"name":"group","group":true,"status":"operational"},{"name":"API","status":"operational"},{"name":"Chat","status":"partial_outage","updated_at":"2026-10-28T07:58:00Z"}]}`), "application/json", nil, publicTestNow())
	if err != nil || p.Count != 2 || !p.Unhealthy || p.Unknown || p.UpdatedAt == nil || p.Components[1].UpdatedAt == nil {
		t.Fatalf("statuspage: %+v %v", p, err)
	}
	cfg := &PublicStatusConfig{ResourcePath: "/health/custom.json", ItemsPath: "report.services", NameField: "service.label", StatusField: "state.code", HealthyValues: []string{"AVAILABLE"}, UnhealthyValues: []string{"broken"}}
	p, err = ParsePublicStatus("json", []byte(`{"report":{"services":[{"service":{"label":"one"},"state":{"code":"available"}},{"service":{"label":"two"},"state":{"code":"broken"}},{"service":{"label":"three"},"state":{"code":"a-secret-error"}}]}}`), "application/json; charset=utf-8", cfg, publicTestNow())
	if err != nil || p.Count != 3 || !p.Unknown || !p.Unhealthy || p.Source != "/health/custom.json" {
		t.Fatalf("configured JSON: %+v %v", p, err)
	}
	out, _ := json.Marshal(p)
	if strings.Contains(string(out), "a-secret-error") {
		t.Fatal("unknown remote status echoed")
	}
}
func publicHTMLConfig() *PublicStatusConfig {
	return &PublicStatusConfig{Format: "html", ResourcePath: "/status", HTMLItemTag: "div", HTMLItemAttribute: "class", HTMLItemValue: "service", HTMLNameAttribute: "data-name", HTMLStatusAttribute: "data-state", HealthyValues: []string{"operational"}, UnhealthyValues: []string{"outage"}}
}
func TestPublicStatusHTMLExplicitAttributes(t *testing.T) {
	body := `<!doctype html><html><head><script>if (1 < 2) { document.write('<div class="service" data-name="fake" data-state="outage"></div>') }</script></head><body><!-- <div class="service" data-name="comment" data-state="outage"></div> --><nav><div class="service" data-name="nav" data-state="operational"></div></nav><div hidden><div class="service" data-name="hidden" data-state="outage"></div></div><div class="service" data-name="A &amp; B" data-state="operational">Anything<script>fake</script></div><div class="service" data-name="C" data-state="outage"></div><div class="service" data-name="D" data-state="maybe"></div></body></html>`
	p, err := ParsePublicStatus("html", []byte(body), "text/html", publicHTMLConfig(), publicTestNow())
	if err != nil || p.Count != 3 || !p.Unhealthy || !p.Unknown || p.Components[0].Name != "A & B" {
		t.Fatalf("HTML: %+v %v", p, err)
	}
}
func TestPublicStatusHTMLVisibleText(t *testing.T) {
	cfg := publicHTMLConfig()
	cfg.HTMLStatusAttribute = ""
	p, err := ParsePublicStatus("html", []byte(`<div class='service' data-name='API'><span> Operational </span><script>outage</script><style>.outage { color:red }</style></div><div class='service' data-name='Models'>outage</div>`), "text/html", cfg, publicTestNow())
	if err != nil || p.Count != 2 || !p.Unhealthy || p.Unknown || p.Components[0].Status != "healthy" {
		t.Fatalf("visible HTML: %+v %v", p, err)
	}
	for _, body := range []string{`<div class='service' data-name='API'>not operational</div>`, `<div class='service' data-name='API'>all operational except one outage</div>`} {
		p, err := ParsePublicStatus("html", []byte(body), "text/html", cfg, publicTestNow())
		if err != nil || !p.Unknown {
			t.Fatalf("substring matched: %+v %v", p, err)
		}
	}
}
func TestPublicStatusHTMLFailsClosed(t *testing.T) {
	cfg := publicHTMLConfig()
	for _, body := range []string{`<div class=service data-name=x data-state=operational>`, `<div class=service data-name=x data-state=operational></span>`, `<div class=service data-name=x data-state=operational data-state=outage></div>`, `<div class=service data-name=x><div class=service data-name=y></div></div>`, `<!-- unfinished`, `<script>unclosed`} {
		if _, err := ParsePublicStatus("html", []byte(body), "text/html", cfg, publicTestNow()); err == nil {
			t.Fatalf("ambiguous HTML accepted: %s", body)
		}
	}
	p, err := ParsePublicStatus("html", []byte(`<html><body><h1>All systems operational</h1><a href="/status">healthy</a></body></html>`), "text/html", cfg, publicTestNow())
	if err != nil || !p.Unknown || p.Count != 0 {
		t.Fatalf("unstructured page must be unknown: %+v %v", p, err)
	}
	if _, err := ParsePublicStatus("auto", []byte(`<p>healthy</p>`), "text/html", nil, publicTestNow()); !errors.Is(err, ErrPublicStatusConfig) {
		t.Fatalf("HTML needs mapping: %v", err)
	}
}
func TestPublicStatusLimitsAndUnknown(t *testing.T) {
	var items []string
	for i := 0; i < 205; i++ {
		state := "operational"
		if i == 204 {
			state = "major_outage"
		}
		items = append(items, fmt.Sprintf(`{"name":"model-%d","status":"%s"}`, i, state))
	}
	p, err := ParsePublicStatus("statuspage", []byte(`{"components":[`+strings.Join(items, ",")+`]}`), "application/json", nil, publicTestNow())
	if err != nil || p.Count != 205 || len(p.Components) != 200 || !p.Truncated || !p.Unhealthy {
		t.Fatalf("truncation lost tail outage: %+v %v", p, err)
	}
	p, err = ParsePublicStatus("json", []byte(`{"components":[]}`), "application/json", nil, publicTestNow())
	if err != nil || !p.Unknown {
		t.Fatalf("empty response: %+v %v", p, err)
	}
	for _, body := range []string{`{"healthy":true}`, `{"components":[{"name":"x"}]}`, `{"components":[]} {}`, strings.Repeat(" ", publicStatusBodyLimit+1)} {
		p, err := ParsePublicStatus("json", []byte(body), "application/json", nil, publicTestNow())
		if err == nil && (p == nil || !p.Unknown) {
			t.Fatalf("invalid response accepted as healthy: %.80s %+v", body, p)
		}
	}
}
func TestPublicStatusConfigValidation(t *testing.T) {
	for _, cfg := range []*PublicStatusConfig{{Format: "execute"}, {ResourcePath: "https://example.com/status"}, {ResourcePath: "//evil/status"}, {ResourcePath: "/status?token=x"}, {ResourcePath: "/status#secret"}, {ResourcePath: "/../secret"}, {ResourcePath: "/%2e%2e/secret"}, {ItemsPath: "data[*]"}, {HealthyValues: []string{"up"}, UnhealthyValues: []string{"UP"}}, {Format: "html"}, {HTMLItemTag: "div onclick"}} {
		if err := ValidatePublicStatusConfig(cfg); !errors.Is(err, ErrPublicStatusConfig) {
			t.Errorf("accepted invalid config %+v", cfg)
		}
	}
	if err := ValidatePublicStatusConfig(publicHTMLConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestPublicStatusJSONAmbiguityAndRootArray(t *testing.T) {
	for _, body := range []string{`{"components":[{"name":"API","status":"major_outage","status":"operational"}]}`, strings.Repeat("[", 66) + `0` + strings.Repeat("]", 66)} {
		if _, err := ParsePublicStatus("json", []byte(body), "application/json", nil, publicTestNow()); !errors.Is(err, ErrPublicStatusUnsupported) {
			t.Fatalf("ambiguous JSON accepted: %v", err)
		}
	}
	cfg := &PublicStatusConfig{ItemsPath: "$", NameField: "label", StatusField: "state", HealthyValues: []string{"1"}, UnhealthyValues: []string{"0"}}
	p, err := ParsePublicStatus("json", []byte(`[{"label":"API","state":1},{"label":"Chat","state":0}]`), "application/json", cfg, publicTestNow())
	if err != nil || p.Count != 2 || !p.Unhealthy {
		t.Fatalf("root array: %+v %v", p, err)
	}
}
func TestPublicStatusHTMLBlockBoundary(t *testing.T) {
	cfg := publicHTMLConfig()
	cfg.HTMLStatusAttribute = ""
	p, err := ParsePublicStatus("html", []byte(`<div class="service" data-name="API"><div>operation</div><div>al</div></div>`), "text/html", cfg, publicTestNow())
	if err != nil || !p.Unknown {
		t.Fatalf("block text must not concatenate into healthy: %+v %v", p, err)
	}
}
