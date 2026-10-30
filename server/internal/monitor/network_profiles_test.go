package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"relayservice/internal/storage"
)

type profileControlFixture struct {
	mu           sync.Mutex
	metas        []networkProfileMeta
	status       int
	detailStatus int
	details      map[string]string
	reads        map[string]int
	listReads    int
	token        string
	t            *testing.T
}

func (f *profileControlFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		f.t.Errorf("unexpected control request: %s %s", r.Method, r.URL.Path)
	}
	expected := ""
	if f.token != "" {
		expected = "Bearer " + f.token
	}
	if r.Header.Get("Authorization") != expected {
		f.t.Error("control request used a different credential")
	}
	if r.URL.Path == "/api/control/profiles" {
		f.listReads++
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		json.NewEncoder(w).Encode(f.metas)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/control/profiles/")
	if id == r.URL.Path {
		f.t.Error("unexpected endpoint")
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.reads[id]++
	if f.detailStatus != 0 {
		w.WriteHeader(f.detailStatus)
		return
	}
	content, ok := f.details[id]
	if !ok {
		content = "proxies: [{name: 节点, type: anytls}]"
	}
	meta := networkProfileMeta{ID: id}
	for _, candidate := range f.metas {
		if candidate.ID == id {
			meta = candidate
			break
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"meta": meta, "content": content})
}

func newProfileControlTest(t *testing.T) (*Engine, *networkWorker, *profileControlFixture) {
	t.Helper()
	f := &profileControlFixture{t: t, reads: map[string]int{}, details: map[string]string{}, token: "profile-test-token"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	store := storage.NewMemory()
	t.Cleanup(func() { store.Close() })
	e := New(store, testCryptor{value: f.token})
	w := newNetworkTestWorker(time.Now())
	w.settings.ControlBaseURL = srv.URL
	w.controlCipher = "cipher:" + f.token
	// The core credential must never be forwarded to the control origin.
	w.rec.Cipher = "cipher:unrelated-core-token"
	w.rec.Service.AllowPrivate = true
	return e, w, f
}

func TestNetworkProfilesDiscoversAddUpdateDeleteWithoutProviders(t *testing.T) {
	e, w, f := newProfileControlTest(t)
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	total, upload, download := int64(1000), int64(100), int64(200)
	f.metas = []networkProfileMeta{
		{ID: "a", Name: "start", Type: "remote", Active: true, UpdatedAt: now.UnixMilli(), SubscriptionInfo: &mihomoRawSubscription{Total: &total, Upload: &upload, Download: &download}},
		{ID: "b", Name: "1", Type: "local", UpdatedAt: now.UnixMilli()},
		{ID: "a", Name: "重复ID", Type: "remote", UpdatedAt: now.UnixMilli()},
		{ID: "overlay", Name: "叠加", Type: "merge"},
		{ID: "script", Name: "脚本", Type: "script"},
	}
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.view.Subscriptions) != 2 || len(w.coreSubs) != 0 || len(w.view.Nodes) != 2 || w.view.Overview.ActiveConfig != "start" {
		t.Fatalf("profiles depended on core providers or included overlays: %+v", w.view.Subscriptions)
	}
	if w.profileSubs[0].UsedBytes == nil || *w.profileSubs[0].UsedBytes != 300 || !w.profileSubs[0].NodeCountKnown {
		t.Fatalf("profile quota/catalog not projected: %+v", w.profileSubs[0])
	}
	f.mu.Lock()
	f.metas = []networkProfileMeta{
		{ID: "a", Name: "已改名", Type: "remote", UpdatedAt: now.Add(time.Minute).UnixMilli()},
		{ID: "future", Name: "后来添加的任意订阅", Type: "remote", Active: true, UpdatedAt: now.UnixMilli()},
	}
	f.details["a"] = "proxies: [{name: 更新节点, type: anytls}, {name: 第二节点, type: ss}]"
	f.mu.Unlock()
	now = now.Add(4 * time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.profileSubs) != 2 || w.profileSubs[0].Name != "start" || f.listReads != 1 {
		t.Fatal("list refreshed before its bounded interval")
	}
	now = now.Add(time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.profileSubs) != 2 || w.profileSubs[0].ID != "profile-a" || w.profileSubs[0].Name != "已改名" || w.profileSubs[0].NodeCount != 2 || w.profileSubs[1].ID != "profile-future" || w.view.Overview.ActiveConfig != "后来添加的任意订阅" {
		t.Fatalf("dynamic changes not discovered: %+v", w.profileSubs)
	}
	for _, node := range w.view.Nodes {
		if node.SubscriptionID == "profile-b" || node.Loaded || node.CheckedAt != nil || node.DelayMS != nil || node.State != "unknown" {
			t.Fatalf("deleted node retained or invented health: %+v", node)
		}
	}
	f.mu.Lock()
	f.metas = []networkProfileMeta{}
	f.mu.Unlock()
	now = now.Add(5 * time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.view.Subscriptions) != 0 || len(w.view.Nodes) != 0 || w.view.Overview.ActiveConfig != "" || w.view.Coverage.SourceStates["profile_nodes"] != "available" {
		t.Fatal("removed profiles were not removed from inventory")
	}
}

func TestNetworkProfilesRefreshDoesNotWaitForDetailsOrStarveTail(t *testing.T) {
	e, w, f := newProfileControlTest(t)
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	for i := 0; i < 30; i++ {
		f.metas = append(f.metas, networkProfileMeta{ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("订阅%d", i), Type: "remote", UpdatedAt: now.UnixMilli()})
	}
	e.pollNetworkProfiles(context.Background(), w)
	if len(f.reads) != 4 {
		t.Fatal("detail request batch exceeded four")
	}
	f.mu.Lock()
	f.metas = append([]networkProfileMeta{{ID: "new", Name: "新增", Type: "remote", UpdatedAt: now.UnixMilli()}}, f.metas...)
	f.mu.Unlock()
	now = now.Add(5 * time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.profileSubs) != 31 || f.listReads != 2 || f.reads["p07"] != 1 {
		t.Fatal("new profile discovery waited for old details or cursor progress was lost")
	}
	// Force a list refresh before every next batch and reverse its ordering.
	// Pending details must still make progress through the entire catalog.
	for i := 0; i < 7; i++ {
		f.mu.Lock()
		for left, right := 0, len(f.metas)-1; left < right; left, right = left+1, right-1 {
			f.metas[left], f.metas[right] = f.metas[right], f.metas[left]
		}
		f.mu.Unlock()
		now = now.Add(5 * time.Minute)
		e.pollNetworkProfiles(context.Background(), w)
	}
	if len(f.reads) != 31 || len(w.profileNodes) != 31 {
		t.Fatalf("tail profiles starved: details=%d nodes=%d", len(f.reads), len(w.profileNodes))
	}
}

func TestNetworkProfilesAuthFailureAndRecoveryPreservesCatalog(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e, w, f := newProfileControlTest(t)
			now := time.Now()
			e.now = func() time.Time { return now }
			f.metas = []networkProfileMeta{{ID: "a", Name: "订阅", Type: "remote", UpdatedAt: now.UnixMilli()}}
			e.pollNetworkProfiles(context.Background(), w)
			f.mu.Lock()
			f.status = status
			f.mu.Unlock()
			now = now.Add(5 * time.Minute)
			e.pollNetworkProfiles(context.Background(), w)
			if w.view.Coverage.SourceStates["profiles"] != "unauthorized" || len(w.view.Subscriptions) != 1 || len(w.view.Nodes) != 1 {
				t.Fatal("auth failure erased catalog or was reported as generic unavailable")
			}
			notes := strings.Join(w.view.Coverage.Notes, " ")
			if !strings.Contains(notes, "拒绝") || strings.Contains(notes, f.token) || strings.Contains(notes, "unrelated-core-token") {
				t.Fatal("auth diagnostic was missing or leaked secrets")
			}
			f.mu.Lock()
			f.status = 0
			f.mu.Unlock()
			e.pollNetworkProfiles(context.Background(), w)
			if w.view.Coverage.SourceStates["profiles"] != "available" || strings.Contains(strings.Join(w.view.Coverage.Notes, " "), "拒绝") {
				t.Fatal("auth failure state/diagnostic survived recovery")
			}
		})
	}
}

func TestNetworkProfilesMissingTokenAndDetailAuth(t *testing.T) {
	e, w, f := newProfileControlTest(t)
	now := time.Now()
	e.now = func() time.Time { return now }
	w.controlCipher = ""
	f.token = ""
	f.status = http.StatusUnauthorized
	e.pollNetworkProfiles(context.Background(), w)
	if w.view.Coverage.SourceStates["profiles"] != "unauthorized" || !strings.Contains(strings.Join(w.view.Coverage.Notes, " "), "填写独立的 CONTROL_TOKEN") {
		t.Fatal("missing control token did not produce actionable guidance")
	}
	f.mu.Lock()
	f.status = 0
	f.detailStatus = http.StatusForbidden
	f.metas = []networkProfileMeta{{ID: "a", Name: "订阅", Type: "remote"}}
	f.mu.Unlock()
	e.pollNetworkProfiles(context.Background(), w)
	if w.view.Coverage.SourceStates["profiles"] != "available" || w.view.Coverage.SourceStates["profile_nodes"] != "unauthorized" || w.profileSubs[0].NodeCountKnown {
		t.Fatal("detail authentication failure masked available metadata or invented an empty catalog")
	}
	f.mu.Lock()
	f.detailStatus = 0
	f.mu.Unlock()
	now = now.Add(5 * time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if w.view.Coverage.SourceStates["profile_nodes"] != "available" || !w.profileSubs[0].NodeCountKnown || strings.Contains(strings.Join(w.view.Coverage.Notes, " "), "控制接口要求认证") {
		t.Fatal("detail authentication diagnostic survived successful recovery")
	}
	f.mu.Lock()
	f.status = http.StatusServiceUnavailable
	f.mu.Unlock()
	now = now.Add(5 * time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if w.view.Coverage.SourceStates["profiles"] != "unavailable" || len(w.view.Subscriptions) != 1 || len(w.view.Nodes) != 1 || strings.Contains(strings.Join(w.view.Coverage.Notes, " "), "控制接口要求认证") {
		t.Fatal("temporary failure erased catalog or was misclassified as authentication")
	}
}

func TestNetworkProfileMembershipRefreshesAfterCoreReloadAndDropsOnAuthFailure(t *testing.T) {
	e, w, f := newProfileControlTest(t)
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	f.metas = []networkProfileMeta{
		{ID: "a", Name: "start", Type: "remote", Active: true, UpdatedAt: now.UnixMilli()},
		{ID: "b", Name: "1", Type: "remote", UpdatedAt: now.UnixMilli()},
	}
	f.details["a"] = "proxies: [{name: same, type: ss}]"
	f.details["b"] = "proxies: [{name: same, type: ss}]"
	core := func(id string) []byte {
		return []byte(fmt.Sprintf(`{"proxies":{"same":{"id":%q,"type":"Shadowsocks","history":[{"time":"2026-10-30T00:00:00Z","delay":17}]}}}`, id))
	}
	w.applyInventory([]byte(`{"providers":{}}`), nil, core("first-runtime-id"), nil, "", now)
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.view.Nodes) != 2 || w.view.Coverage.SourceStates["profile_membership"] != "available" {
		t.Fatalf("active profile failed to merge after HTTP detail projection: %+v", w.view.Nodes)
	}
	for _, node := range w.view.Nodes {
		if node.Loaded && (!networkNodeHasSubscription(node, "profile-a") || node.DelayMS == nil || *node.DelayMS != 17) {
			t.Fatalf("active profile lost delay: %+v", node)
		}
	}
	f.mu.Lock()
	f.metas[0].Active = false
	f.metas[1].Active = true
	f.mu.Unlock()
	now = now.Add(time.Minute)
	w.applyInventory([]byte(`{"providers":{}}`), nil, core("second-runtime-id"), nil, "", now)
	if len(w.view.Nodes) != 3 || w.view.Coverage.SourceStates["profile_membership"] != "unknown" {
		t.Fatal("old membership survived a core reload before control revalidation")
	}
	e.pollNetworkProfiles(context.Background(), w)
	if f.listReads != 2 || len(w.view.Nodes) != 2 {
		t.Fatal("core reload did not promptly refresh current profile")
	}
	for _, node := range w.view.Nodes {
		if node.Loaded && (!networkNodeHasSubscription(node, "profile-b") || networkNodeHasSubscription(node, "profile-a")) {
			t.Fatalf("old profile membership survived reload: %+v", node)
		}
	}
	f.mu.Lock()
	f.status = http.StatusUnauthorized
	f.mu.Unlock()
	now = now.Add(5 * time.Minute)
	e.pollNetworkProfiles(context.Background(), w)
	if len(w.view.Nodes) != 3 || w.view.Coverage.SourceStates["profile_membership"] != "unknown" {
		t.Fatal("failed control authentication kept an asserted core/profile association")
	}
}

func TestNetworkProfileOnlyProjectsDirectoryAndRejectsUnsafeYAML(t *testing.T) {
	content := "proxies:\n  - name: 台湾1\n    type: anytls\n    server: private.example\n    password: do-not-save\n"
	nodes, err := parseNetworkProfileNodes(content, "sub", "core-secret")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("directory parse: %+v %v", nodes, err)
	}
	n := nodes[0]
	if n.Loaded || n.State != "unknown" || n.CheckedAt != nil || n.DelayMS != nil || n.SubscriptionID != "sub" {
		t.Fatalf("directory invented health: %+v", n)
	}
	encoded := string(networkFixtureJSON(t, nodes))
	if strings.Contains(encoded, "do-not-save") || strings.Contains(encoded, "private.example") {
		t.Fatal("raw configuration persisted in projection")
	}
	for _, invalid := range []string{
		"proxy-providers:\n  airport:\n    url: https://private.example/subscription\n",
		"proxies: &p [{name: a, type: anytls}, *p]\n",
		"proxies: [{name: a, type: anytls}, {name: a, type: anytls}]\n",
		"proxies: [{name: core-secret, type: anytls}]\n",
		"proxies: [{name: a, name: b, type: anytls}]\n",
		"proxies: not-an-array\n",
	} {
		if _, err := parseNetworkProfileNodes(invalid, "sub", "core-secret"); err == nil {
			t.Errorf("unsafe directory accepted: %q", invalid)
		}
	}
}
