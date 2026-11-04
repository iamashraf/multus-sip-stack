package kamailio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/dispatch"
)

const listSuccessBody = `{"jsonrpc":"2.0","result":{"NRSETS":1,"RECORDS":[{"SET":{"ID":1,"TARGETS":[{"DEST":{"URI":"sip:10.244.3.35:5060","FLAGS":"AX","PRIORITY":0}},{"DEST":{"URI":"sip:10.244.3.36:5060","FLAGS":"AX","PRIORITY":0}}]}}]},"id":1}`

const listEmptyBody = `{"jsonrpc":"2.0","result":{"NRSETS":0,"RECORDS":[]},"id":1}`

const addRemoveSuccessBody = `{"jsonrpc":"2.0","result":"Ok. Dispatcher destination added.","id":1}`

const ongoingReloadBody = `{"jsonrpc":"2.0","error":{"code":500,"message":"Ongoing reload"},"id":1}`

func newTestServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestList_ParsesMatchingGroup(t *testing.T) {
	srv := newTestServer(http.StatusOK, listSuccessBody)
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	got, err := c.List(context.Background(), 1)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	want := dispatch.IPSet{"10.244.3.35": true, "10.244.3.36": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestList_EmptyWhenGroupAbsent(t *testing.T) {
	srv := newTestServer(http.StatusOK, listEmptyBody)
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	got, err := c.List(context.Background(), 1)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if got == nil {
		t.Fatal("List returned nil IPSet, want empty non-nil")
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty", got)
	}
}

func TestAdd_Success(t *testing.T) {
	srv := newTestServer(http.StatusOK, addRemoveSuccessBody)
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	if err := c.Add(context.Background(), 1, "sip:10.244.3.35:5060"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
}

func TestAdd_SendsIntParamsNotStrings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		params, ok := decoded["params"].([]interface{})
		if !ok || len(params) < 4 {
			t.Fatalf("params = %v, want a 4-element array", decoded["params"])
		}
		if _, ok := params[0].(float64); !ok {
			t.Errorf("params[0] = %T (%v), want JSON number (float64)", params[0], params[0])
		} else if params[0] != float64(1) {
			t.Errorf("params[0] = %v, want 1", params[0])
		}
		if _, ok := params[2].(float64); !ok {
			t.Errorf("params[2] = %T (%v), want JSON number (float64)", params[2], params[2])
		} else if params[2] != float64(0) {
			t.Errorf("params[2] = %v, want 0", params[2])
		}
		if _, ok := params[3].(float64); !ok {
			t.Errorf("params[3] = %T (%v), want JSON number (float64)", params[3], params[3])
		} else if params[3] != float64(0) {
			t.Errorf("params[3] = %v, want 0", params[3])
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(addRemoveSuccessBody))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	if err := c.Add(context.Background(), 1, "sip:10.244.3.35:5060"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
}

func TestAdd_OngoingReload(t *testing.T) {
	srv := newTestServer(http.StatusInternalServerError, ongoingReloadBody)
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	err := c.Add(context.Background(), 1, "sip:10.244.3.35:5060")
	if err == nil {
		t.Fatal("Add returned nil error, want ErrOngoingReload")
	}
	if !errors.Is(err, ErrOngoingReload) {
		t.Errorf("Add error = %v, want errors.Is(err, ErrOngoingReload)", err)
	}
}

func TestRemove_Success(t *testing.T) {
	srv := newTestServer(http.StatusOK, addRemoveSuccessBody)
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	if err := c.Remove(context.Background(), 1, "sip:10.244.3.35:5060"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
}

func TestRemove_OngoingReload(t *testing.T) {
	srv := newTestServer(http.StatusInternalServerError, ongoingReloadBody)
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	err := c.Remove(context.Background(), 1, "sip:10.244.3.35:5060")
	if err == nil {
		t.Fatal("Remove returned nil error, want ErrOngoingReload")
	}
	if !errors.Is(err, ErrOngoingReload) {
		t.Errorf("Remove error = %v, want errors.Is(err, ErrOngoingReload)", err)
	}
}
