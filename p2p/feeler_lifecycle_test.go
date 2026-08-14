package p2p

import (
	"net"
	"testing"
	"time"
)

// TestFeelerDialLifecycle — end-to-end feeler probe: DialV2Feeler
// performs a real v2 handshake against a listening server, the
// resulting peer attaches carrying the feeler flag, and
// disconnectFeelerAfter tears it down once the feeler lifetime
// elapses. This is the probe cycle runOneFeeler drives in production
// (address selection is unit-tested separately via pickFeelerAddr).
func TestFeelerDialLifecycle(t *testing.T) {
	target := startTestServer(t, &newkey().PublicKey, nil)
	defer target.Stop()
	dialer := startTestServer(t, &newkey().PublicKey, nil)
	defer dialer.Stop()

	addr, ok := target.listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("target listener addr is %T", target.listener.Addr())
	}
	tcp := &net.TCPAddr{IP: net.IP{127, 0, 0, 1}, Port: addr.Port}

	if err := dialer.DialV2Feeler(tcp); err != nil {
		t.Fatalf("DialV2Feeler: %v", err)
	}
	// The peer must be attached and flagged as a feeler.
	var feeler *Peer
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, p := range dialer.Peers() {
			if p.rw.is(feelerConn) {
				feeler = p
				break
			}
		}
		if feeler != nil {
			break
		}
	}
	if feeler == nil {
		t.Fatal("feeler peer never attached")
	}
	if feeler.Inbound() {
		t.Fatal("feeler peer classified as inbound")
	}

	// The timed teardown must find and drop exactly the feeler peer.
	dialer.disconnectFeelerAfter(tcp, 50*time.Millisecond)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if len(dialer.Peers()) == 0 {
			return
		}
	}
	t.Fatalf("feeler peer still connected after lifetime teardown: %v", dialer.Peers())
}
