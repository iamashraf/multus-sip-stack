package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/dispatch"
	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/kamailio"
)

const (
	testNamespace = "voice"
	testService   = "freeswitch"
	testGroup     = 1
	testPort      = 5060
)

var (
	testEnv    *envtest.Environment
	testCfg    *rest.Config
	testScheme *runtime.Scheme
	k8sClient  client.Client
	nsOnce     sync.Once
)

func TestMain(m *testing.M) {
	testScheme = runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "add client-go scheme: %v\n", err)
		os.Exit(1)
	}
	if err := discoveryv1.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "add discoveryv1 scheme: %v\n", err)
		os.Exit(1)
	}

	testEnv = &envtest.Environment{}
	if assets := os.Getenv("KUBEBUILDER_ASSETS"); assets != "" {
		testEnv.BinaryAssetsDirectory = assets
	}

	var err error
	testCfg, err = testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest start: %v\n", err)
		os.Exit(1)
	}

	k8sClient, err = client.New(testCfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "client.New: %v\n", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}

	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

// --- fake Kamailio server ---

// fakeKamailio is an httptest-backed Kamailio JSON-RPC server. When
// rateLimited is set, writes within 5s of the last successful write fail
// with the Ongoing-reload fault, mirroring Kamailio's real rate limit.
type fakeKamailio struct {
	mu          sync.Mutex
	srv         *httptest.Server
	current     dispatch.IPSet
	adds        []string
	removes     []string
	rateLimited bool
	lastWrite   time.Time
}

func newFakeKamailio(seed dispatch.IPSet) *fakeKamailio {
	f := &fakeKamailio{current: dispatch.IPSet{}}
	for ip := range seed {
		f.current[ip] = true
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeKamailio) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch req.Method {
	case "dispatcher.list":
		writeListResponse(w, testGroup, f.current)
	case "dispatcher.add":
		uri, _ := req.Params[1].(string)
		if f.rateLimited && f.writeBlockedLocked() {
			writeFault(w)
			return
		}
		f.current[ipFromURI(uri)] = true
		f.adds = append(f.adds, uri)
		f.lastWrite = time.Now()
		writeOK(w)
	case "dispatcher.remove":
		uri, _ := req.Params[1].(string)
		if f.rateLimited && f.writeBlockedLocked() {
			writeFault(w)
			return
		}
		delete(f.current, ipFromURI(uri))
		f.removes = append(f.removes, uri)
		f.lastWrite = time.Now()
		writeOK(w)
	default:
		http.Error(w, "unknown method", http.StatusNotFound)
	}
}

// writeBlockedLocked reports whether a write should fail with the
// Ongoing-reload fault. Callers must hold f.mu.
func (f *fakeKamailio) writeBlockedLocked() bool {
	return !f.lastWrite.IsZero() && time.Since(f.lastWrite) < 5*time.Second
}

func (f *fakeKamailio) hasIP(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current[ip]
}

func (f *fakeKamailio) ipCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.current)
}

func (f *fakeKamailio) hasAdd(uri string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.adds {
		if a == uri {
			return true
		}
	}
	return false
}

func (f *fakeKamailio) hasRemove(uri string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rm := range f.removes {
		if rm == uri {
			return true
		}
	}
	return false
}

func writeListResponse(w http.ResponseWriter, group int, current dispatch.IPSet) {
	type dest struct {
		URI string `json:"URI"`
	}
	type target struct {
		DEST dest `json:"DEST"`
	}
	type set struct {
		ID      int      `json:"ID"`
		TARGETS []target `json:"TARGETS"`
	}
	type record struct {
		SET set `json:"SET"`
	}
	targets := make([]target, 0, len(current))
	for ip := range current {
		targets = append(targets, target{DEST: dest{URI: fmt.Sprintf("sip:%s:%d", ip, testPort)}})
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"result": map[string]interface{}{
			"NRSETS":  1,
			"RECORDS": []record{{SET: set{ID: group, TARGETS: targets}}},
		},
		"id": 1,
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeOK(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"Ok","id":1}`))
}

func writeFault(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":500,"message":"Ongoing reload"},"id":1}`))
}

func ipFromURI(uri string) string {
	parts := strings.Split(uri, ":")
	if len(parts) > 1 {
		return parts[1]
	}
	return uri
}

// --- test helpers ---

func ensureNamespace(t *testing.T) {
	t.Helper()
	nsOnce.Do(func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
		if err := k8sClient.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create namespace: %v", err)
		}
	})
}

// startReconciler runs a Reconciler wired to the given targets against the
// shared envtest cluster, stopping it when the test ends.
func startReconciler(t *testing.T, targets map[string]TargetClient) {
	t.Helper()

	skipNameValidation := true
	mgr, err := ctrl.NewManager(testCfg, ctrl.Options{
		Scheme:                 testScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		// Each test starts its own manager/controller in the same process,
		// and controller-runtime's metrics registry is process-global, so
		// the default "unique controller name" check would fail across
		// tests. Test-only; production wiring is unaffected.
		Controller: ctrlconfig.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	rec := &Reconciler{
		Client:            mgr.GetClient(),
		Namespace:         testNamespace,
		FreeSwitchService: testService,
		DispatcherGroup:   testGroup,
		Port:              testPort,
		Targets:           targets,
	}
	if err := rec.SetupWithManager(mgr); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "manager exited: %v\n", err)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatalf("cache did not sync")
	}
}

func boolPtr(b bool) *bool { return &b }

func createEndpointSlice(t *testing.T, name string, addrs []string) {
	t.Helper()

	endpoints := make([]discoveryv1.Endpoint, 0, len(addrs))
	for _, a := range addrs {
		endpoints = append(endpoints, discoveryv1.Endpoint{
			Addresses:  []string{a},
			Conditions: discoveryv1.EndpointConditions{Ready: boolPtr(true)},
		})
	}

	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{discoveryv1.LabelServiceName: testService},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
	}
	if err := k8sClient.Create(context.Background(), slice); err != nil {
		t.Fatalf("create EndpointSlice: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), slice)
	})
}

func eventually(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// --- tests ---

func TestReconcile_AddsNewEndpoint(t *testing.T) {
	ensureNamespace(t)

	fakeA := newFakeKamailio(nil)
	fakeB := newFakeKamailio(nil)
	defer fakeA.srv.Close()
	defer fakeB.srv.Close()

	targets := map[string]TargetClient{
		"kamailio-a": kamailio.NewClient(fakeA.srv.URL, nil),
		"kamailio-b": kamailio.NewClient(fakeB.srv.URL, nil),
	}
	startReconciler(t, targets)

	createEndpointSlice(t, "fs-addnew", []string{"10.244.3.35"})

	wantURI := "sip:10.244.3.35:5060"
	eventually(t, 5*time.Second, func() bool {
		return fakeA.hasAdd(wantURI) && fakeB.hasAdd(wantURI)
	})
}

func TestReconcile_RemovesGoneEndpoint(t *testing.T) {
	ensureNamespace(t)

	fakeA := newFakeKamailio(dispatch.IPSet{"10.244.3.40": true, "10.244.3.99": true})
	fakeB := newFakeKamailio(dispatch.IPSet{"10.244.3.40": true, "10.244.3.99": true})
	defer fakeA.srv.Close()
	defer fakeB.srv.Close()

	targets := map[string]TargetClient{
		"kamailio-a": kamailio.NewClient(fakeA.srv.URL, nil),
		"kamailio-b": kamailio.NewClient(fakeB.srv.URL, nil),
	}
	startReconciler(t, targets)

	createEndpointSlice(t, "fs-remove", []string{"10.244.3.40"})

	staleURI := "sip:10.244.3.99:5060"
	eventually(t, 5*time.Second, func() bool {
		return fakeA.hasRemove(staleURI) && fakeB.hasRemove(staleURI)
	})
}

func TestReconcile_EmptyDesiredRemovesEverything(t *testing.T) {
	ensureNamespace(t)

	fakeA := newFakeKamailio(dispatch.IPSet{"10.244.3.10": true, "10.244.3.11": true})
	fakeB := newFakeKamailio(dispatch.IPSet{"10.244.3.10": true, "10.244.3.11": true})
	defer fakeA.srv.Close()
	defer fakeB.srv.Close()

	targets := map[string]TargetClient{
		"kamailio-a": kamailio.NewClient(fakeA.srv.URL, nil),
		"kamailio-b": kamailio.NewClient(fakeB.srv.URL, nil),
	}
	startReconciler(t, targets)

	createEndpointSlice(t, "fs-empty", nil)

	eventually(t, 5*time.Second, func() bool {
		return fakeA.ipCount() == 0 && fakeB.ipCount() == 0
	})
}

func TestReconcile_OneTargetUnreachableStillSyncsOther(t *testing.T) {
	ensureNamespace(t)

	fakeA := newFakeKamailio(nil)
	fakeA.srv.Close() // kamailio-a is unreachable for the whole test

	fakeB := newFakeKamailio(nil)
	defer fakeB.srv.Close()

	targets := map[string]TargetClient{
		"kamailio-a": kamailio.NewClient(fakeA.srv.URL, nil),
		"kamailio-b": kamailio.NewClient(fakeB.srv.URL, nil),
	}
	startReconciler(t, targets)

	createEndpointSlice(t, "fs-unreachable", []string{"10.244.3.50"})

	wantURI := "sip:10.244.3.50:5060"
	eventually(t, 5*time.Second, func() bool {
		return fakeB.hasAdd(wantURI)
	})
}

func TestReconcile_BurstOfChangesConvergesAfterOngoingReload(t *testing.T) {
	ensureNamespace(t)

	fakeA := newFakeKamailio(nil)
	fakeA.rateLimited = true
	fakeB := newFakeKamailio(nil)
	fakeB.rateLimited = true
	defer fakeA.srv.Close()
	defer fakeB.srv.Close()

	targets := map[string]TargetClient{
		"kamailio-a": kamailio.NewClient(fakeA.srv.URL, nil),
		"kamailio-b": kamailio.NewClient(fakeB.srv.URL, nil),
	}
	startReconciler(t, targets)

	addrs := []string{"10.244.4.1", "10.244.4.2", "10.244.4.3"}
	createEndpointSlice(t, "fs-burst", addrs)

	eventually(t, 30*time.Second, func() bool {
		for _, ip := range addrs {
			if !fakeA.hasIP(ip) || !fakeB.hasIP(ip) {
				return false
			}
		}
		return true
	})
}

// --- stub TargetClients for direct (no-manager) Reconcile calls ---

// stubTarget is a plain in-memory TargetClient, used where a full
// httptest-backed kamailio.Client isn't needed.
type stubTarget struct {
	mu          sync.Mutex
	current     dispatch.IPSet
	listErr     error
	addErr      error
	removeErr   error
	addCalls    []string
	removeCalls []string
}

func (s *stubTarget) List(_ context.Context, _ int) (dispatch.IPSet, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.current, nil
}

func (s *stubTarget) Add(_ context.Context, _ int, uri string) error {
	s.mu.Lock()
	s.addCalls = append(s.addCalls, uri)
	s.mu.Unlock()
	return s.addErr
}

func (s *stubTarget) Remove(_ context.Context, _ int, uri string) error {
	s.mu.Lock()
	s.removeCalls = append(s.removeCalls, uri)
	s.mu.Unlock()
	return s.removeErr
}

func (s *stubTarget) addCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.addCalls)
}

// blockingTarget simulates a hung Kamailio instance: List blocks until its
// context is done, as a dropped-packet failure would.
type blockingTarget struct{}

func (blockingTarget) List(ctx context.Context, _ int) (dispatch.IPSet, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingTarget) Add(_ context.Context, _ int, _ string) error    { return nil }
func (blockingTarget) Remove(_ context.Context, _ int, _ string) error { return nil }

func directTestSlice(name string, addrs []string) *discoveryv1.EndpointSlice {
	endpoints := make([]discoveryv1.Endpoint, 0, len(addrs))
	for _, a := range addrs {
		endpoints = append(endpoints, discoveryv1.Endpoint{
			Addresses:  []string{a},
			Conditions: discoveryv1.EndpointConditions{Ready: boolPtr(true)},
		})
	}
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{discoveryv1.LabelServiceName: testService},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
	}
}

// TestReconcile_OngoingReloadRequeuesWithoutHardError calls Reconcile
// directly (no manager) to pin down the two global constraints that the
// envtest-backed tests above can't directly assert: an Ongoing-reload fault
// must come back as a RequeueAfter with a nil error, never a hard error,
// and a failing target must not stop the others from being synced in the
// same pass.
func TestReconcile_OngoingReloadRequeuesWithoutHardError(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(directTestSlice("fs-direct-reload", []string{"10.244.5.1"})).
		Build()

	a := &stubTarget{listErr: errors.New("connection refused")}
	b := &stubTarget{current: dispatch.IPSet{}, addErr: fmt.Errorf("add: %w", kamailio.ErrOngoingReload)}

	r := &Reconciler{
		Client:            fakeClient,
		Namespace:         testNamespace,
		FreeSwitchService: testService,
		DispatcherGroup:   testGroup,
		Port:              testPort,
		Targets:           map[string]TargetClient{"kamailio-a": a, "kamailio-b": b},
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err != nil {
		t.Fatalf("Reconcile returned error %v, want nil: an Ongoing-reload fault must not be a hard error", err)
	}
	if res.RequeueAfter < 5*time.Second || res.RequeueAfter >= 6*time.Second {
		t.Errorf("RequeueAfter = %v, want in [5s, 6s) (5s plus up to 1s jitter)", res.RequeueAfter)
	}
	if b.addCallCount() == 0 {
		t.Error("kamailio-b.Add was never called: kamailio-a's List failure must not stop other targets from being synced")
	}
}

// TestReconcile_ListOngoingReloadRequeuesWithoutHardError covers the same
// constraint as the test above, but for the List call specifically.
// kamailio.Client's call() helper wraps ErrOngoingReload generically for
// any RPC method's "Ongoing reload" fault, dispatcher.list included, so
// Reconcile must treat a List failure wrapping the sentinel the same way
// it treats one from Add/Remove: RequeueAfter, never a hard error.
func TestReconcile_ListOngoingReloadRequeuesWithoutHardError(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(directTestSlice("fs-direct-list-reload", []string{"10.244.5.3"})).
		Build()

	a := &stubTarget{listErr: fmt.Errorf("list: %w", kamailio.ErrOngoingReload)}
	b := &stubTarget{current: dispatch.IPSet{}}

	r := &Reconciler{
		Client:            fakeClient,
		Namespace:         testNamespace,
		FreeSwitchService: testService,
		DispatcherGroup:   testGroup,
		Port:              testPort,
		Targets:           map[string]TargetClient{"kamailio-a": a, "kamailio-b": b},
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err != nil {
		t.Fatalf("Reconcile returned error %v, want nil: an Ongoing-reload fault from List must not be a hard error", err)
	}
	if res.RequeueAfter < 5*time.Second || res.RequeueAfter >= 6*time.Second {
		t.Errorf("RequeueAfter = %v, want in [5s, 6s) (5s plus up to 1s jitter)", res.RequeueAfter)
	}
	if b.addCallCount() == 0 {
		t.Error("kamailio-b.Add was never called: kamailio-a's List reload fault must not stop other targets from being synced")
	}
}

// TestReconcile_SlowTargetDoesNotBlockOthersForever asserts the per-target
// call timeout: a target that never responds (e.g. a dead pod IP dropping
// packets, not refusing the connection) must not stall the whole reconcile
// pass indefinitely, and other targets must still be synced.
func TestReconcile_SlowTargetDoesNotBlockOthersForever(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(directTestSlice("fs-direct-slow", []string{"10.244.5.2"})).
		Build()

	healthy := &stubTarget{current: dispatch.IPSet{}}

	r := &Reconciler{
		Client:            fakeClient,
		Namespace:         testNamespace,
		FreeSwitchService: testService,
		DispatcherGroup:   testGroup,
		Port:              testPort,
		Targets:           map[string]TargetClient{"kamailio-a": blockingTarget{}, "kamailio-b": healthy},
	}

	start := time.Now()
	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	elapsed := time.Since(start)

	// kamailio-a's List ultimately fails with context.DeadlineExceeded, a
	// genuine (non-Ongoing-reload) error, so it is expected back wrapped,
	// triggering the default workqueue backoff.
	if err == nil {
		t.Fatal("Reconcile returned nil error, want the wrapped timeout from kamailio-a")
	}
	if elapsed > 15*time.Second {
		t.Fatalf("Reconcile took %v, want bounded by the per-target call timeout", elapsed)
	}
	if healthy.addCallCount() == 0 {
		t.Error("kamailio-b was never synced while kamailio-a was hung")
	}
}
