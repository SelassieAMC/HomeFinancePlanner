package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

func timeParseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

var (
	monthRegex = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
	dateRegex  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// validationError wraps domain.ErrValidation with a human-readable message.
func validationError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrValidation, fmt.Sprintf(format, args...))
}

func validateMonth(month, field string) error {
	if month == "" {
		return validationError("%s is required (format YYYY-MM)", field)
	}
	if !monthRegex.MatchString(month) {
		return validationError("%s %q must be YYYY-MM", field, month)
	}
	return nil
}

func validateDate(date, field string) error {
	if date == "" {
		return validationError("%s is required (format YYYY-MM-DD)", field)
	}
	if !dateRegex.MatchString(date) {
		return validationError("%s %q must be YYYY-MM-DD", field, date)
	}
	// Reject impossible dates like 2026-02-31.
	if _, err := timeParseDate(date); err != nil {
		return validationError("%s %q is not a real date", field, date)
	}
	return nil
}

func validateRequiredString(value, field string, maxLen int) error {
	if strings.TrimSpace(value) == "" {
		return validationError("%s is required", field)
	}
	if len(value) > maxLen {
		return validationError("%s must be at most %d characters", field, maxLen)
	}
	return nil
}

func validatePositiveCents(cents int64, field string) error {
	if cents <= 0 {
		return validationError("%s must be a positive amount in cents", field)
	}
	return nil
}

func validateNonNegativeCents(cents int64, field string) error {
	if cents < 0 {
		return validationError("%s must be zero or a positive amount in cents", field)
	}
	return nil
}
