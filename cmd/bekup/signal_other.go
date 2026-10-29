//go:build !unix

package main

// reloadSignals: there is no SIGHUP here; reloads come from the admin UI
// only (D-200).
func reloadSignals() (<-chan struct{}, func()) { return nil, func() {} }
