// Package webhook validates destinations and delivers outbox events with
// retries. Delivery state lives in SQLite so a crash never drops an event.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/logging"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

type Dispatcher struct {
	store  *store.Store
	cfg    *config.Config
	log    *slog.Logger
	http   *http.Client
	legacy map[string]config.CallbackTarget
	OnDead func(context.Context, store.Delivery, string)
}

func New(s *store.Store, cfg *config.Config, log *slog.Logger) *Dispatcher {
	legacy := make(map[string]config.CallbackTarget, len(cfg.CallbackTargets))
	for _, t := range cfg.CallbackTargets {
		legacy[t.Name] = t
	}
	return &Dispatcher{
		store:  s,
		cfg:    cfg,
		log:    log,
		legacy: legacy,
		http:   &http.Client{Timeout: cfg.CallbackTimeout},
	}
}

// ValidateURL blocks SSRF targets. Private ranges are rejected unless the
// operator explicitly allows them.
func ValidateURL(raw string, allowInsecure, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" && !(allowInsecure && u.Scheme == "http") {
		return errors.New("webhook URL must use https")
	}
	if u.Hostname() == "" {
		return errors.New("webhook URL has no host")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" && !allowPrivate {
		return errors.New("webhook URL must not target localhost")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := checkIP(ip, allowPrivate); err != nil {
			return err
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve webhook host: %w", err)
	}
	for _, ip := range ips {
		if err := checkIP(ip, allowPrivate); err != nil {
			return err
		}
	}
	return nil
}

func checkIP(ip netip.Addr, allowPrivate bool) error {
	ip = ip.Unmap()
	if allowPrivate {
		return nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("webhook URL resolves to a blocked address %s", ip)
	}
	return nil
}

func (d *Dispatcher) Start(ctx context.Context) {
	if err := d.store.ReclaimStale(5 * time.Minute); err != nil {
		d.log.Warn("reclaim stale deliveries", "error", err)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						d.log.Error("dispatch cycle panicked", "error", rec)
					}
				}()
				if err := d.DispatchOnce(ctx); err != nil {
					d.log.Warn("dispatch cycle failed", "error", err)
				}
			}()
		}
	}
}

func (d *Dispatcher) DispatchOnce(ctx context.Context) error {
	due, err := d.store.DueDeliveries(50)
	if err != nil {
		return err
	}
	for _, item := range due {
		if err := d.deliver(ctx, item); err != nil {
			d.log.Warn("delivery attempt failed", "deliveryId", item.ID, "target", item.TargetName, "error", err)
		}
	}
	return nil
}

func (d *Dispatcher) deliver(ctx context.Context, item store.DueDelivery) error {
	if err := d.store.MarkDelivering(item.ID); err != nil {
		return err
	}

	secret, eventName, body, cname, err := d.render(item)
	if err != nil {
		return d.fail(item, err)
	}
	if err := ValidateURL(item.URL, d.cfg.AllowInsecureWebhooks, d.cfg.AllowPrivateWebhooks); err != nil {
		return d.fail(item, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, item.URL, strings.NewReader(body))
	if err != nil {
		return d.fail(item, err)
	}
	ts := fmt.Sprintf("%d", time.Now().Unix())
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-auvyn-callback-secret", secret)
	req.Header.Set("x-auvyn-event", eventName)
	req.Header.Set("x-auvyn-event-id", fmt.Sprintf("%d", item.EventID))
	req.Header.Set("x-auvyn-timestamp", ts)
	req.Header.Set("x-auvyn-signature", Sign(secret, ts, body))
	if item.WebhookID != "" {
		req.Header.Set("x-auvyn-webhook-id", item.WebhookID)
	}
	if item.ClientID != "" {
		req.Header.Set("x-auvyn-client-id", item.ClientID)
		req.Header.Set("x-auvyn-client-name", cname)
	}

	resp, err := d.http.Do(req)
	if err != nil {
		return d.fail(item, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return d.fail(item, fmt.Errorf("target returned HTTP %d", resp.StatusCode))
	}
	if err := d.store.MarkDelivered(item.ID, resp.StatusCode); err != nil {
		return err
	}
	return nil
}

func (d *Dispatcher) render(item store.DueDelivery) (secret, eventName, body, cname string, err error) {
	secret = ""
	eventName = item.EventName
	var client *store.Client
	if item.ClientID != "" {
		c, err := d.store.GetClient(item.ClientID)
		if err != nil {
			if item.TargetKind == "managed" {
				return "", "", "", "", fmt.Errorf("load client: %w", err)
			}
		} else {
			client = c
			secret = c.SecretCallback
			cname = c.ClientName
		}
	}
	if item.TargetKind == "legacy" {
		if t, ok := d.legacy[item.TargetName]; ok {
			secret = t.Secret
		}
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(item.Payload), &payload); err != nil {
		return "", "", "", "", fmt.Errorf("decode event payload: %w", err)
	}
	if item.TargetKind == "managed" && client != nil {
		payload["event"] = eventName
		payload["client_id"] = client.ClientID
		payload["client_name"] = client.ClientName
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", "", "", err
	}
	return secret, eventName, string(bodyBytes), cname, nil
}

func (d *Dispatcher) fail(item store.DueDelivery, cause error) error {
	next := time.Now().UTC().Add(d.backoff(item.Attempts))
	dead, err := d.store.MarkFailed(item.ID, cause.Error(), next, d.cfg.DeliveryMaxAttempts)
	if err != nil {
		return errors.Join(cause, err)
	}
	if dead {
		d.log.Error("delivery dead-lettered", "deliveryId", item.ID, "target", item.TargetName,
			"url", logging.SafeURL(item.URL), "error", cause)
		if d.OnDead != nil {
			d.OnDead(context.Background(), item.Delivery, cause.Error())
		}
	}
	return nil
}

func (d *Dispatcher) backoff(attempts int) time.Duration {
	if len(d.cfg.DeliveryBackoff) == 0 {
		return time.Minute
	}
	if attempts >= len(d.cfg.DeliveryBackoff) {
		return d.cfg.DeliveryBackoff[len(d.cfg.DeliveryBackoff)-1]
	}
	return d.cfg.DeliveryBackoff[attempts]
}

// Sign returns hex(HMAC-SHA256(secret, timestamp + "." + body)).
func Sign(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}
