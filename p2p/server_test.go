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
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/internal/testlog"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx"
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
		Config:    Config{MaxPeers: 1},
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

	t.Run("port unset", func(t *testing.T) {
		// Bootstrap state: setupLocalNode has run (IP fallback)
		// but setupListening has not yet published the TCP port.
		// Must not false-positive — admin RPC paths can call DialV2
		// before listenSetup completes.
		early := newSelfEndpointServer(t, selfIP, 0)
		if early.IsSelfEndpoint(&net.TCPAddr{IP: selfIP, Port: selfPort}) {
			t.Fatal("IsSelfEndpoint must return false while localnode TCP port is 0")
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
}

func TestDialV2RejectsSelfEndpoint(t *testing.T) {
	srv := startTestServer(t, &newkey().PublicKey, nil)
	defer srv.Stop()

	self := srv.localnode.Node()
	if self.TCP() == 0 {
		t.Fatalf("test server did not publish TCP port to localnode")
	}
	addr := &net.TCPAddr{IP: self.IP(), Port: self.TCP()}

	err := srv.DialV2(addr)
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

	err := srv.postHandshakeChecks(map[enode.ID]*Peer{}, 0, c)
	if !errors.Is(err, DiscSelf) {
		t.Fatalf("postHandshakeChecks err = %v, want DiscSelf", err)
	}
}
