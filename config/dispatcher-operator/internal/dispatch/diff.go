// Package dispatch holds pure logic for reconciling Kamailio dispatcher sets.
package dispatch

// IPSet is a set of addresses. The value is always true for members.
type IPSet map[string]bool

// Diff returns the addresses to add (desired minus current) and to remove
// (current minus desired). Both results are non-nil, possibly empty.
func Diff(desired, current IPSet) (toAdd, toRemove IPSet) {
	toAdd = IPSet{}
	toRemove = IPSet{}
	for ip := range desired {
		if !current[ip] {
			toAdd[ip] = true
		}
	}
	for ip := range current {
		if !desired[ip] {
			toRemove[ip] = true
		}
	}
	return toAdd, toRemove
}
