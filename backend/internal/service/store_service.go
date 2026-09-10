package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// StoreService manages the recurring markets a bill can be linked to.
type StoreService struct {
	stores   StoreStore
	logosDir string
}

// StoreInput is the user-facing payload for create/update; the logo is
// managed separately through SetLogo/RemoveLogo.
type StoreInput struct {
	Name        string
	Description string
	Location    string
}

// NewStoreService wires the store workflow. logosDir is where logo files are
// stored.
func NewStoreService(stores StoreStore, logosDir string) *StoreService {
	return &StoreService{stores: stores, logosDir: logosDir}
}

func (s *StoreService) List(ctx context.Context) ([]domain.Store, error) {
	return s.stores.List(ctx)
}

func (s *StoreService) Get(ctx context.Context, id int64) (domain.Store, error) {
	return s.stores.GetByID(ctx, id)
}

func (s *StoreService) Create(ctx context.Context, in StoreInput) (domain.Store, error) {
	store, err := s.build(in)
	if err != nil {
		return domain.Store{}, err
	}
	return s.stores.Create(ctx, store)
}

// Update rewrites name/description/location; the logo is untouched (managed
// by SetLogo/RemoveLogo).
func (s *StoreService) Update(ctx context.Context, id int64, in StoreInput) (domain.Store, error) {
	store, err := s.build(in)
	if err != nil {
		return domain.Store{}, err
	}
	store.ID = id
	return s.stores.Update(ctx, store)
}

// Delete removes the store and its logo file. Bills keep their market_name
// snapshot; their store_id is set to NULL by the FK.
func (s *StoreService) Delete(ctx context.Context, id int64) error {
	store, err := s.stores.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.stores.Delete(ctx, id); err != nil {
		return err
	}
	if store.LogoPath != "" {
		if err := os.Remove(store.LogoPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete store logo: %w", err)
		}
	}
	return nil
}

// MaxLogoBytes caps uploaded logo files at 2 MB.
const MaxLogoBytes = 2 << 20

// SetLogo stores an uploaded logo image, replacing any previous one.
func (s *StoreService) SetLogo(ctx context.Context, id int64, mimeType string, file []byte) (domain.Store, error) {
	// The sniffed magic bytes win when they identify an image: a client can
	// declare any Content-Type it likes, but a gif-in-a-.png must not slip
	// through the declared type. Opaque results (text/plain, …) leave the
	// declared type standing.
	if sniffed := sniffMime(file); strings.HasPrefix(sniffed, "image/") {
		mimeType = sniffed
	}
	mimeType = strings.ToLower(mimeType)
	ext, ok := logoMime[mimeType]
	if !ok {
		return domain.Store{}, validationError("unsupported file type %q (want jpeg, png or webp)", mimeType)
	}
	if len(file) == 0 {
		return domain.Store{}, validationError("logo file is empty")
	}
	if len(file) > MaxLogoBytes {
		return domain.Store{}, validationError("logo file exceeds %d MB limit", MaxLogoBytes>>20)
	}

	existing, err := s.stores.GetByID(ctx, id)
	if err != nil {
		return domain.Store{}, err
	}
	path, err := writeFileRandom(s.logosDir, ext, file)
	if err != nil {
		return domain.Store{}, fmt.Errorf("save logo: %w", err)
	}
	updated, err := s.stores.SetLogo(ctx, id, path)
	if err != nil {
		_ = os.Remove(path)
		return domain.Store{}, err
	}
	if existing.LogoPath != "" && existing.LogoPath != path {
		_ = os.Remove(existing.LogoPath)
	}
	return updated, nil
}

// RemoveLogo clears the logo and deletes its file.
func (s *StoreService) RemoveLogo(ctx context.Context, id int64) (domain.Store, error) {
	existing, err := s.stores.GetByID(ctx, id)
	if err != nil {
		return domain.Store{}, err
	}
	updated, err := s.stores.SetLogo(ctx, id, "")
	if err != nil {
		return domain.Store{}, err
	}
	if existing.LogoPath != "" {
		if err := os.Remove(existing.LogoPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return domain.Store{}, fmt.Errorf("remove logo file: %w", err)
		}
	}
	return updated, nil
}

// LogoPath resolves the logo file for serving; mirrors ReceiptImagePath.
func (s *StoreService) LogoPath(ctx context.Context, id int64) (string, string, error) {
	store, err := s.stores.GetByID(ctx, id)
	if err != nil {
		return "", "", err
	}
	if store.LogoPath == "" {
		return "", "", validationError("store %d has no logo", id)
	}
	return store.LogoPath, detectMime(store.LogoPath), nil
}

// build validates and normalizes a StoreInput.
func (s *StoreService) build(in StoreInput) (domain.Store, error) {
	if err := validateRequiredString(in.Name, "name", 80); err != nil {
		return domain.Store{}, err
	}
	description := strings.TrimSpace(in.Description)
	location := strings.TrimSpace(in.Location)
	if len(description) > 500 {
		return domain.Store{}, validationError("description must be at most 500 characters")
	}
	if len(location) > 200 {
		return domain.Store{}, validationError("location must be at most 200 characters")
	}
	return domain.Store{
		Name:        strings.TrimSpace(in.Name),
		Description: description,
		Location:    location,
	}, nil
}
