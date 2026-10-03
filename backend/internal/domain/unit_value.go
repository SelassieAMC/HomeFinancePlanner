package domain

// UnitValueRow is one products/bill_items/transaction_items row the one-time
// unit-value backfill still has to resolve, joined with the magnitude its
// linked product carries (when any), so the line pass can copy the product's
// decided size instead of re-parsing the raw name.
type UnitValueRow struct {
	ID               int64
	Name             string
	Unit             string
	ProductUnitValue *float64
	ProductUnit      string
}

// UnitValueFix writes one resolved magnitude back. Unit nil keeps the stored
// unit; a non-nil one only lands when the row's unit is still empty — a
// decided unit is never overwritten, and values are never guessed.
type UnitValueFix struct {
	ID        int64
	UnitValue float64
	Unit      *string
}
