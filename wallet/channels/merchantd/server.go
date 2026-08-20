// Package merchantd is the headless merchant daemon (Part 3 §9): a REST API
// over the channel node, policy-driven auto-accept (the engine already
// enforces Part 2 §7.3; this layer adds the operational surface), and
// HMAC-signed webhooks delivered at-least-once from a persistent queue.
package merchantd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/channeld"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// Server exposes the merchant REST API for one channel node.
type Server struct {
	node  *channeld.Node
	token string
	log   *slog.Logger
}

func New(node *channeld.Node, authToken string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{node: node, token: authToken, log: logger}

	// Payment completions become webhook deliveries (persisted first, so a
	// crash between completion and delivery still delivers after restart).
	if url := node.Cfg.Merchant.WebhookURL; url != "" {
		node.OnPayment = func(invoiceID string, state *proofstore.SignedState) {
			s.enqueueWebhook(url, invoiceID, state)
		}
	}
	return s
}

// Handler returns the authenticated mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relays": s.node.Pool.Healthy()})
	})
	mux.HandleFunc("GET /v1/metrics", s.auth(s.metrics))
	mux.HandleFunc("POST /v1/invoices", s.auth(s.createInvoice))
	mux.HandleFunc("GET /v1/invoices/{id}", s.auth(s.getInvoice))
	mux.HandleFunc("GET /v1/channels", s.auth(s.listChannels))
	mux.HandleFunc("POST /v1/channels/{id}/close", s.auth(s.closeChannel))
	mux.HandleFunc("POST /v1/channels/{id}/withdraw", s.auth(s.withdraw))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "bad bearer token")
			return
		}
		next(w, r)
	}
}

// ------------------------------------------------------------- endpoints

type invoiceRequest struct {
	AmountWei  string `json:"amountWei"`
	Memo       string `json:"memo,omitempty"`
	TTLSeconds int64  `json:"ttlSeconds,omitempty"`
	ChannelID  uint64 `json:"channelId,omitempty"`
}

type invoiceResponse struct {
	InvoiceID string `json:"invoiceId"`
	AmountWei string `json:"amountWei"`
	Memo      string `json:"memo,omitempty"`
	ExpiresAt int64  `json:"expiresAt"`
	ChannelID uint64 `json:"channelId,omitempty"`
	Status    string `json:"status"`
	PaidBy    string `json:"paidByChannel,omitempty"`
	PaidSeq   uint64 `json:"paidSeq,omitempty"`
	URI       string `json:"uri,omitempty"`
}

func invoiceStatus(inv proofstore.Invoice, now int64) string {
	switch {
	case inv.Paid:
		return "paid"
	case now > inv.ExpiresAt:
		return "expired"
	default:
		return "pending"
	}
}

func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request) {
	var req invoiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	amount, ok := new(big.Int).SetString(req.AmountWei, 10)
	if !ok || amount.Sign() <= 0 {
		writeError(w, http.StatusBadRequest, "bad amountWei")
		return
	}
	inv, uri, err := s.node.CreateInvoice(amount, req.Memo, time.Duration(req.TTLSeconds)*time.Second, req.ChannelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Push the 21901 to the pinned channel's counterparty when known.
	if req.ChannelID != 0 {
		if key, found := s.channelKey(req.ChannelID); found {
			if err := s.node.SendInvoice(key, inv); err != nil {
				s.log.Warn("invoice relay send", "err", err)
			}
		}
	}
	writeJSON(w, http.StatusCreated, invoiceResponse{
		InvoiceID: inv.ID,
		AmountWei: inv.AmountWei.BigInt().String(),
		Memo:      inv.Memo,
		ExpiresAt: inv.ExpiresAt,
		ChannelID: inv.ChannelID,
		Status:    "pending",
		URI:       uri,
	})
}

func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request) {
	inv, err := s.node.Store.Invoice(r.PathValue("id"))
	if errors.Is(err, proofstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "unknown invoice")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := invoiceResponse{
		InvoiceID: inv.ID,
		AmountWei: inv.AmountWei.BigInt().String(),
		Memo:      inv.Memo,
		ExpiresAt: inv.ExpiresAt,
		ChannelID: inv.ChannelID,
		Status:    invoiceStatus(inv, time.Now().Unix()),
		PaidSeq:   inv.PaidSeq,
	}
	if inv.PaidBy != nil {
		resp.PaidBy = inv.PaidBy.String()
	}
	writeJSON(w, http.StatusOK, resp)
}

type channelResponse struct {
	ChannelID uint64 `json:"channelId"`
	Registry  string `json:"registry"`
	ChainID   string `json:"chainId"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Peer      string `json:"peer"`
	Seq       uint64 `json:"seq"`
	BalanceA  string `json:"balanceA"`
	BalanceB  string `json:"balanceB"`
	Poisoned  bool   `json:"poisoned,omitempty"`
	FrozenTil uint64 `json:"frozenUntilBlock,omitempty"`
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	metas, err := s.node.Store.ListChannels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]channelResponse, 0, len(metas))
	for _, meta := range metas {
		row := channelResponse{
			ChannelID: meta.Key.ChannelID,
			Registry:  meta.Key.Registry.Hex(),
			ChainID:   meta.Key.ChainID,
			Role:      string(meta.Role),
			Status:    string(meta.Status),
			Peer:      meta.PeerAddress.Hex(),
			Poisoned:  meta.Poisoned,
			FrozenTil: meta.FrozenUntilBlock,
		}
		if latest, err := s.node.Store.LatestState(meta.Key); err == nil {
			row.Seq = latest.Seq
		}
		if balA, balB, err := s.node.Engine.CloseBalances(meta.Key); err == nil {
			row.BalanceA, row.BalanceB = balA.String(), balB.String()
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

type closeRequest struct {
	Mode  string `json:"mode,omitempty"` // "coop" (default) | "unilateral"
	Force bool   `json:"force,omitempty"`
}

func (s *Server) closeChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad channel id")
		return
	}
	key, found := s.channelKey(id)
	if !found {
		writeError(w, http.StatusNotFound, "unknown channel")
		return
	}
	var req closeRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // empty body = defaults
	}
	switch req.Mode {
	case "", "coop":
		err = s.node.CoopClose(r.Context(), key)
	case "unilateral":
		err = s.node.UnilateralClose(r.Context(), key, req.Force)
	default:
		writeError(w, http.StatusBadRequest, "mode must be coop or unilateral")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "initiated"})
}

type withdrawRequest struct {
	AmountWei string `json:"amountWei"`
}

// withdraw initiates the cooperative-withdraw negotiation (Part 2 §6.10);
// on-chain submission follows automatically when the countersign arrives.
func (s *Server) withdraw(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad channel id")
		return
	}
	key, found := s.channelKey(id)
	if !found {
		writeError(w, http.StatusNotFound, "unknown channel")
		return
	}
	var req withdrawRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	amount, ok := new(big.Int).SetString(req.AmountWei, 10)
	if !ok || amount.Sign() <= 0 {
		writeError(w, http.StatusBadRequest, "bad amountWei")
		return
	}
	if err := s.node.Withdraw(r.Context(), key, amount); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "negotiating"})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	queueLen, _ := s.node.Store.OutboundLen()
	metas, _ := s.node.Store.ListChannels()
	poisoned := 0
	for _, m := range metas {
		if m.Poisoned {
			poisoned++
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "parallax_channels_total %d\n", len(metas))
	fmt.Fprintf(w, "parallax_channels_poisoned %d\n", poisoned)
	fmt.Fprintf(w, "parallax_relays_healthy %d\n", s.node.Pool.Healthy())
	fmt.Fprintf(w, "parallax_outbound_queue %d\n", queueLen)
}

func (s *Server) channelKey(id uint64) (proofstore.ChannelKey, bool) {
	metas, err := s.node.Store.ListChannels()
	if err != nil {
		return proofstore.ChannelKey{}, false
	}
	for _, meta := range metas {
		if meta.Key.ChannelID == id {
			return meta.Key, true
		}
	}
	return proofstore.ChannelKey{}, false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// -------------------------------------------------------------- webhooks

// WebhookPayload is the POST body sent on payment (Part 3 §9): paid means
// the dual-signed state is committed (W2).
type WebhookPayload struct {
	InvoiceID string `json:"invoiceId"`
	ChannelID uint64 `json:"channelId"`
	Seq       uint64 `json:"seq"`
	AmountWei string `json:"amountWei"`
}

func (s *Server) enqueueWebhook(url, invoiceID string, state *proofstore.SignedState) {
	inv, err := s.node.Store.Invoice(invoiceID)
	if err != nil {
		s.log.Error("webhook: invoice lookup", "err", err)
		return
	}
	body, err := json.Marshal(WebhookPayload{
		InvoiceID: invoiceID,
		ChannelID: state.Key.ChannelID,
		Seq:       state.Seq,
		AmountWei: inv.AmountWei.BigInt().String(),
	})
	if err != nil {
		s.log.Error("webhook: marshal", "err", err)
		return
	}
	now := time.Now().Unix()
	if _, err := s.node.Store.EnqueueWebhook(proofstore.WebhookItem{
		URL:       url,
		Body:      body,
		ExpiresAt: now + 24*3600,
	}); err != nil {
		s.log.Error("webhook: enqueue", "err", err)
	}
}

// Sign computes the X-Parallax-Signature header value: hex HMAC-SHA256 of
// the body keyed with the bearer token.
func Sign(token string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// RunWebhookWorker drains the persistent webhook queue until ctx is done.
func (s *Server) RunWebhookWorker(ctx context.Context, interval time.Duration) {
	client := &http.Client{Timeout: 10 * time.Second}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().Unix()
			due, expired, err := s.node.Store.DueWebhooks(now, 16)
			if err != nil {
				s.log.Error("webhook queue", "err", err)
				continue
			}
			for _, item := range expired {
				s.log.Error("webhook gave up after 24h", "url", item.URL)
			}
			for _, item := range due {
				if s.deliver(client, item) {
					_ = s.node.Store.RemoveWebhook(item.ID)
					continue
				}
				backoff := int64(30) << uint(min(item.Attempts, 6)) // 30s .. 32m
				_ = s.node.Store.RescheduleWebhook(item.ID, item.Attempts+1, now+backoff)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) deliver(client *http.Client, item proofstore.WebhookItem) bool {
	req, err := http.NewRequest(http.MethodPost, item.URL, strings.NewReader(string(item.Body)))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Parallax-Signature", Sign(s.token, item.Body))
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
