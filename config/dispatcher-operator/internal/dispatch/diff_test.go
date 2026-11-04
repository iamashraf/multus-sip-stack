package dispatch

import (
	"reflect"
	"testing"
)

func TestDiff(t *testing.T) {
	cases := []struct {
		name                string
		desired, current    IPSet
		wantAdd, wantRemove IPSet
	}{
		{
			name:       "one new address",
			desired:    IPSet{"10.0.0.1": true, "10.0.0.2": true, "10.0.0.3": true},
			current:    IPSet{"10.0.0.2": true, "10.0.0.3": true},
			wantAdd:    IPSet{"10.0.0.1": true},
			wantRemove: IPSet{},
		},
		{
			name:       "one stale address",
			desired:    IPSet{"10.0.0.2": true, "10.0.0.3": true},
			current:    IPSet{"10.0.0.1": true, "10.0.0.2": true, "10.0.0.3": true},
			wantAdd:    IPSet{},
			wantRemove: IPSet{"10.0.0.1": true},
		},
		{
			name:       "empty both sides",
			desired:    IPSet{},
			current:    IPSet{},
			wantAdd:    IPSet{},
			wantRemove: IPSet{},
		},
		{
			name:       "desired goes to empty",
			desired:    IPSet{},
			current:    IPSet{"10.0.0.1": true, "10.0.0.2": true},
			wantAdd:    IPSet{},
			wantRemove: IPSet{"10.0.0.1": true, "10.0.0.2": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAdd, gotRemove := Diff(tc.desired, tc.current)
			if !reflect.DeepEqual(gotAdd, tc.wantAdd) {
				t.Errorf("toAdd = %v, want %v", gotAdd, tc.wantAdd)
			}
			if !reflect.DeepEqual(gotRemove, tc.wantRemove) {
				t.Errorf("toRemove = %v, want %v", gotRemove, tc.wantRemove)
			}
		})
	}
}
