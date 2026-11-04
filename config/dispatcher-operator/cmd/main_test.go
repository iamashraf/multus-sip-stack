package main

import (
	"reflect"
	"testing"
)

// TestBuildManagerOptions_CacheScopedToNamespace pins the fix for the
// cross-task RBAC gap: the manager's cache must be restricted to the
// --namespace value, since the deployed RBAC Role only grants
// list/watch on endpointslices (and leases) in that one namespace, not
// cluster-wide. This is a structural check only (the built Options carry
// the right namespace key) — it cannot verify the live RBAC behavior
// itself; that needs a real cluster and is covered by Task 7's deploy.
func TestBuildManagerOptions_CacheScopedToNamespace(t *testing.T) {
	opts := buildManagerOptions(nil, "voice", ":8443", ":8081", true)

	if opts.Cache.DefaultNamespaces == nil {
		t.Fatal("Cache.DefaultNamespaces is nil, want it restricted to the given namespace")
	}
	if _, ok := opts.Cache.DefaultNamespaces["voice"]; !ok {
		t.Errorf("Cache.DefaultNamespaces = %v, want a \"voice\" key", opts.Cache.DefaultNamespaces)
	}
	if len(opts.Cache.DefaultNamespaces) != 1 {
		t.Errorf("Cache.DefaultNamespaces has %d entries, want exactly 1 (namespace-scoped, not cluster-wide)", len(opts.Cache.DefaultNamespaces))
	}
}

func TestParseTargets(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{"two targets", "kamailio-a=http://a:8080/RPC,kamailio-b=http://b:8080/RPC",
			map[string]string{"kamailio-a": "http://a:8080/RPC", "kamailio-b": "http://b:8080/RPC"}, false},
		{"empty string", "", nil, true},
		{"missing equals", "kamailio-a", nil, true},
		{"duplicate name", "kamailio-a=http://a:8080/RPC,kamailio-a=http://b:8080/RPC", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTargets(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
