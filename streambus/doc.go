// Package streambus provides bounded, resumable streams with explicit delivery
// semantics for high-volume application data.
//
// Unlike watchbus, StreamBus never hides its overload policy. Each subscriber
// chooses whether a full queue blocks the publisher, drops the newest or oldest
// frame, or retains only the latest state.
package streambus
