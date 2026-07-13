package readiness

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestA2SProbeHandlesChallengeAndWaitsForInfoResponse(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 256)
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		n, client, err := server.ReadFromUDP(buffer)
		if err != nil {
			serverDone <- err
			return
		}
		if string(buffer[:n]) != string(a2sInfoQuery) {
			serverDone <- &unexpectedPacketError{got: append([]byte(nil), buffer[:n]...)}
			return
		}
		challenge := []byte{0xff, 0xff, 0xff, 0xff, 0x41, 0x01, 0x02, 0x03, 0x04}
		if _, err := server.WriteToUDP(challenge, client); err != nil {
			serverDone <- err
			return
		}
		n, _, err = server.ReadFromUDP(buffer)
		if err != nil {
			serverDone <- err
			return
		}
		wantLength := len(a2sInfoQuery) + 4
		if n != wantLength || string(buffer[:len(a2sInfoQuery)]) != string(a2sInfoQuery) || string(buffer[len(a2sInfoQuery):n]) != string(challenge[5:]) {
			serverDone <- &unexpectedPacketError{got: append([]byte(nil), buffer[:n]...)}
			return
		}
		_, err = server.WriteToUDP(validA2SInfoFixture(), client)
		serverDone <- err
	}()

	probe, err := NewA2SProbe(server.LocalAddr().String(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probe.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestA2SProbeSingleCheckHandlesChallenge(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 256)
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		_, client, err := server.ReadFromUDP(buffer)
		if err != nil {
			serverDone <- err
			return
		}
		challenge := []byte{0xff, 0xff, 0xff, 0xff, 0x41, 0x05, 0x06, 0x07, 0x08}
		if _, err := server.WriteToUDP(challenge, client); err != nil {
			serverDone <- err
			return
		}
		n, _, err := server.ReadFromUDP(buffer)
		if err != nil {
			serverDone <- err
			return
		}
		if n != len(a2sInfoQuery)+4 || string(buffer[n-4:n]) != string(challenge[5:]) {
			serverDone <- &unexpectedPacketError{got: append([]byte(nil), buffer[:n]...)}
			return
		}
		_, err = server.WriteToUDP(validA2SInfoFixture(), client)
		serverDone <- err
	}()

	probe, err := NewA2SProbe(server.LocalAddr().String(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probe.Probe(ctx); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestA2SProbeSingleCheckAcceptsDirectInfo(t *testing.T) {
	if err := runA2SProbeResponse(t, validA2SInfoFixture()); err != nil {
		t.Fatalf("Probe() direct info error = %v", err)
	}
}

func TestA2SProbeSingleCheckRejectsMalformedResponses(t *testing.T) {
	for name, response := range map[string][]byte{
		"short challenge": {0xff, 0xff, 0xff, 0xff, 0x41, 0x01},
		"invalid info":    {0xff, 0xff, 0xff, 0xff, 0x49, 0x11},
		"wrong header":    {0x00, 0xff, 0xff, 0xff, 0x49},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runA2SProbeResponse(t, response); err == nil {
				t.Fatal("Probe() accepted malformed response")
			}
		})
	}
}

func TestA2SProbeSingleCheckHonorsCanceledContext(t *testing.T) {
	probe, err := NewA2SProbe("127.0.0.1:9", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := probe.Probe(ctx); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() cancellation error = %v", err)
	}
}

func runA2SProbeResponse(t *testing.T, response []byte) error {
	t.Helper()
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 256)
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		_, client, err := server.ReadFromUDP(buffer)
		if err == nil {
			_, err = server.WriteToUDP(response, client)
		}
		serverDone <- err
	}()
	probe, err := NewA2SProbe(server.LocalAddr().String(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	probeErr := probe.Probe(ctx)
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	return probeErr
}

func validA2SInfoFixture() []byte {
	response := []byte{0xff, 0xff, 0xff, 0xff, 0x49, 0x11}
	for _, value := range []string{"Test Server", "map", "palworld", "Palworld"} {
		response = append(response, value...)
		response = append(response, 0)
	}
	response = append(response, 0x00, 0x00, 1, 16, 0, 'd', 'l', 0, 1)
	response = append(response, []byte("1.0.0\x00")...)
	return response
}

func TestValidA2SInfoRejectsHeaderOnlyAndMissingFields(t *testing.T) {
	if validA2SInfo([]byte{0xff, 0xff, 0xff, 0xff, 0x49}) {
		t.Fatal("accepted header-only A2S_INFO")
	}
	truncated := validA2SInfoFixture()
	truncated = truncated[:len(truncated)-1]
	if validA2SInfo(truncated) {
		t.Fatal("accepted A2S_INFO without terminated version")
	}
	if !validA2SInfo(validA2SInfoFixture()) {
		t.Fatal("rejected valid A2S_INFO fixture")
	}
}

func TestA2SProbeStopsWhenContextExpires(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	probe, err := NewA2SProbe(server.LocalAddr().String(), 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := probe.WaitReady(ctx); err == nil {
		t.Fatal("WaitReady() succeeded without an A2S response")
	}
}

type unexpectedPacketError struct{ got []byte }

func (err *unexpectedPacketError) Error() string { return "unexpected A2S packet" }
