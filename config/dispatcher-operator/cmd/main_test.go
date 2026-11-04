package main

import (
	"reflect"
	"testing"
)

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
