// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package p2p

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"io"
	"math/rand"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/internal/testlog"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enr"
	"github.com/ParallaxProtocol/parallax/v2/p2p/rlpx"
	"github.com/ParallaxProtocol/parallax/v2/util/mclock"
)

type testTransport struct {
	*rlpxTransport
	rpub     *ecdsa.PublicKey
	closeErr error
}

func newTestTransport(rpub *ecdsa.PublicKey, fd net.Conn, dialDest *ecdsa.PublicKey) transport {
	wrapped := newRLPX(fd, dialDest).(*rlpxTransport)
	wrapped.conn.InitWithSecrets(rlpx.Secrets{
		AES:        make([]byte, 16),
		MAC:        make([]byte, 16),
		EgressMAC:  sha256.New(),
		IngressMAC: sha256.New(),
	})
	return &testTransport{rpub: rpub, rlpxTransport: wrapped}
}

func (c *testTransport) doEncHandshake(prv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error) {
	return c.rpub, nil
}

func (c *testTransport) doProtoHandshake(our *protoHandshake) (*protoHandshake, error) {
	pubkey := crypto.FromECDSAPub(c.rpub)[1:]
	return &protoHandshake{ID: pubkey, Name: "test"}, nil
}

func (c *testTransport) close(err error) {
	c.conn.Close()
	c.closeErr = err
}

func startTestServer(t *testing.T, remoteKey *ecdsa.PublicKey, pf func(*Peer)) *Server {
	config := Config{
		Name:        "test",
		MaxPeers:    10,
		ListenAddr:  "127.0.0.1:0",
		NoDiscovery: true,
		PrivateKey:  newkey(),
		Logger:      testlog.Logger(t, logging.LvlTrace),
	}
	server := &Server{
		Config:      config,
		newPeerHook: pf,
		newTransport: func(fd net.Conn, dialDest *ecdsa.PublicKey) transport {
			return newTestTransport(remoteKey, fd, dialDest)
		},
	}
	if err := server.Start(); err != nil {
		t.Fatalf("Could not start server: %v", err)
	}
	return server
}

func TestServerListen(t *testing.T) {
	// start the test server
	connected := make(chan *Peer)
	remid := &newkey().PublicKey
	srv := startTestServer(t, remid, func(p *Peer) {
		if p.ID() != enode.PubkeyToIDV4(remid) {
			t.Error("peer func called with wrong node id")
		}
		connected <- p
	})
	defer close(connected)
	defer srv.Stop()

	// dial the test server. Send any non-0xA0 byte so the PIP-0006
	// listener peek classifies as legacy and proceeds.
	conn, err := net.DialTimeout("tcp", srv.ListenAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("could not dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0x01}); err != nil {
		t.Fatalf("could not write peek byte: %v", err)
	}

	select {
	case peer := <-connected:
		if peer.LocalAddr().String() != conn.RemoteAddr().String() {
			t.Errorf("peer started with wrong conn: got %v, want %v",
				peer.LocalAddr(), conn.RemoteAddr())
		}
		peers := srv.Peers()
		if !reflect.DeepEqual(peers, []*Peer{peer}) {
			t.Errorf("Peers mismatch: got %v, want %v", peers, []*Peer{peer})
		}
	case <-time.After(1 * time.Second):
		t.Error("server did not accept within one second")
	}
}

func TestServerDial(t *testing.T) {
	// run a one-shot TCP server to handle the connection.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not setup listener: %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	// start the server
	connected := make(chan *Peer)
	remid := &newkey().PublicKey
	srv := startTestServer(t, remid, func(p *Peer) { connected <- p })
	defer close(connected)
	defer srv.Stop()

	// tell the server to connect
	tcpAddr := listener.Addr().(*net.TCPAddr)
	node := enode.NewV4(remid, tcpAddr.IP, tcpAddr.Port, 0)
	srv.AddPeer(node)

	select {
	case conn := <-accepted:
		defer conn.Close()

		select {
		case peer := <-connected:
			if peer.ID() != enode.PubkeyToIDV4(remid) {
				t.Errorf("peer has wrong id")
			}
			if peer.Name() != "test" {
				t.Errorf("peer has wrong name")
			}
			if peer.RemoteAddr().String() != conn.LocalAddr().String() {
				t.Errorf("peer started with wrong conn: got %v, want %v",
					peer.RemoteAddr(), conn.LocalAddr())
			}
			peers := srv.Peers()
			if !reflect.DeepEqual(peers, []*Peer{peer}) {
				t.Errorf("Peers mismatch: got %v, want %v", peers, []*Peer{peer})
			}

			// Test AddTrustedPeer/RemoveTrustedPeer and changing Trusted flags
			// Particularly for race conditions on changing the flag state.
			if peer := srv.Peers()[0]; peer.Info().Network.Trusted {
				t.Errorf("peer is trusted prematurely: %v", peer)
			}
			done := make(chan bool)
			go func() {
				srv.AddTrustedPeer(node)
				if peer := srv.Peers()[0]; !peer.Info().Network.Trusted {
					t.Errorf("peer is not trusted after AddTrustedPeer: %v", peer)
				}
				srv.RemoveTrustedPeer(node)
				if peer := srv.Peers()[0]; peer.Info().Network.Trusted {
					t.Errorf("peer is trusted after RemoveTrustedPeer: %v", peer)
				}
				done <- true
			}()
			// Trigger potential race conditions
			peer = srv.Peers()[0]
			_ = peer.Inbound()
			_ = peer.Info()
			<-done
		case <-time.After(1 * time.Second):
			t.Error("server did not launch peer within one second")
		}

	case <-time.After(1 * time.Second):
		t.Error("server did not connect within one second")
	}
}

// This test checks that RemovePeer disconnects the peer if it is connected.
func TestServerRemovePeerDisconnect(t *testing.T) {
	srv1 := &Server{Config: Config{
		PrivateKey:  newkey(),
		MaxPeers:    1,
		NoDiscovery: true,
		Logger:      testlog.Logger(t, logging.LvlTrace).New("server", "1"),
	}}
	srv2 := &Server{Config: Config{
		PrivateKey:  newkey(),
		MaxPeers:    1,
		NoDiscovery: true,
		NoDial:      true,
		ListenAddr:  "127.0.0.1:0",
		Logger:      testlog.Logger(t, logging.LvlTrace).New("server", "2"),
	}}
	srv1.Start()
	defer srv1.Stop()
	srv2.Start()
	defer srv2.Stop()

	if !syncAddPeer(srv1, srv2.Self()) {
		t.Fatal("peer not connected")
	}
	srv1.RemovePeer(srv2.Self())
	if srv1.PeerCount() > 0 {
		t.Fatal("removed peer still connected")
	}
}

// This test checks that connections are disconnected just after the encryption handshake
// when the server is at capacity. Trusted connections should still be accepted.
func TestServerAtCap(t *testing.T) {
	trustedNode := newkey()
	trustedID := enode.PubkeyToIDV4(&trustedNode.PublicKey)
	srv := &Server{
		Config: Config{
			PrivateKey:   newkey(),
			MaxPeers:     10,
			NoDial:       true,
			NoDiscovery:  true,
			TrustedNodes: []*enode.Node{newNode(trustedID, "")},
			Logger:       testlog.Logger(t, logging.LvlTrace),
		},
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("could not start: %v", err)
	}
	defer srv.Stop()

	newconn := func(id enode.ID) *conn {
		fd, _ := net.Pipe()
		tx := newTestTransport(&trustedNode.PublicKey, fd, nil)
		node := enode.SignNull(new(enr.Record), id)
		return &conn{fd: fd, transport: tx, flags: inboundConn, node: node, cont: make(chan error)}
	}

	// Inject a few connections to fill up the peer set.
	for i := 0; i < 10; i++ {
		c := newconn(randomID())
		if err := srv.checkpoint(c, srv.checkpointAddPeer); err != nil {
			t.Fatalf("could not add conn %d: %v", i, err)
		}
	}
	// Try inserting a non-trusted connection.
	anotherID := randomID()
	c := newconn(anotherID)
	if err := srv.checkpoint(c, srv.checkpointPostHandshake); err != DiscTooManyPeers {
		t.Error("wrong error for insert:", err)
	}
	// Try inserting a trusted connection.
	c = newconn(trustedID)
	if err := srv.checkpoint(c, srv.checkpointPostHandshake); err != nil {
		t.Error("unexpected error for trusted conn @posthandshake:", err)
	}
	if !c.is(trustedConn) {
		t.Error("Server did not set trusted flag")
	}

	// Remove from trusted set and try again
	srv.RemoveTrustedPeer(newNode(trustedID, ""))
	c = newconn(trustedID)
	if err := srv.checkpoint(c, srv.checkpointPostHandshake); err != DiscTooManyPeers {
		t.Error("wrong error for insert:", err)
	}

	// Add anotherID to trusted set and try again
	srv.AddTrustedPeer(newNode(anotherID, ""))
	c = newconn(anotherID)
	if err := srv.checkpoint(c, srv.checkpointPostHandshake); err != nil {
		t.Error("unexpected error for trusted conn @posthandshake:", err)
	}
	if !c.is(trustedConn) {
		t.Error("Server did not set trusted flag")
	}
}

func TestServerPeerLimits(t *testing.T) {
	srvkey := newkey()
	clientkey := newkey()
	clientnode := enode.NewV4(&clientkey.PublicKey, nil, 0, 0)

	var tp = &setupTransport{
		pubkey: &clientkey.PublicKey,
		phs: protoHandshake{
			ID: crypto.FromECDSAPub(&clientkey.PublicKey)[1:],
			// Force "DiscUselessPeer" due to unmatching caps
			// Caps: []Cap{discard.cap()},
		},
	}

	srv := &Server{
		Config: Config{
			PrivateKey:  srvkey,
			MaxPeers:    0,
			NoDial:      true,
			NoDiscovery: true,
			Protocols:   []Protocol{discard},
			Logger:      testlog.Logger(t, logging.LvlTrace),
		},
		newTransport: func(fd net.Conn, dialDest *ecdsa.PublicKey) transport { return tp },
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("couldn't start server: %v", err)
	}
	defer srv.Stop()

	// Check that server is full (MaxPeers=0)
	flags := dynDialedConn
	dialDest := clientnode
	conn, _ := net.Pipe()
	srv.SetupConn(conn, flags, dialDest)
	if tp.closeErr != DiscTooManyPeers {
		t.Errorf("unexpected close error: %q", tp.closeErr)
	}
	conn.Close()

	srv.AddTrustedPeer(clientnode)

	// Check that server allows a trusted peer despite being full.
	conn, _ = net.Pipe()
	srv.SetupConn(conn, flags, dialDest)
	if tp.closeErr == DiscTooManyPeers {
		t.Errorf("failed to bypass MaxPeers with trusted node: %q", tp.closeErr)
	}

	if tp.closeErr != DiscUselessPeer {
		t.Errorf("unexpected close error: %q", tp.closeErr)
	}
	conn.Close()

	srv.RemoveTrustedPeer(clientnode)

	// Check that server is full again.
	conn, _ = net.Pipe()
	srv.SetupConn(conn, flags, dialDest)
	if tp.closeErr != DiscTooManyPeers {
		t.Errorf("unexpected close error: %q", tp.closeErr)
	}
	conn.Close()
}

func TestServerSetupConn(t *testing.T) {
	var (
		clientkey, srvkey = newkey(), newkey()
		clientpub         = &clientkey.PublicKey
		srvpub            = &srvkey.PublicKey
		fooErr            = errors.New("foo")
		readErr           = errors.New("read error")
	)
	tests := []struct {
		dontstart bool
		tt        *setupTransport
		flags     connFlag
		dialDest  *enode.Node

		wantCloseErr error
		wantCalls    string
	}{
		{
			dontstart:    true,
			tt:           &setupTransport{pubkey: clientpub},
			wantCalls:    "close,",
			wantCloseErr: errServerStopped,
		},
		{
			tt:           &setupTransport{pubkey: clientpub, encHandshakeErr: readErr},
			flags:        inboundConn,
			wantCalls:    "doEncHandshake,close,",
			wantCloseErr: readErr,
		},
		{
			tt:           &setupTransport{pubkey: clientpub, phs: protoHandshake{ID: randomID().Bytes()}},
			dialDest:     enode.NewV4(clientpub, nil, 0, 0),
			flags:        dynDialedConn,
			wantCalls:    "doEncHandshake,doProtoHandshake,close,",
			wantCloseErr: DiscUnexpectedIdentity,
		},
		{
			tt:           &setupTransport{pubkey: clientpub, protoHandshakeErr: fooErr},
			dialDest:     enode.NewV4(clientpub, nil, 0, 0),
			flags:        dynDialedConn,
			wantCalls:    "doEncHandshake,doProtoHandshake,close,",
			wantCloseErr: fooErr,
		},
		{
			tt:           &setupTransport{pubkey: srvpub, phs: protoHandshake{ID: crypto.FromECDSAPub(srvpub)[1:]}},
			flags:        inboundConn,
			wantCalls:    "doEncHandshake,close,",
			wantCloseErr: DiscSelf,
		},
		{
			tt:           &setupTransport{pubkey: clientpub, phs: protoHandshake{ID: crypto.FromECDSAPub(clientpub)[1:]}},
			flags:        inboundConn,
			wantCalls:    "doEncHandshake,doProtoHandshake,close,",
			wantCloseErr: DiscUselessPeer,
		},
	}

	for i, test := range tests {
		t.Run(test.wantCalls, func(t *testing.T) {
			cfg := Config{
				PrivateKey:  srvkey,
				MaxPeers:    10,
				NoDial:      true,
				NoDiscovery: true,
				Protocols:   []Protocol{discard},
				Logger:      testlog.Logger(t, logging.LvlTrace),
			}
			srv := &Server{
				Config:       cfg,
				newTransport: func(fd net.Conn, dialDest *ecdsa.PublicKey) transport { return test.tt },
				log:          cfg.Logger,
			}
			if !test.dontstart {
				if err := srv.Start(); err != nil {
					t.Fatalf("couldn't start server: %v", err)
				}
				defer srv.Stop()
			}
			p1, _ := net.Pipe()
			srv.SetupConn(p1, test.flags, test.dialDest)
			if !errors.Is(test.tt.closeErr, test.wantCloseErr) {
				t.Errorf("test %d: close error mismatch: got %q, want %q", i, test.tt.closeErr, test.wantCloseErr)
			}
			if test.tt.calls != test.wantCalls {
				t.Errorf("test %d: calls mismatch: got %q, want %q", i, test.tt.calls, test.wantCalls)
			}
		})
	}
}

type setupTransport struct {
	pubkey            *ecdsa.PublicKey
	encHandshakeErr   error
	phs               protoHandshake
	protoHandshakeErr error

	calls    string
	closeErr error
}

func (c *setupTransport) doEncHandshake(prv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error) {
	c.calls += "doEncHandshake,"
	return c.pubkey, c.encHandshakeErr
}

func (c *setupTransport) doProtoHandshake(our *protoHandshake) (*protoHandshake, error) {
	c.calls += "doProtoHandshake,"
	if c.protoHandshakeErr != nil {
		return nil, c.protoHandshakeErr
	}
	return &c.phs, nil
}
func (c *setupTransport) close(err error) {
	c.calls += "close,"
	c.closeErr = err
}

// setupConn shouldn't write to/read from the connection.
func (c *setupTransport) WriteMsg(Msg) error {
	panic("WriteMsg called on setupTransport")
}
func (c *setupTransport) ReadMsg() (Msg, error) {
	panic("ReadMsg called on setupTransport")
}

func newkey() *ecdsa.PrivateKey {
	key, err := crypto.GenerateKey()
	if err != nil {
		panic("couldn't generate key: " + err.Error())
	}
	return key
}

func randomID() (id enode.ID) {
	for i := range id {
		id[i] = byte(rand.Intn(255))
	}
	return id
}

// TestSetupConnInboundProgressLifecycle — confirms the
// inboundProgress register/unregister wiring in setupConn:
//
//   - on inbound setup, the NodeID is registered with the dial
//     scheduler before checkpointPostHandshake;
//   - the registration is cleared on every exit path (here:
//     protoHandshake failure mid-stream).
//
// Verified by checking the dialsched's inboundProgress map directly
// after SetupConn returns. Pairs with the dial-scheduler unit tests
// that exercise checkDial's new branch.
func TestSetupConnInboundProgressLifecycle(t *testing.T) {
	t.Parallel()

	clientkey := newkey()
	srvkey := newkey()
	clientpub := &clientkey.PublicKey
	tt := &setupTransport{
		pubkey:            clientpub,
		phs:               protoHandshake{ID: crypto.FromECDSAPub(clientpub)[1:]},
		protoHandshakeErr: errors.New("fail at proto handshake"),
	}
	cfg := Config{
		PrivateKey:  srvkey,
		MaxPeers:    10,
		NoDial:      true,
		NoDiscovery: true,
		Protocols:   []Protocol{discard},
		Logger:      testlog.Logger(t, logging.LvlTrace),
	}
	srv := &Server{
		Config:       cfg,
		newTransport: func(fd net.Conn, _ *ecdsa.PublicKey) transport { return tt },
		log:          cfg.Logger,
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	clientNode := nodeFromConn(clientpub, &fakeConn{remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}})
	id := clientNode.ID()

	a, b := net.Pipe()
	defer b.Close()
	if err := srv.SetupConn(a, inboundConn, nil); err == nil {
		t.Fatal("SetupConn returned nil; want proto-handshake error")
	}

	// After SetupConn returns, the deferred inboundProgressEnd must
	// have run. Probe dialsched via checkDial: a candidate carrying
	// our just-vacated NodeID must NOT be blocked by errInboundProgress.
	probe := newNode(id, "1.2.3.4:32110")
	gotErr := make(chan error, 1)
	go func() {
		// checkDial runs on the dial loop goroutine. We funnel a
		// query in via a synthetic dial-update helper.
		gotErr <- srv.dialsched.probeCheckDial(probe)
	}()
	select {
	case err := <-gotErr:
		if errors.Is(err, errInboundProgress) {
			t.Fatalf("inboundProgress leaked after SetupConn returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probeCheckDial did not return")
	}
}

// fakeConn is a net.Conn stub that returns a fixed RemoteAddr. Used
// to coax nodeFromConn into producing the same ID setupConn would.
type fakeConn struct {
	net.Conn
	remote net.Addr
}

func (f *fakeConn) RemoteAddr() net.Addr { return f.remote }

// This test checks that inbound connections are throttled by IP.
func TestServerInboundThrottle(t *testing.T) {
	const timeout = 5 * time.Second
	newTransportCalled := make(chan struct{})
	srv := &Server{
		Config: Config{
			PrivateKey:  newkey(),
			ListenAddr:  "127.0.0.1:0",
			MaxPeers:    10,
			NoDial:      true,
			NoDiscovery: true,
			Protocols:   []Protocol{discard},
			Logger:      testlog.Logger(t, logging.LvlTrace),
		},
		newTransport: func(fd net.Conn, dialDest *ecdsa.PublicKey) transport {
			newTransportCalled <- struct{}{}
			return newRLPX(fd, dialDest)
		},
		listenFunc: func(network, laddr string) (net.Listener, error) {
			fakeAddr := &net.TCPAddr{IP: net.IP{95, 33, 21, 2}, Port: 4444}
			return listenFakeAddr(network, laddr, fakeAddr)
		},
	}
	if err := srv.Start(); err != nil {
		t.Fatal("can't start: ", err)
	}
	defer srv.Stop()

	// Dial the test server up to maxInboundConnAttemptsPerIP times.
	// Each one should reach newTransport — the throttle only kicks in
	// once that count is exceeded.
	//
	// Send a legacy-RLPx-shaped first byte (0xf9, the RLP list-length
	// prefix for an ECIES auth packet) so the PIP-0006 peek dispatcher
	// classifies each connection as legacy and proceeds into
	// srv.newTransport.
	for i := 0; i < maxInboundConnAttemptsPerIP; i++ {
		conn, err := net.DialTimeout("tcp", srv.ListenAddr, timeout)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		if _, err := conn.Write([]byte{0xf9}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		select {
		case <-newTransportCalled:
			// OK — connection reached the handshake stage.
		case <-time.After(timeout):
			t.Fatalf("newTransport not called for attempt %d (within rate limit)", i)
		}
		conn.Close()
	}

	// One more dial — this exceeds maxInboundConnAttemptsPerIP within
	// the throttle window. Server should close the connection
	// immediately (pre-handshake).
	connClosed := make(chan struct{}, 1)
	conn, err := net.DialTimeout("tcp", srv.ListenAddr, timeout)
	if err != nil {
		t.Fatalf("could not dial throttled attempt: %v", err)
	}
	defer conn.Close()
	go func() {
		conn.SetDeadline(time.Now().Add(timeout))
		buf := make([]byte, 10)
		if n, err := conn.Read(buf); err != io.EOF || n != 0 {
			t.Errorf("expected io.EOF and n == 0, got error %q and n == %d", err, n)
		}
		connClosed <- struct{}{}
	}()
	select {
	case <-connClosed:
		// OK
	case <-newTransportCalled:
		t.Errorf("newTransport called for over-limit attempt (cap=%d)", maxInboundConnAttemptsPerIP)
	case <-time.After(timeout):
		t.Error("connection not closed within timeout")
	}
}

func listenFakeAddr(network, laddr string, remoteAddr net.Addr) (net.Listener, error) {
	l, err := net.Listen(network, laddr)
	if err == nil {
		l = &fakeAddrListener{l, remoteAddr}
	}
	return l, err
}

// fakeAddrListener is a listener that creates connections with a mocked remote address.
type fakeAddrListener struct {
	net.Listener
	remoteAddr net.Addr
}

type fakeAddrConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (l *fakeAddrListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &fakeAddrConn{c, l.remoteAddr}, nil
}

func (c *fakeAddrConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func syncAddPeer(srv *Server, node *enode.Node) bool {
	var (
		ch      = make(chan *PeerEvent)
		sub     = srv.SubscribeEvents(ch)
		timeout = time.After(2 * time.Second)
	)
	defer sub.Unsubscribe()
	srv.AddPeer(node)
	for {
		select {
		case ev := <-ch:
			if ev.Type == PeerEventTypeAdd && ev.Peer == node.ID() {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

// newSelfEndpointServer builds a minimal Server whose localnode
// advertises (ip, port). Used by the v2 self-connect tests so they
// can drive IsSelfEndpoint without standing up a full Start() path.
func newSelfEndpointServer(t *testing.T, ip net.IP, port int) *Server {
	t.Helper()
	db, err := enode.OpenDB("")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(db.Close)
	ln := enode.NewLocalNode(db, newkey())
	if ip != nil {
		ln.SetStaticIP(ip)
	}
	if port != 0 {
		ln.Set(enr.TCP(port))
	}
	return &Server{
		// A non-empty ListenAddr models a node that intends to
		// listen: in the port==0 cases that means the listener just
		// hasn't published its port yet (startup window), which is
		// what the pre-listen self-endpoint tests exercise. The
		// "configured not to listen" case sets ListenAddr back to ""
		// explicitly.
		Config:    Config{MaxPeers: 1, ListenAddr: "127.0.0.1:0"},
		localnode: ln,
		log:       testlog.Logger(t, logging.LvlTrace),
	}
}

func TestIsSelfEndpoint(t *testing.T) {
	const selfPort = 30303
	selfIP := net.ParseIP("1.2.3.4")
	srv := newSelfEndpointServer(t, selfIP, selfPort)

	tests := []struct {
		name string
		addr *net.TCPAddr
		want bool
	}{
		{"nil addr", nil, false},
		{"exact match", &net.TCPAddr{IP: selfIP, Port: selfPort}, true},
		{"port mismatch", &net.TCPAddr{IP: selfIP, Port: selfPort + 1}, false},
		{"ip mismatch", &net.TCPAddr{IP: net.ParseIP("5.6.7.8"), Port: selfPort}, false},
		{"v4-mapped v6", &net.TCPAddr{IP: net.ParseIP("::ffff:1.2.3.4"), Port: selfPort}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := srv.IsSelfEndpoint(tc.addr); got != tc.want {
				t.Fatalf("IsSelfEndpoint(%v) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}

	t.Run("nil localnode", func(t *testing.T) {
		bare := &Server{log: testlog.Logger(t, logging.LvlTrace)}
		if bare.IsSelfEndpoint(&net.TCPAddr{IP: selfIP, Port: selfPort}) {
			t.Fatal("IsSelfEndpoint must return false when localnode is nil")
		}
	})

	t.Run("port unset external IP", func(t *testing.T) {
		// Bootstrap state: setupLocalNode has run (IP fallback)
		// but setupListening has not yet published the TCP port.
		// Dialing some external (IP, port) must NOT be classified
		// as self — we genuinely don't know our port yet, and the
		// remote IP can't be us.
		early := newSelfEndpointServer(t, selfIP, 0)
		if early.IsSelfEndpoint(&net.TCPAddr{IP: selfIP, Port: selfPort}) {
			t.Fatal("IsSelfEndpoint must return false on external IP while localnode TCP port is 0")
		}
	})

	t.Run("port unset loopback", func(t *testing.T) {
		// Before the listener publishes a port, a loopback dial is
		// always self — no legitimate caller wants to dial 127.0.0.1
		// during the Start() window. Closes the prior gap where
		// admin RPC / replayAnchors could have raced setupListening.
		early := newSelfEndpointServer(t, selfIP, 0)
		if !early.IsSelfEndpoint(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}) {
			t.Fatal("IsSelfEndpoint must catch loopback dial during pre-listen window")
		}
		if !early.IsSelfEndpoint(&net.TCPAddr{IP: net.IPv6loopback, Port: 1234}) {
			t.Fatal("IsSelfEndpoint must catch IPv6 loopback dial during pre-listen window")
		}
	})

	t.Run("not listening loopback", func(t *testing.T) {
		// A node configured not to listen (ListenAddr == "") never
		// publishes a TCP port, so selfPort stays 0 for the whole
		// process lifetime. A loopback dial then reaches a co-hosted
		// node, not a hairpin into a listener we don't have, so it
		// must NOT be classified as self. Without this a non-listening
		// node could never dial a co-hosted peer over 127.0.0.1.
		noListen := newSelfEndpointServer(t, selfIP, 0)
		noListen.ListenAddr = ""
		if noListen.IsSelfEndpoint(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}) {
			t.Fatal("IsSelfEndpoint must not treat a loopback dial as self when the node does not listen")
		}
		if noListen.IsSelfEndpoint(&net.TCPAddr{IP: net.IPv6loopback, Port: 1234}) {
			t.Fatal("IsSelfEndpoint must not treat an IPv6 loopback dial as self when the node does not listen")
		}
	})

	t.Run("loopback bootstrap", func(t *testing.T) {
		// Before NAT discovery, localnode.IP is the 127.0.0.1
		// fallback. Dialing your own loopback at your listen port
		// is a self-connect and should be classified as such.
		loop := newSelfEndpointServer(t, net.IP{127, 0, 0, 1}, selfPort)
		if !loop.IsSelfEndpoint(&net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: selfPort}) {
			t.Fatal("IsSelfEndpoint must catch loopback self-dial")
		}
	})

	t.Run("loopback to listen port with external IP", func(t *testing.T) {
		// Once the listener is bound on an external IP, a loopback
		// dial to the listen port still hairpins to our own listener
		// (kernel routes 127.0.0.1 -> the local socket regardless of
		// the bind interface). Must be flagged as self.
		ext := newSelfEndpointServer(t, selfIP, selfPort)
		if !ext.IsSelfEndpoint(&net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: selfPort}) {
			t.Fatal("IsSelfEndpoint must catch loopback dial to our listen port")
		}
		if !ext.IsSelfEndpoint(&net.TCPAddr{IP: net.IPv6loopback, Port: selfPort}) {
			t.Fatal("IsSelfEndpoint must catch v6-loopback dial to our listen port")
		}
		// Loopback at a *different* port is not self.
		if ext.IsSelfEndpoint(&net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: selfPort + 1}) {
			t.Fatal("IsSelfEndpoint false-positive on loopback at non-listen port")
		}
	})
}

func TestDialV2RejectsSelfEndpoint(t *testing.T) {
	srv := startTestServer(t, &newkey().PublicKey, nil)
	defer srv.Stop()

	self := srv.localnode.Node()
	if self.TCP() == 0 {
		t.Fatalf("test server did not publish TCP port to localnode")
	}
	addr := &net.TCPAddr{IP: self.IP(), Port: self.TCP()}

	err := srv.DialV2(testNetAddr(t, addr))
	if err == nil {
		t.Fatalf("DialV2(%v) returned nil error", addr)
	}
	if !errors.Is(err, errV2DialSelf) {
		t.Fatalf("DialV2 error = %v, want wrapping errV2DialSelf", err)
	}
	for _, p := range srv.Peers() {
		if pra, ok := p.RemoteAddr().(*net.TCPAddr); ok && pra.IP.Equal(addr.IP) && pra.Port == addr.Port {
			t.Fatalf("self-dial produced a peer: %v", pra)
		}
	}
	srv.v2DialRecentMu.Lock()
	_, recorded := srv.v2DialRecent[addr.String()]
	srv.v2DialRecentMu.Unlock()
	if !recorded {
		t.Fatalf("v2DialRecent did not record the rejected dial; cooldown semantics depend on it")
	}
}

func TestPostHandshakeChecksRejectsV2Self(t *testing.T) {
	const selfPort = 30303
	selfIP := net.ParseIP("1.2.3.4")
	srv := newSelfEndpointServer(t, selfIP, selfPort)

	// Build a fake net.Conn whose RemoteAddr returns the self
	// endpoint. The kernel-level remote of an outbound v2 dial
	// is the dial target; if that equals our advertised endpoint
	// the connection is a hairpin and must be rejected as DiscSelf.
	pipe, _ := net.Pipe()
	defer pipe.Close()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: selfIP, Port: selfPort}}

	// c.node.ID must differ from srv.localnode.ID() so the existing
	// node.ID-keyed self-check doesn't mask the v2 endpoint check.
	other := enode.SignNull(new(enr.Record), randomID())
	c := &conn{
		fd:        fake,
		transport: &v2Transport{},
		node:      other,
	}

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, 0, c)
	if !errors.Is(err, DiscSelf) {
		t.Fatalf("postHandshakeChecks err = %v, want DiscSelf", err)
	}
}

// TestHelloNonceRandomized — startTestServer goes through Start which
// calls setupLocalNode. The resulting helloNonce must be non-zero on
// all but vanishingly rare draws and must differ across two
// independently-started servers (collision probability 2^-64).
func TestHelloNonceRandomized(t *testing.T) {
	srvA := startTestServer(t, &newkey().PublicKey, nil)
	defer srvA.Stop()
	srvB := startTestServer(t, &newkey().PublicKey, nil)
	defer srvB.Stop()

	if srvA.HelloNonce() == 0 {
		t.Fatal("server A helloNonce is zero; init likely skipped")
	}
	if srvB.HelloNonce() == 0 {
		t.Fatal("server B helloNonce is zero; init likely skipped")
	}
	if srvA.HelloNonce() == srvB.HelloNonce() {
		t.Fatalf("two servers got the same helloNonce %d; randomness broken", srvA.HelloNonce())
	}
}

// TestHelloNonceStableAfterStart — once the server is up the nonce
// must be immutable. Multiple reads return the same value and no
// background goroutine rotates it.
func TestHelloNonceStableAfterStart(t *testing.T) {
	srv := startTestServer(t, &newkey().PublicKey, nil)
	defer srv.Stop()

	first := srv.HelloNonce()
	for i := 0; i < 100; i++ {
		if got := srv.HelloNonce(); got != first {
			t.Fatalf("helloNonce changed mid-run: was %d, got %d on read %d", first, got, i)
		}
	}
}

// TestInitHelloNonce — direct test of the init helper. Calling on a
// bare Server populates the field with non-zero bytes drawn from
// crypto/rand.
func TestInitHelloNonce(t *testing.T) {
	var srv Server
	if err := srv.initHelloNonce(); err != nil {
		t.Fatalf("initHelloNonce: %v", err)
	}
	if srv.helloNonce == 0 {
		t.Fatal("helloNonce still zero after init")
	}
}

// fakePortLookup is a test stub for PeerListenPortLookup. Maps
// enode.ID -> disclosed listen port; missing entries return ok=false.
type fakePortLookup struct {
	ports map[enode.ID]uint16
}

func (f *fakePortLookup) PeerListenPort(id enode.ID) (uint16, bool) {
	p, ok := f.ports[id]
	if !ok {
		return 0, false
	}
	return p, true
}

// makeFakePeer constructs a Peer with the given (ID, RemoteAddr)
// and inbound flag. The conn flags carry inbound state so
// p.rw.is(inboundConn) returns the requested value.
func makeFakePeer(t *testing.T, id enode.ID, remote *net.TCPAddr, inbound bool) *Peer {
	t.Helper()
	pipe, _ := net.Pipe()
	fake := &fakeAddrConn{Conn: pipe, remoteAddr: remote}
	t.Cleanup(func() { _ = fake.Close() })
	p := NewPeerForTest(id, "fake", nil, fake)
	if inbound {
		p.rw.set(inboundConn, true)
	}
	return p
}

// TestPeerListenAddrOutboundUsesRemoteAddr — outbound peers report
// their dial-target's listen port via RemoteAddr; peerListenAddr
// returns it directly without consulting the lookup.
func TestPeerListenAddrOutboundUsesRemoteAddr(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	id := randomID()
	peer := makeFakePeer(t, id, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 32110}, false /*outbound*/)

	la, ok := srv.peerListenAddr(peer)
	if !ok {
		t.Fatal("outbound peer should always yield listen-addr")
	}
	if la.Port != 32110 || !la.IP.Equal(net.IPv4(8, 8, 8, 8)) {
		t.Fatalf("got %v, want 8.8.8.8:32110", la)
	}
}

// TestPeerListenAddrInboundNeedsLookup — inbound peer's RemoteAddr
// has the ephemeral source port; peerListenAddr returns ok=false
// without a lookup, and the disclosed listen port with one.
func TestPeerListenAddrInboundNeedsLookup(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	id := randomID()
	peer := makeFakePeer(t, id, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555 /*ephemeral*/}, true /*inbound*/)

	if _, ok := srv.peerListenAddr(peer); ok {
		t.Fatal("inbound peer with no lookup should yield ok=false")
	}

	srv.SetPeerListenPortLookup(&fakePortLookup{ports: map[enode.ID]uint16{id: 32110}})
	la, ok := srv.peerListenAddr(peer)
	if !ok {
		t.Fatal("inbound peer with lookup should yield ok=true")
	}
	if la.Port != 32110 || !la.IP.Equal(net.IPv4(8, 8, 8, 8)) {
		t.Fatalf("got %v, want 8.8.8.8:32110", la)
	}
}

// TestPeerListenAddrInboundUnknownPort — lookup returns port=0 (peer
// disclosed unknown listen port); peerListenAddr returns ok=false.
func TestPeerListenAddrInboundUnknownPort(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	srv.SetPeerListenPortLookup(&fakePortLookup{ports: map[enode.ID]uint16{}})
	id := randomID()
	peer := makeFakePeer(t, id, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555}, true)

	if _, ok := srv.peerListenAddr(peer); ok {
		t.Fatal("inbound peer with no port disclosed should yield ok=false")
	}
}

// TestFindCrossDialDupKeepsOutbound — when an inbound peer's Hello
// reveals a listen port matching an existing outbound, the inbound
// is selected as the loser (tie-break is outbound-preferring).
func TestFindCrossDialDupKeepsOutbound(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	target := &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 32110}
	out := makeFakePeer(t, randomID(), target, false /*outbound*/)
	in := makeFakePeer(t, randomID(), &net.TCPAddr{IP: target.IP, Port: 55555}, true /*inbound*/)

	dup := srv.findCrossDialDupIn([]*Peer{out}, in, 32110)
	if dup == nil {
		t.Fatal("expected the outbound peer to be found as duplicate")
	}
	if dup != out {
		t.Fatalf("got %v, want %v", dup.ID(), out.ID())
	}
}

// TestFindCrossDialDupSkipsSelf — must not return newPeer itself.
func TestFindCrossDialDupSkipsSelf(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	target := &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 32110}
	self := makeFakePeer(t, randomID(), target, false)

	if dup := srv.findCrossDialDupIn([]*Peer{self}, self, 32110); dup != nil {
		t.Fatalf("must skip newPeer itself; got %v", dup.ID())
	}
}

// TestFindCrossDialDupNoMatch — different IP or port → nil.
func TestFindCrossDialDupNoMatch(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	other := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 32110}, false)
	in := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555}, true)

	if dup := srv.findCrossDialDupIn([]*Peer{other}, in, 32110); dup != nil {
		t.Fatalf("must return nil when no match; got %v", dup.ID())
	}
}

// TestFindCrossDialDupZeroPort — listenPort=0 is a no-op.
func TestFindCrossDialDupZeroPort(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	peer := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 32110}, false)

	if dup := srv.findCrossDialDupIn([]*Peer{peer}, peer, 0); dup != nil {
		t.Fatalf("listenPort=0 must yield nil; got %v", dup.ID())
	}
}

// TestFindCrossDialDupInboundWithLookup — the legitimate symmetric-dial
// case where the new peer is our OUTBOUND leg and the existing inbound
// leg is the same node: we dialed (8.8.8.8, 32110) and the inbound peer
// from that IP disclosed listen port 32110. The outbound leg's port is
// its trusted dial target (RemoteAddr), which is what anchors the match.
func TestFindCrossDialDupInboundWithLookup(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	id := randomID()
	srv.SetPeerListenPortLookup(&fakePortLookup{ports: map[enode.ID]uint16{id: 32110}})
	existing := makeFakePeer(t, id, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555}, true /*inbound*/)
	// Outbound leg: dial target (RemoteAddr) IS (8.8.8.8, 32110), the
	// peer's real listen endpoint — the port we actually connected to.
	newPeer := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 32110}, false /*outbound*/)

	dup := srv.findCrossDialDupIn([]*Peer{existing}, newPeer, 32110)
	if dup != existing {
		t.Fatalf("must find existing inbound as duplicate; got %v want %v", dup, existing.ID())
	}
}

// TestFindCrossDialDupInboundClaimDoesNotEvictHonest — DEFECT 3
// regression (attack case A). An inbound peer sharing a source IP with
// an existing honest inbound connection pre-claims that connection's
// listen port. Because both connections are inbound, their linkage
// would rest entirely on unverified Hello ports, so the dedup must NOT
// fire — otherwise the attacker could get the honest connection torn
// down as a bogus duplicate.
func TestFindCrossDialDupInboundClaimDoesNotEvictHonest(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	victimID := randomID()
	// The honest inbound victim disclosed listen port 32110.
	srv.SetPeerListenPortLookup(&fakePortLookup{ports: map[enode.ID]uint16{victimID: 32110}})
	victim := makeFakePeer(t, victimID, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555}, true /*inbound*/)
	// Attacker: inbound, same source IP, claiming the victim's port.
	attacker := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 44444}, true /*inbound*/)

	if dup := srv.findCrossDialDupIn([]*Peer{victim}, attacker, 32110); dup != nil {
		t.Fatalf("inbound peer's port claim must not match an existing inbound peer; got %v", dup.ID())
	}
}

// TestFindCrossDialDupOutboundClaimIgnoresHelloPort — DEFECT 3
// regression (attack case B). An OUTBOUND peer (one we dialed) claims,
// via Hello, a listen port different from where we actually dialed it —
// here the victim's port. The match must use the trusted dial-target
// port (RemoteAddr), not the self-claimed Hello port, so the attacker
// can't make an existing honest inbound connection look like a
// duplicate and get it evicted.
func TestFindCrossDialDupOutboundClaimIgnoresHelloPort(t *testing.T) {
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	victimID := randomID()
	srv.SetPeerListenPortLookup(&fakePortLookup{ports: map[enode.ID]uint16{victimID: 32110}})
	victim := makeFakePeer(t, victimID, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555}, true /*inbound*/)
	// Attacker: we dialed it at (8.8.8.8, 44444); its Hello lies that it
	// listens on 32110 (the victim's port).
	attacker := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 44444}, false /*outbound*/)

	if dup := srv.findCrossDialDupIn([]*Peer{victim}, attacker, 32110); dup != nil {
		t.Fatalf("outbound peer's Hello-claimed port must not evict an existing connection; got %v", dup.ID())
	}
}

// TestSelectCrossDialLoserPrefersOutbound — direct unit test of the
// tie-break helper. Inbound + outbound: inbound loses regardless of
// argument order.
func TestSelectCrossDialLoserPrefersOutbound(t *testing.T) {
	out := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 32110}, false)
	in := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 55555}, true)

	if loser := selectCrossDialLoser(out, in); loser != in {
		t.Fatalf("(out, in) loser = %v, want in", loser)
	}
	if loser := selectCrossDialLoser(in, out); loser != in {
		t.Fatalf("(in, out) loser = %v, want in", loser)
	}
}

// TestSelectCrossDialLoserSameDirectionKeepsOlder — when both are
// inbound or both outbound, the younger connection loses.
func TestSelectCrossDialLoserSameDirectionKeepsOlder(t *testing.T) {
	older := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 32110}, true)
	// A bare-handed mclock manipulation isn't possible without a
	// clock injection seam; instead, sleep briefly so the second
	// peer's Created() is genuinely newer.
	time.Sleep(2 * time.Millisecond)
	younger := makeFakePeer(t, randomID(), &net.TCPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 44444}, true)

	if older.Created() >= younger.Created() {
		t.Skipf("clock didn't advance; older=%d younger=%d", older.Created(), younger.Created())
	}
	if loser := selectCrossDialLoser(older, younger); loser != younger {
		t.Fatalf("loser = %v, want younger", loser)
	}
	if loser := selectCrossDialLoser(younger, older); loser != younger {
		t.Fatalf("loser = %v, want younger (reversed args)", loser)
	}
}

// TestAlreadyConnectedToInboundWithLookup — alreadyConnectedTo
// must succeed for an inbound peer once that peer has disclosed
// its listen port via the lookup. Without a lookup it returns
// false (no signal).
func TestAlreadyConnectedToInboundWithLookup(t *testing.T) {
	// Construct a server but don't Start it — we drive Peers() via
	// a direct injection using the same channel pattern but skipping
	// the run-loop wiring isn't available here, so use a minimal
	// approach: bypass alreadyConnectedTo (which calls srv.Peers()
	// and hence the run loop) and test the underlying decision
	// surface — peerListenAddr + lookup integration — directly.
	srv := &Server{log: testlog.Logger(t, logging.LvlTrace)}
	id := randomID()
	in := makeFakePeer(t, id, &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 55555}, true)

	target := &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 32110}
	// Pre-Hello: lookup returns ok=false → peerListenAddr returns
	// false → no match.
	if la, ok := srv.peerListenAddr(in); ok {
		t.Fatalf("pre-Hello peerListenAddr should be ok=false; got %v", la)
	}
	srv.SetPeerListenPortLookup(&fakePortLookup{ports: map[enode.ID]uint16{id: 32110}})
	la, ok := srv.peerListenAddr(in)
	if !ok {
		t.Fatal("post-Hello peerListenAddr should be ok=true")
	}
	if la.Port != target.Port || !la.IP.Equal(target.IP) {
		t.Fatalf("peerListenAddr = %v, want %v", la, target)
	}
}

// TestPeerTelemetryDefaults — fresh peer has zero telemetry except
// RelayTxs which defaults true (peers relay tx until disclosed).
func TestPeerTelemetryDefaults(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	if p.MinPing() != 0 {
		t.Errorf("MinPing default = %v, want 0", p.MinPing())
	}
	if p.LastBlockRx() != 0 {
		t.Errorf("LastBlockRx default = %v, want 0", p.LastBlockRx())
	}
	if p.LastTxRx() != 0 {
		t.Errorf("LastTxRx default = %v, want 0", p.LastTxRx())
	}
	if p.BytesRx() != 0 {
		t.Errorf("BytesRx default = %v, want 0", p.BytesRx())
	}
	if p.BytesTx() != 0 {
		t.Errorf("BytesTx default = %v, want 0", p.BytesTx())
	}
	if !p.RelayTxs() {
		t.Error("RelayTxs default should be true")
	}
	if p.BlockRelayOnly() {
		t.Error("BlockRelayOnly default should be false")
	}
}

// TestPeerMarkBlockRxAdvances — MarkBlockRx writes a non-zero
// monotonic value; subsequent calls produce values >= the prior.
func TestPeerMarkBlockRxAdvances(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	p.MarkBlockRx()
	first := p.LastBlockRx()
	if first == 0 {
		t.Fatal("LastBlockRx still zero after MarkBlockRx")
	}
	time.Sleep(time.Millisecond)
	p.MarkBlockRx()
	second := p.LastBlockRx()
	if second < first {
		t.Fatalf("LastBlockRx regressed: first=%v second=%v", first, second)
	}
}

// TestPeerMarkTxRxAdvances — same shape as block.
func TestPeerMarkTxRxAdvances(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	p.MarkTxRx()
	first := p.LastTxRx()
	if first == 0 {
		t.Fatal("LastTxRx still zero after MarkTxRx")
	}
	time.Sleep(time.Millisecond)
	p.MarkTxRx()
	if p.LastTxRx() < first {
		t.Fatal("LastTxRx regressed")
	}
}

// TestPeerSetRelayTxs — setter flips the flag; concurrent reads see
// the new value.
func TestPeerSetRelayTxs(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	p.SetRelayTxs(false)
	if p.RelayTxs() {
		t.Error("RelayTxs should be false after SetRelayTxs(false)")
	}
	p.SetRelayTxs(true)
	if !p.RelayTxs() {
		t.Error("RelayTxs should be true after SetRelayTxs(true)")
	}
}

// TestPeerSetBlockRelayOnly — setter is sticky; default false.
func TestPeerSetBlockRelayOnly(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	if p.BlockRelayOnly() {
		t.Error("default BlockRelayOnly should be false")
	}
	p.SetBlockRelayOnly(true)
	if !p.BlockRelayOnly() {
		t.Error("BlockRelayOnly should be true after SetBlockRelayOnly")
	}
}

// TestPeerBytesRxAccumulates — incrementing the counter from
// (simulated) readLoop iterations is monotonic and the public
// BytesRx accessor reflects the running total. The actual
// readLoop wiring is exercised by the existing TestServerListen
// chain; this is a unit test of the counter semantics.
func TestPeerBytesRxAccumulates(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	if p.BytesRx() != 0 {
		t.Fatalf("BytesRx default = %d, want 0", p.BytesRx())
	}
	// Simulate readLoop's per-message increment.
	p.bytesRx.Add(100)
	p.bytesRx.Add(50)
	p.bytesRx.Add(7)
	if got := p.BytesRx(); got != 157 {
		t.Fatalf("BytesRx = %d, want 157", got)
	}
}

// TestRecordPongRTTNoSendIsNoop — receiving a pong before we ever
// sent a ping must NOT update minPing (would record a meaningless
// "since startup" duration).
func TestRecordPongRTTNoSendIsNoop(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	p.recordPongRTT()
	if p.MinPing() != 0 {
		t.Fatalf("MinPing = %v, want 0 (no ping sent yet)", p.MinPing())
	}
}

// TestRecordPongRTTUpdatesMinimum — after stamping a send time and
// invoking recordPongRTT, MinPing reflects a positive duration.
func TestRecordPongRTTUpdatesMinimum(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	p.lastPingSent.Store(int64(mclock.Now()))
	time.Sleep(time.Millisecond)
	p.recordPongRTT()
	if p.MinPing() <= 0 {
		t.Fatalf("MinPing = %v, want positive", p.MinPing())
	}
}

// TestRecordPongRTTKeepsMinimum — second sample with worse RTT
// must NOT replace the minimum.
func TestRecordPongRTTKeepsMinimum(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	// First sample: short RTT.
	p.lastPingSent.Store(int64(mclock.Now()))
	time.Sleep(time.Millisecond)
	p.recordPongRTT()
	first := p.MinPing()

	// Second sample: long RTT.
	p.lastPingSent.Store(int64(mclock.Now()))
	time.Sleep(20 * time.Millisecond)
	p.recordPongRTT()
	second := p.MinPing()

	if second != first {
		t.Fatalf("MinPing changed from %v to %v; should keep first (smaller) sample", first, second)
	}
}

// TestRecordPongRTTUpdatesOnImprovement — second sample with
// shorter RTT replaces the minimum.
func TestRecordPongRTTUpdatesOnImprovement(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	// First sample: long RTT.
	p.lastPingSent.Store(int64(mclock.Now()))
	time.Sleep(20 * time.Millisecond)
	p.recordPongRTT()
	first := p.MinPing()
	if first <= 0 {
		t.Fatal("first MinPing not set")
	}

	// Second sample: shorter RTT.
	p.lastPingSent.Store(int64(mclock.Now()))
	time.Sleep(time.Millisecond)
	p.recordPongRTT()
	second := p.MinPing()

	if second >= first {
		t.Fatalf("MinPing did not improve: first=%v second=%v", first, second)
	}
}

// TestPeerTelemetryConcurrent — concurrent writers must not race
// (run with -race to verify). Sanity check that the atomic
// operations are correct.
func TestPeerTelemetryConcurrent(t *testing.T) {
	p := NewPeer(randomID(), "test", nil)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for range 100 {
				p.MarkBlockRx()
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				p.MarkTxRx()
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_ = p.MinPing()
				_ = p.RelayTxs()
			}
		}()
	}
	wg.Wait()
	if p.LastBlockRx() == 0 {
		t.Fatal("LastBlockRx still zero after concurrent writes")
	}
	if p.LastTxRx() == 0 {
		t.Fatal("LastTxRx still zero after concurrent writes")
	}
}

// selectCrossDialLoser is in the disc package; this trampoline lets
// the p2p test driver call it without exporting from disc.
// We don't have access to disc from p2p (would be a cycle), so
// re-test the rule here against the same algorithm, keyed off the
// peer fields the disc package consumes.
func selectCrossDialLoser(a, b *Peer) *Peer {
	aIn := a.rw.is(inboundConn)
	bIn := b.rw.is(inboundConn)
	if aIn != bIn {
		if aIn {
			return a
		}
		return b
	}
	if a.Created() < b.Created() {
		return b
	}
	return a
}

// TestPostHandshakeEnforcesBlockRelayCap — the checkpoint rejects a
// dialed block-relay conn when the bucket is already full. The dial
// scheduler and runV2Dialer each check the bucket against the live
// peer set before dialing, but neither sees the other's in-flight
// dials, so two racing picks can both target the last slot; the
// checkpoint, serialized on the run loop, is where the excess is
// caught before it becomes a persistent overshoot.
func TestPostHandshakeEnforcesBlockRelayCap(t *testing.T) {
	srv := newSelfEndpointServer(t, nil, 0)
	// MaxBlockRelayPeers 0 -> default cap of 2; MaxPeers must be
	// large enough that the maxDialedConns/2 clamp doesn't shrink it.
	srv.Config.MaxPeers = 30

	mkBRPeer := func(ip net.IP) *Peer {
		p := makeEvictionPeer(t, evictionOpts{ip: ip})
		p.rw.set(dynDialedConn, true)
		p.rw.set(blockRelayConn, true)
		return p
	}
	mkConn := func(flags connFlag, ip net.IP) *conn {
		pipe, _ := net.Pipe()
		t.Cleanup(func() { pipe.Close() })
		fake := &fakeAddrConn{Conn: pipe, remoteAddr: &net.TCPAddr{IP: ip, Port: 32110}}
		return &conn{
			fd:    fake,
			flags: flags,
			node:  enode.SignNull(new(enr.Record), randomID()),
		}
	}

	peers := map[enode.ID]*Peer{}
	first := mkBRPeer(net.IPv4(10, 0, 0, 1))
	peers[randomID()] = first

	// One of two slots used: a fresh block-relay dial is admitted.
	if err := srv.postHandshakeChecks(peers, 0, 0, mkConn(dynDialedConn|blockRelayConn, net.IPv4(10, 1, 0, 1))); err != nil {
		t.Fatalf("block-relay conn with a free slot: err = %v, want nil", err)
	}
	peers[randomID()] = mkBRPeer(net.IPv4(10, 2, 0, 1))

	// Bucket full: the racing third block-relay dial is rejected.
	if err := srv.postHandshakeChecks(peers, 0, 0, mkConn(dynDialedConn|blockRelayConn, net.IPv4(10, 3, 0, 1))); !errors.Is(err, DiscTooManyPeers) {
		t.Fatalf("block-relay conn past the cap: err = %v, want DiscTooManyPeers", err)
	}
	// A full-relay dial is unaffected by block-relay saturation.
	if err := srv.postHandshakeChecks(peers, 0, 0, mkConn(dynDialedConn, net.IPv4(10, 4, 0, 1))); err != nil {
		t.Fatalf("full-relay conn at block-relay saturation: err = %v, want nil", err)
	}
}
