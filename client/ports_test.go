package client

import (
	"bytes"
	"context"
	"io"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/protocol"
)

func TestPortForwardRoundTripHalfCloseAndCleanup(t *testing.T) {
	c, _ := peer(t, nil)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := protocol.Port{Number: uint16(listener.Addr().(*net.TCPAddr).Port), Host: "127.0.0.1"}
	payload := bytes.Repeat([]byte("端口转发\x00"), 50000)
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		data, err := io.ReadAll(conn)
		if err == nil && !bytes.Equal(data, payload) {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			_, err = conn.Write(data)
		}
		serverDone <- err
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f, err := c.ForwardPort(ctx, port, ForwardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conn, err := net.DialTimeout("tcp4", f.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(conn)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("round trip: %d bytes, %v", len(got), err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel leaked forward")
	}
	if conn, err := net.DialTimeout("tcp4", f.Address(), time.Second); err == nil {
		conn.Close()
		t.Fatal("listener survived cancellation")
	}
}

func TestPortForwardDisconnectClosesActiveSockets(t *testing.T) {
	c, _ := peer(t, nil)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	f, err := c.ForwardPort(t.Context(), protocol.Port{Number: uint16(listener.Addr().(*net.TCPAddr).Port), Host: "127.0.0.1"}, ForwardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	remoteDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, err = conn.Write([]byte("ready"))
			if err == nil {
				_, err = io.Copy(io.Discard, conn)
			}
		}
		remoteDone <- err
	}()
	conn, err := net.DialTimeout("tcp4", f.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var ready [5]byte
	if _, err := io.ReadFull(conn, ready[:]); err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect leaked active forward")
	}
	if _, err := conn.Read(ready[:]); err == nil {
		t.Fatal("local connection survived backend close")
	}
	if err := <-remoteDone; err != nil {
		t.Fatal(err)
	}
}

func TestPortForwardRejectsInvalidDestinationsAndOccupiedLocalPort(t *testing.T) {
	c, _ := peer(t, nil)
	for _, port := range []protocol.Port{{Number: 80, Host: "http://bad"}, {Number: 80, Host: "bad host"}, {Host: "127.0.0.1"}} {
		if f, err := c.ForwardPort(t.Context(), port, ForwardOptions{}); err == nil {
			f.Close()
			t.Fatal("invalid destination accepted", port)
		}
		if stream, err := c.connectPort(t.Context(), port); err == nil {
			stream.Close()
			t.Fatal("server accepted invalid destination", port)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	number := uint16(listener.Addr().(*net.TCPAddr).Port)
	if f, err := c.ForwardPort(t.Context(), protocol.Port{Number: number, Host: "127.0.0.1"}, ForwardOptions{Port: number}); err == nil {
		f.Close()
		t.Fatal("occupied local port accepted")
	}
	ports, err := c.ListeningPorts(t.Context())
	if err != nil {
		t.Skip("platform discovery unavailable:", err)
	}
	if !slices.ContainsFunc(ports, func(p protocol.Port) bool {
		return p.Number == number && p.Host == "127.0.0.1" && p.PID != 0 && p.Process != ""
	}) {
		t.Fatalf("listener %d was not discovered: %+v", number, ports)
	}
}

func TestPortForwardIPv6Listener(t *testing.T) {
	c, _ := peer(t, nil)
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			conn.Write([]byte("IPv6"))
		}
	}()
	f, err := c.ForwardPort(t.Context(), protocol.Port{Number: uint16(listener.Addr().(*net.TCPAddr).Port), Host: "127.0.0.1"}, ForwardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	conn, err := net.DialTimeout("tcp4", f.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var data [4]byte
	if _, err := io.ReadFull(conn, data[:]); err != nil || string(data[:]) != "IPv6" {
		t.Fatal(string(data[:]), err)
	}
}

func TestPortForwardHostAddressAndAllInterfaces(t *testing.T) {
	c, _ := peer(t, nil)
	listener, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Skip(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			conn.Write([]byte("host"))
		}
	}()
	f, err := c.ForwardPort(t.Context(), protocol.Port{Number: uint16(listener.Addr().(*net.TCPAddr).Port), Host: "127.0.0.2"}, ForwardOptions{AllInterfaces: true})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	host, port, err := net.SplitHostPort(f.Address())
	if err != nil || host != "0.0.0.0" {
		t.Fatal("all-interfaces listener was not bound", f.Address(), err)
	}
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var data [4]byte
	if _, err := io.ReadFull(conn, data[:]); err != nil || string(data[:]) != "host" {
		t.Fatal(string(data[:]), err)
	}
}
