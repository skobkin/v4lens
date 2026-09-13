// Package probe contains build-tagged real-device probes and decoder
// benchmarks used during the Phase 0 technical validation.
//
// Nothing in this package is part of the application build. It is excluded
// from normal builds, tests, vet and lint, and it never touches hardware
// unless explicitly compiled and run with:
//
//	go test -tags probe -run TestDeviceProbe -v ./internal/probe
//	go test -tags probe -bench . ./internal/probe
//
// The device probe is state-preserving: it snapshots input, format, frame
// interval and every control value it touches, restores them best-effort and
// reports any restoration failure.
package probe
