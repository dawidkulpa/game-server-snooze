package readiness

import (
	"context"
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
