package ipcenter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/logging"
)

const maxResponseBytes = 1 << 20

type Client struct {
	cfg  *config.Config
	http *http.Client
	log  *slog.Logger
}

type TransactionRequest struct {
	Product string
	Dest    string
	Qty     any
	RefID   string
	Nominal any
	Meta    map[string]any
}

type TransactionResult struct {
	OK         bool
	StatusCode int
	RefID      string
	Product    string
	Dest       string
	Qty        *int64
	Nominal    *int64
	Category   string
	Sign       string
	CenterURL  string
	Raw        string
	Meta       map[string]any
	Balance    *Balance
	Provider   ProviderResult
	ReceivedAt time.Time
}

type BalanceResult struct {
	OK         bool
	StatusCode int
	MemberID   string
	Balance    *Balance
	Raw        string
	Meta       map[string]any
	Provider   ProviderResult
	ReceivedAt time.Time
}

type TicketResult struct {
	OK         bool
	StatusCode int
	MemberID   string
	Amount     int64
	Ticket     string
	Sign       string
	Raw        string
	Meta       map[string]any
	Provider   ProviderResult
	ReceivedAt time.Time
}

func New(cfg *config.Config, log *slog.Logger) *Client {
	return &Client{
		cfg:  cfg,
		log:  log,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) SendTransaction(ctx context.Context, req TransactionRequest) (*TransactionResult, error) {
	if req.Product == "" || req.Dest == "" {
		return nil, errors.New("product and dest are required")
	}
	refID := req.RefID
	if refID == "" {
		return nil, errors.New("refID is required")
	}
	nominal := ParseNominalToInt(req.Nominal)
	bebas := c.cfg.IsBebasProduct(req.Product)
	qty, err := ResolveQty(req.Qty, nominal, bebas, c.cfg.EwalletBebasQtyIsNominal, c.cfg.EwalletBebasUnit)
	if err != nil {
		return nil, err
	}
	if bebas && qty == nil {
		return nil, ErrQtyRequired
	}

	signNominal := nominal
	if bebas {
		signNominal = nil
	}
	sign := BuildSignature(c.cfg.SignPrefix, c.cfg.MemberID, req.Product, req.Dest, refID,
		c.cfg.PIN, c.cfg.Password, signNominal, c.cfg.SignIncludeNominal)

	url := TransactionURL(c.cfg.CenterURL, bebas, c.cfg.MemberID, req.Product, req.Dest, qty, refID,
		c.cfg.PIN, c.cfg.Password, sign, nominal)

	c.log.Info("send transaction to center",
		"endpoint", logging.SafeURL(url),
		"refID", refID,
		"product", req.Product,
		"qty", qty,
		"nominal", nominal,
		"bebas", bebas,
	)
	status, raw, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("center request: %w", err)
	}
	provider := ParseMessage(raw)
	category := ClassifyTransaction(raw, status)
	c.log.Info("response from center",
		"refID", refID, "statusCode", status, "category", category, "providerCode", provider.Code)

	return &TransactionResult{
		OK:         true,
		StatusCode: status,
		RefID:      refID,
		Product:    req.Product,
		Dest:       req.Dest,
		Qty:        qty,
		Nominal:    nominal,
		Category:   category,
		Sign:       sign,
		CenterURL:  url,
		Raw:        raw,
		Meta:       req.Meta,
		Balance:    ParseBalance(raw),
		Provider:   provider,
		ReceivedAt: time.Now().UTC(),
	}, nil
}

func (c *Client) CheckBalance(ctx context.Context, memberID string, meta map[string]any) (*BalanceResult, error) {
	used := memberID
	if used == "" {
		used = c.cfg.MemberID
	}
	if used == "" {
		return nil, errors.New("memberID is required")
	}
	if c.cfg.PIN == "" || c.cfg.Password == "" {
		return nil, errors.New("PIN and PASSWORD are required for balance check")
	}
	sign := BuildCheckBalanceSignature(c.cfg.SignPrefix, used, c.cfg.PIN, c.cfg.Password)
	url := BalanceURL(c.cfg.CenterURL, used, c.cfg.PIN, c.cfg.Password, sign)
	c.log.Info("check balance at center", "endpoint", logging.SafeURL(url), "memberId", used)
	status, raw, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("center request: %w", err)
	}
	provider := ParseMessage(raw)
	balance := ParseBalance(raw)
	c.log.Info("response balance from center", "statusCode", status, "memberId", used, "balance", balance)
	return &BalanceResult{
		OK:         status >= 200 && status < 300,
		StatusCode: status,
		MemberID:   used,
		Balance:    balance,
		Raw:        raw,
		Meta:       meta,
		Provider:   provider,
		ReceivedAt: time.Now().UTC(),
	}, nil
}

func (c *Client) CreateTicket(ctx context.Context, amount int64, memberID string, meta map[string]any) (*TicketResult, error) {
	if amount <= 0 {
		return nil, errors.New("amount must be > 0")
	}
	used := memberID
	if used == "" {
		used = c.cfg.MemberID
	}
	if used == "" || c.cfg.PIN == "" || c.cfg.Password == "" {
		return nil, errors.New("member credentials are not fully configured")
	}
	sign := BuildTicketSignature(c.cfg.SignPrefix, used, c.cfg.PIN, c.cfg.Password, amount)
	url := TicketURL(c.cfg.CenterURL, used, amount, sign)
	c.log.Info("create ticket at center", "endpoint", logging.SafeURL(url), "memberId", used, "amount", amount)
	status, raw, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("center request: %w", err)
	}
	provider := ParseMessage(raw)
	ticket := ParseTicket(raw)
	c.log.Info("response ticket from center",
		"statusCode", status, "memberId", used, "ticket", ticket != "")
	return &TicketResult{
		OK:         status >= 200 && status < 300,
		StatusCode: status,
		MemberID:   used,
		Amount:     amount,
		Ticket:     ticket,
		Sign:       sign,
		Raw:        raw,
		Meta:       meta,
		Provider:   provider,
		ReceivedAt: time.Now().UTC(),
	}, nil
}

func (c *Client) get(ctx context.Context, url string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", errors.New("invalid center request")
	}
	req.Header.Set("User-Agent", "auvyn-apies/2.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", safeError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, "", errors.New("failed to read center response")
	}
	return resp.StatusCode, string(body), nil
}

// safeError strips *url.Error.URL, which contains pin/password/sign in the
// query string and must never reach logs or API responses.
func safeError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}
