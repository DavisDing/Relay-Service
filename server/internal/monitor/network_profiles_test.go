package monitor

import (
	"strings"
	"testing"
)

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
