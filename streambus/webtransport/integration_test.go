package webtransport

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v2/streambus"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	wt "github.com/quic-go/webtransport-go"
)

func TestWebTransportReliableAndDatagramDelivery(t *testing.T) {
	serverTLS := testTLSConfig(t)
	bus := streambus.NewInMemory(streambus.Config{ReplayCapacity: 8})
	t.Cleanup(func() { _ = bus.Close() })

	mux := http.NewServeMux()
	h3 := &http3.Server{
		TLSConfig: serverTLS,
		QUICConfig: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
		EnableDatagrams: true,
		Handler:         mux,
	}
	server := &wt.Server{H3: h3}
	wt.ConfigureHTTP3Server(h3)
	mux.Handle("/stream", &Handler{Server: server, Bus: bus})

	address, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packetConn, err := net.ListenUDP("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(packetConn) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packetConn.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Error("WebTransport server did not stop")
		}
	})

	dialer := &wt.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test-only certificate
		QUICConfig: &quic.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}
	t.Cleanup(func() { _ = dialer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://localhost:%d/stream", packetConn.LocalAddr().(*net.UDPAddr).Port)
	response, session, err := dialer.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	t.Cleanup(func() { _ = session.CloseWithError(0, "") })

	control, err := session.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := streambus.WriteMessage(control, streambus.Message{
		Kind:           streambus.MessageSubscribe,
		ID:             1,
		SubscriptionID: 7,
		Options: streambus.SubscribeOptions{
			Topic: "ui:metrics", Buffer: 8, Overflow: streambus.LatestOnly,
		},
	}); err != nil {
		t.Fatal(err)
	}
	ack, err := streambus.NewReader(control, 4096).ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if ack.Kind != streambus.MessageAck || ack.SubscriptionID != 7 {
		t.Fatalf("subscribe ack = %#v", ack)
	}

	if _, err := bus.Publish(ctx, streambus.Frame{
		Topic: "ui:metrics", Payload: []byte("snapshot"), Reliability: streambus.Reliable,
	}); err != nil {
		t.Fatal(err)
	}
	reliable, err := session.AcceptUniStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reliableReader := streambus.NewReader(reliable, 4096)
	opened, err := reliableReader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if opened.Kind != streambus.MessageStreamOpen || opened.SubscriptionID != 7 {
		t.Fatalf("stream open = %#v", opened)
	}
	data, err := reliableReader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if data.Kind != streambus.MessageData || string(data.Frame.Payload) != "snapshot" {
		t.Fatalf("reliable data = %#v", data)
	}

	if _, err := bus.Publish(ctx, streambus.Frame{
		Topic: "ui:metrics", Payload: []byte("cursor"), Reliability: streambus.Unreliable,
	}); err != nil {
		t.Fatal(err)
	}
	datagram, err := session.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message, err := streambus.DecodeMessage(datagram)
	if err != nil {
		t.Fatal(err)
	}
	if message.Kind != streambus.MessageData || string(message.Frame.Payload) != "cursor" {
		t.Fatalf("datagram data = %#v", message)
	}
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{http3.NextProtoH3},
	}
}
