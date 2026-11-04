// Package controller implements the Kubernetes reconciler that watches
// FreeSWITCH's EndpointSlice and syncs its ready addresses into every
// configured Kamailio dispatcher target.
package controller

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/dispatch"
	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/kamailio"
)

// targetCallTimeout bounds how long Reconcile waits on a single target's
// List/Add/Remove calls, so one unreachable or hung Kamailio instance can't
// stall syncing the rest for an unbounded time.
const targetCallTimeout = 10 * time.Second

// TargetClient is the Kamailio dispatcher operation set the reconciler needs.
// *kamailio.Client satisfies it.
type TargetClient interface {
	List(ctx context.Context, group int) (dispatch.IPSet, error)
	Add(ctx context.Context, group int, uri string) error
	Remove(ctx context.Context, group int, uri string) error
}

// Reconciler watches FreeSWITCH's EndpointSlice and reconciles its ready
// addresses into every configured Kamailio dispatcher target. It is a
// singleton reconciler: the req passed to Reconcile is ignored, since any
// watched event means "recompute the full desired state and resync every
// target."
type Reconciler struct {
	Client            client.Client
	Namespace         string
	FreeSwitchService string
	DispatcherGroup   int
	Port              int
	Targets           map[string]TargetClient
}

// Reconcile recomputes the desired dispatcher set from FreeSWITCH's
// EndpointSlice and pushes it to every configured target. One target being
// unreachable does not stop the others from being synced.
func (r *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var slices discoveryv1.EndpointSliceList
	if err := r.Client.List(ctx, &slices,
		client.InNamespace(r.Namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: r.FreeSwitchService},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("list EndpointSlices: %w", err)
	}

	desired := dispatch.IPSet{}
	for _, slice := range slices.Items {
		for _, ep := range slice.Endpoints {
			if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
				for _, addr := range ep.Addresses {
					desired[addr] = true
				}
			}
		}
	}

	names := make([]string, 0, len(r.Targets))
	for name := range r.Targets {
		names = append(names, name)
	}
	sort.Strings(names)

	var needsRequeue bool
	var errs []error

	for _, name := range names {
		tc := r.Targets[name]

		tctx, cancel := context.WithTimeout(ctx, targetCallTimeout)

		current, err := tc.List(tctx, r.DispatcherGroup)
		if err != nil {
			cancel()
			errs = append(errs, fmt.Errorf("target %s: list: %w", name, err))
			continue
		}

		toAdd, toRemove := dispatch.Diff(desired, current)

		// targetFaulted stops further writes to this target once it hits
		// the rate limit: they would fail too, so there is no point.
		targetFaulted := false
		for ip := range toAdd {
			if targetFaulted {
				break
			}
			uri := fmt.Sprintf("sip:%s:%d", ip, r.Port)
			if err := tc.Add(tctx, r.DispatcherGroup, uri); err != nil {
				if errors.Is(err, kamailio.ErrOngoingReload) {
					needsRequeue = true
					targetFaulted = true
					continue
				}
				errs = append(errs, fmt.Errorf("target %s: add %s: %w", name, uri, err))
			}
		}
		if !targetFaulted {
			for ip := range toRemove {
				uri := fmt.Sprintf("sip:%s:%d", ip, r.Port)
				if err := tc.Remove(tctx, r.DispatcherGroup, uri); err != nil {
					if errors.Is(err, kamailio.ErrOngoingReload) {
						needsRequeue = true
						break
					}
					errs = append(errs, fmt.Errorf("target %s: remove %s: %w", name, uri, err))
				}
			}
		}
		cancel()
	}

	if needsRequeue {
		if len(errs) > 0 {
			log.FromContext(ctx).Error(errors.Join(errs...), "target sync errors alongside an ongoing-reload fault; requeueing for the reload instead of fast backoff")
		}
		jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
		return ctrl.Result{RequeueAfter: 5*time.Second + jitter}, nil
	}

	if len(errs) > 0 {
		return ctrl.Result{}, errors.Join(errs...)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers the Reconciler to watch EndpointSlices labeled
// for r.FreeSwitchService.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&discoveryv1.EndpointSlice{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetLabels()[discoveryv1.LabelServiceName] == r.FreeSwitchService
		})).
		Complete(r)
}
