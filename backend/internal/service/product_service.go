package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// ProductService manages the product catalogue. Products are created only by
// the bill workflow (find-or-created from bill item names at confirm time);
// this service edits them and manages their photos. Rewriting name/unit/
// category propagates to every linked bill item (the UI confirms first).
type ProductService struct {
	products   ProductStore
	categories CategoryStore
	photosDir  string
}

// ProductFilters re-exports the shared domain filter type.
type ProductFilters = domain.ProductFilters

// ProductInput is the user-facing payload for product update; the photo is
// managed separately through SetPhoto/RemovePhoto.
type ProductInput struct {
	Name        string
	Brand       string
	Unit        string
	CategoryID  *int64
	Description string
}

// NewProductService wires the product workflow. photosDir is where photo
// files are stored.
func NewProductService(products ProductStore, categories CategoryStore, photosDir string) *ProductService {
	return &ProductService{products: products, categories: categories, photosDir: photosDir}
}

// List returns the paged product list.
func (s *ProductService) List(ctx context.Context, f domain.ProductFilters) (domain.ProductPage, error) {
	return s.products.List(ctx, f)
}

func (s *ProductService) Get(ctx context.Context, id int64) (domain.Product, error) {
	return s.products.GetByID(ctx, id)
}

// StorePrices returns the latest per-store prices of a product. The amounts
// are scoped to the product's most recent purchase currency so the list never
// mixes currencies; a product never bought (or with no priced lines) yields an
// empty list.
func (s *ProductService) StorePrices(ctx context.Context, id int64) ([]domain.ProductStorePrice, error) {
	product, err := s.products.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if product.PriceCurrency == "" {
		return []domain.ProductStorePrice{}, nil
	}
	return s.products.StorePrices(ctx, id, product.PriceCurrency)
}

// Update rewrites a product's editable fields and propagates name/unit/
// category to the linked bill items; the photo is untouched (managed by
// SetPhoto/RemovePhoto).
func (s *ProductService) Update(ctx context.Context, id int64, in ProductInput) (domain.Product, error) {
	product, err := s.build(ctx, in)
	if err != nil {
		return domain.Product{}, err
	}
	product.ID = id
	return s.products.Update(ctx, product)
}

// MaxPhotoBytes caps uploaded photo files at 2 MB.
const MaxPhotoBytes = 2 << 20

// SetPhoto stores an uploaded product photo, replacing any previous one.
func (s *ProductService) SetPhoto(ctx context.Context, id int64, mimeType string, file []byte) (domain.Product, error) {
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
		return domain.Product{}, validationError("unsupported file type %q (want jpeg, png or webp)", mimeType)
	}
	if len(file) == 0 {
		return domain.Product{}, validationError("photo file is empty")
	}
	if len(file) > MaxPhotoBytes {
		return domain.Product{}, validationError("photo file exceeds %d MB limit", MaxPhotoBytes>>20)
	}

	existing, err := s.products.GetByID(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	path, err := writeFileRandom(s.photosDir, ext, file)
	if err != nil {
		return domain.Product{}, fmt.Errorf("save photo: %w", err)
	}
	updated, err := s.products.SetPhoto(ctx, id, path)
	if err != nil {
		_ = os.Remove(path)
		return domain.Product{}, err
	}
	if existing.ImagePath != "" && existing.ImagePath != path {
		_ = os.Remove(existing.ImagePath)
	}
	return updated, nil
}

// RemovePhoto clears the photo and deletes its file.
func (s *ProductService) RemovePhoto(ctx context.Context, id int64) (domain.Product, error) {
	existing, err := s.products.GetByID(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	updated, err := s.products.SetPhoto(ctx, id, "")
	if err != nil {
		return domain.Product{}, err
	}
	if existing.ImagePath != "" {
		if err := os.Remove(existing.ImagePath); err != nil && !os.IsNotExist(err) {
			return domain.Product{}, fmt.Errorf("remove photo file: %w", err)
		}
	}
	return updated, nil
}

// PhotoPath resolves the photo file for serving; mirrors LogoPath.
func (s *ProductService) PhotoPath(ctx context.Context, id int64) (string, string, error) {
	product, err := s.products.GetByID(ctx, id)
	if err != nil {
		return "", "", err
	}
	if product.ImagePath == "" {
		return "", "", validationError("product %d has no photo", id)
	}
	return product.ImagePath, detectMime(product.ImagePath), nil
}

// build validates and normalizes a ProductInput.
func (s *ProductService) build(ctx context.Context, in ProductInput) (domain.Product, error) {
	if err := validateRequiredString(in.Name, "name", 200); err != nil {
		return domain.Product{}, err
	}
	brand := strings.TrimSpace(in.Brand)
	unit := strings.ToLower(strings.TrimSpace(in.Unit))
	description := strings.TrimSpace(in.Description)
	if len(brand) > 120 {
		return domain.Product{}, validationError("brand must be at most 120 characters")
	}
	if len(unit) > 20 {
		return domain.Product{}, validationError("unit must be at most 20 characters")
	}
	if len(description) > 500 {
		return domain.Product{}, validationError("description must be at most 500 characters")
	}
	if in.CategoryID != nil {
		cat, err := s.categories.GetByID(ctx, *in.CategoryID)
		if err != nil {
			return domain.Product{}, fmt.Errorf("validate category: %w", err)
		}
		if cat.Kind != "product" {
			return domain.Product{}, validationError("category %d is not a product category", *in.CategoryID)
		}
	}
	return domain.Product{
		Name:        strings.TrimSpace(in.Name),
		Brand:       brand,
		Unit:        unit,
		CategoryID:  in.CategoryID,
		Description: description,
	}, nil
}
