// Copyright 2026 The Parallax Protocol Authors
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

package torcontrol

import (
	"bufio"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/logging"
)

// Constants from Bitcoin Core src/torcontrol.cpp.
const (
	// DefaultControlAddr is Tor's standard control port.
	DefaultControlAddr = "127.0.0.1:9051"

	torCookieSize = 32 // TOR_COOKIE_SIZE
	torNonceSize  = 32 // TOR_NONCE_SIZE

	// SAFECOOKIE HMAC keys (control-spec §3.24).
	torSafeServerKey = "Tor safe cookie authentication server-to-controller hash"
	torSafeClientKey = "Tor safe cookie authentication controller-to-server hash"

	// Reconnect backoff: 1s start, ×1.5, capped at 10 minutes.
	reconnectStart = time.Second
	reconnectExp   = 1.5
	reconnectMax   = 10 * time.Minute

	// maxLineLength guards against a hostile control endpoint
	// streaming an unbounded line (MAX_LINE_LENGTH).
	maxLineLength = 100000

	// commandTimeout bounds each command round-trip. Local control
	// connections answer in milliseconds; anything slower is wedged.
	commandTimeout = 30 * time.Second

	replyOK           = 250 // TOR_REPLY_OK
	replyUnrecognized = 510 // TOR_REPLY_UNRECOGNIZED
)

// Config parameterizes a Controller.
type Config struct {
	// ControlAddr is the Tor control port ("127.0.0.1:9051").
	ControlAddr string
	// Password enables HASHEDPASSWORD authentication when non-empty.
	Password string
	// KeyFile persists the service's ed25519 key across restarts
	// (Core's onion_v3_private_key). Created 0600. Empty keeps the
	// key ephemeral — a fresh onion identity every reconnect.
	KeyFile string
	// VirtualPort is the port the onion service exposes. Core always
	// advertises the network's default port here regardless of the
	// local listen port, to avoid decloaking nodes that picked a
	// non-standard one.
	VirtualPort uint16
	// Target is the local listener the service forwards to
	// ("127.0.0.1:32110").
	Target string
	// OnService fires with the bare service ID (no ".onion") each
	// time ADD_ONION succeeds — on first connect and after every
	// reconnect. Called from the controller goroutine.
	OnService func(serviceID string)
	// OnDisconnected fires when an established service is lost (the
	// control connection dropped; Tor discards ephemeral services
	// with it). Called from the controller goroutine.
	OnDisconnected func()
	// Log defaults to logging.Root().
	Log logging.Logger

	// initialBackoff overrides reconnectStart in tests.
	initialBackoff time.Duration
}

// Controller maintains the onion service: one goroutine cycling
// connect → authenticate → ADD_ONION → hold, with exponential backoff
// between attempts.
type Controller struct {
	cfg  Config
	log  logging.Logger
	quit chan struct{}
	once sync.Once
	wg   sync.WaitGroup

	// dialFunc is a test seam defaulting to net.DialTimeout.
	dialFunc func(addr string, timeout time.Duration) (net.Conn, error)

	mu   sync.Mutex
	conn net.Conn // live control connection, closed on Stop
}

// New builds a Controller; call Start to begin.
func New(cfg Config) *Controller {
	if cfg.ControlAddr == "" {
		cfg.ControlAddr = DefaultControlAddr
	}
	if cfg.Log == nil {
		cfg.Log = logging.Root()
	}
	if cfg.initialBackoff <= 0 {
		cfg.initialBackoff = reconnectStart
	}
	return &Controller{
		cfg:  cfg,
		log:  cfg.Log,
		quit: make(chan struct{}),
		dialFunc: func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("tcp", addr, timeout)
		},
	}
}

// Start launches the controller loop.
func (c *Controller) Start() {
	c.wg.Add(1)
	go c.run()
}

// Stop tears the controller down and waits for the loop to exit. The
// onion service disappears with the control connection (Tor discards
// ephemeral services), matching Core's shutdown behavior.
func (c *Controller) Stop() {
	c.once.Do(func() { close(c.quit) })
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Controller) run() {
	defer c.wg.Done()
	backoff := c.cfg.initialBackoff
	for {
		select {
		case <-c.quit:
			return
		default:
		}
		connected, established, err := c.session()
		if err != nil && !c.stopping() {
			c.log.Debug("torcontrol: session ended", "err", err)
		}
		if connected {
			// Core resets the backoff whenever the TCP connect
			// succeeds (connected_cb), not only after a full
			// service establishment.
			backoff = c.cfg.initialBackoff
		}
		if established && c.cfg.OnDisconnected != nil && !c.stopping() {
			// A service had been created; its loss matters to the
			// advertisement layer.
			c.cfg.OnDisconnected()
		}
		select {
		case <-c.quit:
			return
		case <-time.After(backoff):
		}
		backoff = min(time.Duration(float64(backoff)*reconnectExp), reconnectMax)
	}
}

func (c *Controller) stopping() bool {
	select {
	case <-c.quit:
		return true
	default:
		return false
	}
}

// session runs one full control-port session. connected reports
// whether the TCP connect succeeded; established whether ADD_ONION
// succeeded before the session ended.
func (c *Controller) session() (connected, established bool, err error) {
	conn, err := c.dialFunc(c.cfg.ControlAddr, commandTimeout)
	if err != nil {
		return false, false, fmt.Errorf("connect %s: %w", c.cfg.ControlAddr, err)
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		conn.Close()
	}()
	br := bufio.NewReaderSize(conn, 4096)

	if err := c.authenticate(conn, br); err != nil {
		return true, false, err
	}
	serviceID, err := c.addOnion(conn, br)
	if err != nil {
		return true, false, err
	}
	c.log.Info("torcontrol: onion service established",
		"service", serviceID+".onion", "virtualPort", c.cfg.VirtualPort, "target", c.cfg.Target)
	if c.cfg.OnService != nil {
		c.cfg.OnService(serviceID)
	}

	// Hold the connection open — the ephemeral service lives exactly
	// as long as it does. Skip any unsolicited lines until the
	// connection drops or Stop closes it.
	conn.SetDeadline(time.Time{})
	for {
		if _, err := readLine(br); err != nil {
			return true, true, err
		}
	}
}

// authenticate runs PROTOCOLINFO and the preferred auth method, in
// Core's order: HASHEDPASSWORD when a password is configured, then
// NULL, then SAFECOOKIE.
func (c *Controller) authenticate(conn net.Conn, br *bufio.Reader) error {
	rep, err := c.command(conn, br, "PROTOCOLINFO 1")
	if err != nil {
		return err
	}
	if rep.code != replyOK {
		return fmt.Errorf("PROTOCOLINFO failed: %d", rep.code)
	}
	methods := map[string]bool{}
	cookieFile := ""
	for _, s := range rep.lines {
		first, rest := splitReplyLine(s)
		if first != "AUTH" {
			continue
		}
		kv := parseMapping(rest)
		for m := range strings.SplitSeq(kv["METHODS"], ",") {
			methods[m] = true
		}
		if f, ok := kv["COOKIEFILE"]; ok {
			cookieFile = f
		}
	}

	var authCmd string
	switch {
	case c.cfg.Password != "" && methods["HASHEDPASSWORD"]:
		c.log.Debug("torcontrol: using HASHEDPASSWORD authentication")
		// Core escapes only double quotes in the password.
		authCmd = `AUTHENTICATE "` + strings.ReplaceAll(c.cfg.Password, `"`, `\"`) + `"`
	case c.cfg.Password != "":
		return errors.New("password provided but HASHEDPASSWORD authentication is not available")
	case methods["NULL"]:
		c.log.Debug("torcontrol: using NULL authentication")
		authCmd = "AUTHENTICATE"
	case methods["SAFECOOKIE"]:
		c.log.Debug("torcontrol: using SAFECOOKIE authentication", "cookie", cookieFile)
		var err error
		if authCmd, err = c.safeCookieAuth(conn, br, cookieFile); err != nil {
			return err
		}
	case methods["HASHEDPASSWORD"]:
		return errors.New("control port wants a password but --torpassword was not provided")
	default:
		return errors.New("no supported authentication method")
	}
	rep, err = c.command(conn, br, authCmd)
	if err != nil {
		return err
	}
	if rep.code != replyOK {
		return fmt.Errorf("authentication failed: %d", rep.code)
	}
	return nil
}

// safeCookieAuth performs the AUTHCHALLENGE half of SAFECOOKIE and
// returns the AUTHENTICATE command to send. Verifies the server-side
// hash so a fake control endpoint can't phish the cookie relationship.
func (c *Controller) safeCookieAuth(conn net.Conn, br *bufio.Reader, cookieFile string) (string, error) {
	cookie, err := os.ReadFile(cookieFile)
	if err != nil {
		return "", fmt.Errorf("reading auth cookie: %w", err)
	}
	if len(cookie) != torCookieSize {
		return "", fmt.Errorf("auth cookie %s is %d bytes, want %d", cookieFile, len(cookie), torCookieSize)
	}
	clientNonce := make([]byte, torNonceSize)
	if _, err := crand.Read(clientNonce); err != nil {
		return "", err
	}
	rep, err := c.command(conn, br, "AUTHCHALLENGE SAFECOOKIE "+hex.EncodeToString(clientNonce))
	if err != nil {
		return "", err
	}
	if rep.code != replyOK || len(rep.lines) == 0 {
		return "", fmt.Errorf("AUTHCHALLENGE failed: %d", rep.code)
	}
	first, rest := splitReplyLine(rep.lines[0])
	if first != "AUTHCHALLENGE" {
		return "", fmt.Errorf("unexpected AUTHCHALLENGE reply %q", rep.lines[0])
	}
	kv := parseMapping(rest)
	serverHash, err1 := hex.DecodeString(kv["SERVERHASH"])
	serverNonce, err2 := hex.DecodeString(kv["SERVERNONCE"])
	if err1 != nil || err2 != nil || len(serverHash) != sha256.Size || len(serverNonce) != torNonceSize {
		return "", fmt.Errorf("malformed AUTHCHALLENGE reply %q", rep.lines[0])
	}
	wantServer := computeResponse(torSafeServerKey, cookie, clientNonce, serverNonce)
	if !hmac.Equal(wantServer, serverHash) {
		return "", errors.New("AUTHCHALLENGE server hash mismatch — endpoint does not know the cookie")
	}
	clientHash := computeResponse(torSafeClientKey, cookie, clientNonce, serverNonce)
	return "AUTHENTICATE " + hex.EncodeToString(clientHash), nil
}

// computeResponse is Core's ComputeResponse: HMAC-SHA256(key,
// cookie|clientNonce|serverNonce).
func computeResponse(key string, cookie, clientNonce, serverNonce []byte) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(cookie)
	mac.Write(clientNonce)
	mac.Write(serverNonce)
	return mac.Sum(nil)
}

// addOnion issues ADD_ONION with the persisted key (or NEW:ED25519-V3)
// and persists the returned key. Returns the bare service ID.
func (c *Controller) addOnion(conn net.Conn, br *bufio.Reader) (string, error) {
	key := "NEW:ED25519-V3" // explicitly request the key type (Core: issue #9214)
	if c.cfg.KeyFile != "" {
		if blob, err := os.ReadFile(c.cfg.KeyFile); err == nil {
			if s := strings.TrimSpace(string(blob)); s != "" {
				key = s
			}
		}
	}
	rep, err := c.command(conn, br, fmt.Sprintf("ADD_ONION %s Port=%d,%s", key, c.cfg.VirtualPort, c.cfg.Target))
	if err != nil {
		return "", err
	}
	switch rep.code {
	case replyOK:
	case replyUnrecognized:
		return "", errors.New("ADD_ONION unrecognized — the Tor daemon is too old")
	default:
		return "", fmt.Errorf("ADD_ONION failed: %d", rep.code)
	}
	serviceID, privateKey := "", ""
	for _, s := range rep.lines {
		kv := parseMapping(s)
		if v, ok := kv["ServiceID"]; ok {
			serviceID = v
		}
		if v, ok := kv["PrivateKey"]; ok {
			privateKey = v
		}
	}
	if serviceID == "" {
		return "", fmt.Errorf("ADD_ONION reply carries no ServiceID: %q", rep.lines)
	}
	// Persist the key so the onion identity survives restarts. Tor
	// omits PrivateKey when we supplied the key ourselves — nothing
	// new to persist then.
	if c.cfg.KeyFile != "" && privateKey != "" {
		if err := os.WriteFile(c.cfg.KeyFile, []byte(privateKey), 0o600); err != nil {
			c.log.Warn("torcontrol: persisting onion service key failed", "path", c.cfg.KeyFile, "err", err)
		} else {
			c.log.Debug("torcontrol: cached onion service key", "path", c.cfg.KeyFile)
		}
	}
	return serviceID, nil
}

// command sends one line and reads its (possibly multi-line) reply,
// under commandTimeout.
func (c *Controller) command(conn net.Conn, br *bufio.Reader, cmd string) (reply, error) {
	conn.SetDeadline(time.Now().Add(commandTimeout))
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write([]byte(cmd + "\r\n")); err != nil {
		return reply{}, err
	}
	return readReply(br)
}

// reply is one parsed control-protocol reply.
type reply struct {
	code  int
	lines []string
}

// readReply consumes reply lines "<code><sep><text>" until the final
// one (sep ' '), skipping unsolicited async events (6xx) and data
// payloads ('+' lines run until a lone "."). Control-spec §2.3.
func readReply(br *bufio.Reader) (reply, error) {
	var rep reply
	for {
		line, err := readLine(br)
		if err != nil {
			return reply{}, err
		}
		if len(line) < 4 {
			return reply{}, fmt.Errorf("malformed reply line %q", line)
		}
		// Strict three-ASCII-digit code, first digit non-zero — the
		// control-spec's codes are SMTP-style (2xx..6xx). Sscanf
		// would also accept signed or zero-padded forms.
		code := 0
		for i := range 3 {
			d := line[i]
			if d < '0' || d > '9' {
				return reply{}, fmt.Errorf("malformed reply code in %q", line)
			}
			code = code*10 + int(d-'0')
		}
		if code < 100 {
			return reply{}, fmt.Errorf("malformed reply code in %q", line)
		}
		sep, text := line[3], line[4:]
		if sep == '+' {
			// Data payload: consume until the terminating ".".
			for {
				dl, err := readLine(br)
				if err != nil {
					return reply{}, err
				}
				if dl == "." {
					break
				}
			}
		}
		if code >= 600 && code < 700 {
			// Async event we never subscribed to; skip it.
			continue
		}
		rep.code = code
		rep.lines = append(rep.lines, text)
		if sep == ' ' {
			return rep, nil
		}
		if sep != '-' && sep != '+' {
			return reply{}, fmt.Errorf("malformed reply separator in %q", line)
		}
	}
}

// readLine reads one CRLF line, enforcing maxLineLength.
func readLine(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		chunk, err := br.ReadString('\n')
		b.WriteString(chunk)
		if b.Len() > maxLineLength {
			return "", errors.New("control reply line exceeds maximum length")
		}
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(chunk, "\n") {
			return strings.TrimRight(b.String(), "\r\n"), nil
		}
	}
}

// splitReplyLine splits "AUTH METHODS=..." into ("AUTH", "METHODS=...")
// — Core's SplitTorReplyLine.
func splitReplyLine(s string) (string, string) {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// parseMapping parses 'Key=Value Key="quoted \"value\""' argument
// lists — Core's ParseTorReplyMapping, including backslash unescaping
// (named escapes plus octal) inside quoted values. Unparseable input
// yields the pairs recognized so far.
func parseMapping(s string) map[string]string {
	out := make(map[string]string)
	i := 0
	for i < len(s) {
		// Key up to '='.
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			return out
		}
		key := s[i : i+eq]
		if j := strings.LastIndexByte(key, ' '); j >= 0 {
			key = key[j+1:]
		}
		i += eq + 1
		var val string
		if i < len(s) && s[i] == '"' {
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				ch := s[i]
				if ch == '\\' && i+1 < len(s) {
					i++
					ch = s[i]
					switch {
					case ch == 'n':
						ch = '\n'
					case ch == 't':
						ch = '\t'
					case ch == 'r':
						ch = '\r'
					case ch >= '0' && ch <= '7':
						// Octal escape: up to 3 digits, or 2 when the
						// first digit exceeds 3 (Core parity).
						oct := int(ch - '0')
						maxDigits := 3
						if oct > 3 {
							maxDigits = 2
						}
						digits := 1
						for digits < maxDigits && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' {
							i++
							oct = oct*8 + int(s[i]-'0')
							digits++
						}
						ch = byte(oct)
					}
				}
				b.WriteByte(ch)
				i++
			}
			if i >= len(s) {
				return out // unterminated quote
			}
			i++ // closing quote
			val = b.String()
		} else {
			end := strings.IndexByte(s[i:], ' ')
			if end < 0 {
				val, i = s[i:], len(s)
			} else {
				val, i = s[i:i+end], i+end
			}
		}
		out[key] = val
		for i < len(s) && s[i] == ' ' {
			i++
		}
	}
	return out
}
