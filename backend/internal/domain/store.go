package domain

import "time"

// Store is a recurring market/vendor a bill can be linked to. Bills keep
// market_name as a denormalized snapshot (store renames do not rewrite past
// bills). LogoPath is the server-side file path and is never exposed;
// HasLogo tells the UI whether GET /stores/{id}/logo returns an image.
type Store struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	LogoPath    string    `json:"-"`
	HasLogo     bool      `json:"has_logo"`
	BillCount   int64     `json:"bill_count"` // display-only, counted from bills
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
