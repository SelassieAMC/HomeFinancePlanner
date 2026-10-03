package domain

import (
	"errors"
	"time"
)

// BillStatus is the persisted state of a bill. Scan drafts live in bill_scans
// (until confirmed or discarded), so stored bills are accepted ones.
type BillStatus string

const (
	BillStatusPending   BillStatus = "pending"
	BillStatusDraft     BillStatus = "draft"
	BillStatusAccepted  BillStatus = "accepted"
	BillStatusDiscarded BillStatus = "discarded"
)

func (s BillStatus) Valid() bool {
	switch s {
	case BillStatusPending, BillStatusDraft, BillStatusAccepted, BillStatusDiscarded:
		return true
	}
	return false
}

// BillFilters narrows bill list queries. Zero values mean "no filter".
type BillFilters struct {
	Status BillStatus
	Month  string // YYYY-MM
	Market string // substring match, case-insensitive
}

// ReceiptFile is one uploaded part of a receipt: the image/PDF bytes and their
// content type. One file is the classic single-photo receipt; several files are
// the consecutive parts of one long paper receipt.
type ReceiptFile struct {
	Data     []byte
	MimeType string
}

// BillFile is one stored part of a saved bill's receipt (position 1..n, in the
// order the photos were taken). All fields except Position are server-side
// only.
type BillFile struct {
	ID        int64     `json:"-"`
	BillID    int64     `json:"-"`
	Position  int       `json:"position"`
	Path      string    `json:"-"`
	MimeType  string    `json:"-"`
	FileHash  string    `json:"-"`
	CreatedAt time.Time `json:"-"`
}

// Bill is a scanned receipt with its extracted header data.
type Bill struct {
	ID                 int64      `json:"id"`
	MarketName         string     `json:"market_name"`
	Date               string     `json:"date"` // YYYY-MM-DD
	PaymentMethod      string     `json:"payment_method"`
	CardLastDigits     string     `json:"card_last_digits,omitempty"` // last 4 digits when paid by card
	Currency           string     `json:"currency"`
	ItemsSubtotalCents int64      `json:"items_subtotal_cents"`
	DiscountCents      int64      `json:"discount_cents"`
	VATCents           int64      `json:"vat_cents"`
	TotalCents         int64      `json:"total_cents"`         // computed: sum of lines − global discount (VAT already included in prices)
	PrintedTotalCents  int64      `json:"printed_total_cents"` // as printed on the receipt (warning when ≠ TotalCents)
	Status             BillStatus `json:"status"`
	ImagePath          string     `json:"image_path,omitempty"`   // server-side path of part 1; not exposed
	FileHash           string     `json:"-"`                      // sha256 of part 1; dedup only (children carry every hash)
	FileCount          int        `json:"file_count,omitempty"`   // receipt parts stored for this bill (hidden for single-file receipts)
	Files              []BillFile `json:"-"`                      // every stored part, position order
	ExtractedBy        string     `json:"extracted_by"`           // provider id that produced the draft
	BudgetID           *int64     `json:"budget_id"`              // optional budget this bill counts toward
	BudgetName         string     `json:"budget_name,omitempty"`  // display-only, joined from budgets
	TransactionID      *int64     `json:"transaction_id"`         // expense transaction recorded at confirm
	AccountID          *int64     `json:"account_id"`             // account of that transaction (display-only, joined; nil when unlinked)
	AccountName        string     `json:"account_name,omitempty"` // display-only, joined from accounts
	StoreID            *int64     `json:"store_id"`               // store resolved from market_name (nil for empty names; ON DELETE SET NULL)
	Items              []BillItem `json:"items,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// BillItem is one article line printed on a bill.
type BillItem struct {
	ID             int64    `json:"id"`
	BillID         int64    `json:"bill_id"`
	Name           string   `json:"name"`
	StandardName   string   `json:"standard_name,omitempty"` // display-only, joined from product_name_mappings (raw name stays authoritative)
	GenericName    string   `json:"generic_name,omitempty"`  // display-only, joined from product_name_mappings (product family; empty = none known)
	Brand          string   `json:"brand,omitempty"`         // optional, editable in the draft
	Unit           string   `json:"unit,omitempty"`          // measure: kg, g, l, ml, pcs, …
	UnitValue      *float64 `json:"unit_value,omitempty"`    // printed size magnitude ("500" for "500ml"); nil = unknown
	CategoryID     *int64   `json:"category_id"`             // nullable; resolved from the AI category name
	CategoryName   string   `json:"category_name"`           // display-only, joined from categories
	Quantity       float64  `json:"quantity"`
	UnitPriceCents int64    `json:"unit_price_cents"` // may be negative for deposit returns
	DiscountCents  int64    `json:"discount_cents"`
	LineTotalCents int64    `json:"line_total_cents"` // may be negative for deposit returns
	IsReturn       bool     `json:"is_return"`        // deposit/bottle return (e.g. "Leergut")
	BudgetID       *int64   `json:"budget_id"`        // per-line budget override (nil = bill's budget)
	ProductID      *int64   `json:"product_id"`       // catalogue product resolved from the name (nil for returns)
}

// BillItemDraft is one extracted line before persistence. ID is a session-local
// line number (1..n) so the UI can key editable rows. CategoryName is the raw
// AI suggestion; CategoryID is filled in by the service after resolving (or
// creating) the category.
type BillItemDraft struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	StandardName   string   `json:"standard_name,omitempty"` // AI-suggested (or user-corrected) standardized name; empty on pre-feature drafts
	GenericName    string   `json:"generic_name,omitempty"`  // AI-suggested (or user-corrected) product-family name; empty = no broader family known
	Brand          string   `json:"brand,omitempty"`
	Unit           string   `json:"unit,omitempty"`       // measure: kg, g, l, ml, pcs, …
	UnitValue      *float64 `json:"unit_value,omitempty"` // AI-suggested (or user-corrected) size magnitude; nil = unknown
	CategoryName   string   `json:"category_name,omitempty"`
	CategoryID     *int64   `json:"category_id,omitempty"` // nil when no category applies
	Quantity       float64  `json:"quantity"`
	UnitPriceCents int64    `json:"unit_price_cents"` // may be negative for deposit returns
	DiscountCents  int64    `json:"discount_cents"`
	LineTotalCents int64    `json:"line_total_cents"` // may be negative for deposit returns
	IsReturn       bool     `json:"is_return,omitempty"`
	BudgetID       *int64   `json:"budget_id,omitempty"` // per-line budget override (nil = bill's budget)
}

// BillDraft is the normalized extraction result, held in memory until the user
// confirms it — drafts are never persisted. TotalCents is always computed from
// the lines minus the global discount (VAT included in prices);
// PrintedTotalCents is the amount printed on the receipt and
// drives the "calculated total does not match the receipt" warning.
type BillDraft struct {
	MarketName         string          `json:"market_name"`
	Date               string          `json:"date"` // YYYY-MM-DD ("" when unreadable)
	PaymentMethod      string          `json:"payment_method"`
	CardLastDigits     string          `json:"card_last_digits,omitempty"` // "" unless paid by card
	Currency           string          `json:"currency,omitempty"`
	Items              []BillItemDraft `json:"items"`
	ItemsSubtotalCents int64           `json:"items_subtotal_cents"` // sum of the line totals
	DiscountCents      int64           `json:"discount_cents"`       // receipt-wide discount, subtracted from the item sum in the total
	VATCents           int64           `json:"vat_cents"`
	TotalCents         int64           `json:"total_cents"`         // computed: sum of lines − global discount (VAT already included in prices)
	PrintedTotalCents  int64           `json:"printed_total_cents"` // as printed on the receipt
}

// BillScanStatus is the pipeline state of one persisted scan. It is separate
// from BillStatus: a scan is analyzed in the background and only becomes a
// bill (accepted) when the user confirms it.
type BillScanStatus string

const (
	BillScanAnalyzing BillScanStatus = "analyzing"
	BillScanDone      BillScanStatus = "done"
	BillScanFailed    BillScanStatus = "failed"
)

func (s BillScanStatus) Valid() bool {
	switch s {
	case BillScanAnalyzing, BillScanDone, BillScanFailed:
		return true
	}
	return false
}

// BillScanFile is one stored part of a scan's receipt upload (position 1..n).
// All fields except Position are server-side only.
type BillScanFile struct {
	ID        int64     `json:"-"`
	ScanID    int64     `json:"-"`
	Position  int       `json:"position"`
	Path      string    `json:"-"`
	MimeType  string    `json:"-"`
	FileHash  string    `json:"-"`
	CreatedAt time.Time `json:"-"`
}

// BillScan is one receipt upload awaiting analysis and confirmation. Scan is
// the initial response (status analyzing); the same shape is polled by token
// until the draft is ready. ImagePath and MimeType are server-side only
// (part 1's mirror).
type BillScan struct {
	ID         int64          `json:"-"` // DB id; the token is the API handle
	ScanToken  string         `json:"scan_token"`
	Status     BillScanStatus `json:"status"`
	ProviderID string         `json:"provider_id,omitempty"`
	Draft      *BillDraft     `json:"draft"`           // null while analyzing or failed
	Error      string         `json:"error,omitempty"` // set when failed
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"-"`
	ImagePath  string         `json:"-"`
	MimeType   string         `json:"-"`
	FileHash   string         `json:"-"`                    // sha256 of part 1; dedup only (children carry every hash)
	FileCount  int            `json:"file_count,omitempty"` // receipt parts in this upload (hidden for single-file scans)
	Files      []BillScanFile `json:"-"`                    // every stored part, position order
}

// BillConfirmInput is the (possibly user-corrected) draft the client sends
// back when confirming a scan. The bill and its expense transaction are
// created from these values in one step. The server recomputes the total from
// the lines minus the global discount; PrintedTotalCents carries the
// receipt's printed amount through unchanged for the mismatch warning.
type BillConfirmInput struct {
	// MarketName is matched case-insensitively against existing stores; the
	// server find-or-creates a store from it and links the bill.
	MarketName        string          `json:"market_name"`
	Date              string          `json:"date"`
	PaymentMethod     string          `json:"payment_method"`
	CardLastDigits    string          `json:"card_last_digits"`
	Currency          string          `json:"currency"`
	DiscountCents     int64           `json:"discount_cents"` // receipt-wide discount, subtracted from the item sum in the total
	VATCents          int64           `json:"vat_cents"`
	PrintedTotalCents int64           `json:"printed_total_cents"`
	BudgetID          *int64          `json:"budget_id,omitempty"` // optional budget this bill counts toward
	Items             []BillItemDraft `json:"items"`
	AccountID         *int64          `json:"account_id,omitempty"` // optional expense target
}

// BillStatsRow is one aggregated row of bill analysis. Currency is the
// grouping key the repository returns; the service merges per-currency rows
// and zeroes it before responding, so it never appears on the wire.
type BillStatsRow struct {
	Label      string  `json:"label"`
	BillCount  int64   `json:"bill_count"`
	Quantity   float64 `json:"quantity,omitempty"` // item grouping only
	TotalCents int64   `json:"total_cents"`
	Currency   string  `json:"-"`
}

// AIProviderType enumerates supported AI connector families.
type AIProviderType string

const (
	AIProviderOllama           AIProviderType = "ollama"
	AIProviderOllamaWebSearch  AIProviderType = "ollama_web_search"
	AIProviderOpenAI           AIProviderType = "openai"
	AIProviderGemini           AIProviderType = "gemini"
	AIProviderAnthropic        AIProviderType = "anthropic"
	AIProviderOpenAICompatible AIProviderType = "openai_compatible"
)

func (t AIProviderType) Valid() bool {
	switch t {
	case AIProviderOllama, AIProviderOllamaWebSearch, AIProviderOpenAI, AIProviderGemini, AIProviderAnthropic, AIProviderOpenAICompatible:
		return true
	}
	return false
}

// AIProvider is a configured AI connector. APIKey is stored encrypted at rest
// and masked in API responses. DefaultForBills / DefaultForSearch mark the
// connector used by default for bill reads and web (offer) searches
// respectively; the settings service normalizes the list so at most one
// provider carries each flag, and a purpose without any flag falls back to
// any configured connector.
type AIProvider struct {
	ID               string         `json:"id"`
	Type             AIProviderType `json:"type"`
	BaseURL          string         `json:"base_url,omitempty"`
	APIKey           string         `json:"api_key,omitempty"` // masked in responses; empty = keep existing
	Model            string         `json:"model"`
	DefaultForBills  bool           `json:"default_for_bills,omitempty"`
	DefaultForSearch bool           `json:"default_for_search,omitempty"`
}

var ErrKeyNotConfigured = errors.New("no encryption key configured")
