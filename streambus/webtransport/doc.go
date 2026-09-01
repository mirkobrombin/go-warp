// Package webtransport exposes StreamBus over HTTP/3 WebTransport sessions.
// Reliable subscriptions receive independent unidirectional streams, while
// unreliable subscriptions use congestion-controlled datagrams.
package webtransport
