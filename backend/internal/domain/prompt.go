package domain

import "time"

// Known ai_prompt keys resolved by background processes. New processes that
// need a managed prompt should add a constant here and a default in the
// service layer; the key is the stable lookup the processes resolve by.
const (
	PromptKeyBillExtraction       = "bill_extraction"
	PromptKeyOfferSearch          = "offer_search"
	PromptKeyProductNormalization = "product_normalization"
	PromptKeyUnitValueBackfill    = "unit_value_backfill"
	PromptKeyProductInsights      = "product_insights"
)

// AIPrompt is a managed instruction text sent to an AI connector. Key is the
// stable identifier a process resolves by (domain.PromptKey*); Content may be
// empty, which means "use the built-in default" at resolve time. The
// {{categories}} placeholder, when present, is expanded from the live
// product-kind category taxonomy.
type AIPrompt struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
