package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// ProductService manages the product catalogue. Products are created only by
// the bill workflow (find-or-created from bill item names at confirm time);
// this service edits them and manages their photos. Edits update the
// products row only — linked bill/transaction lines are immutable snapshots.
type ProductService struct {
	products   ProductStore
	categories CategoryStore
	photosDir  string
	// mappings is the product-name normalization memory: NormalizeName looks
	// raw texts up in it, and the backfill job fills it from existing product
	// names. nil disables the endpoints (tests).
	mappings ProductMappingStore
	// normalizer runs the text-only AI calls of the backfill job
	// (implemented by the extractor). nil disables the job.
	normalizer TextNormalizer
	// providers resolves the AI connector used by the backfill job (the same
	// default_for_bills policy as bill scans). nil disables the job.
	providers *SettingsService
	// prompts resolves the managed product_normalization prompt. nil falls
	// back to the built-in default.
	prompts PromptResolver
	// timeout bounds one AI call of the backfill job.
	timeout time.Duration
	// batchSize is the working batch size of the backfill job: it starts at
	// normalizationBatch and halves whenever a call outlives timeout (slow
	// local models need minutes to emit a full batch), so later batches of
	// the same process fit the deadline. Only the job's single-flight run
	// touches it.
	batchSize int
	log       *slog.Logger
}

// ProductFilters re-exports the shared domain filter type.
type ProductFilters = domain.ProductFilters

// ProductInput is the user-facing payload for product update; the photo is
// managed separately through SetPhoto/RemovePhoto. StandardName is the
// optional normalization-mapping decision for the product's raw name — nil
// means the client didn't touch the field.
type ProductInput struct {
	Name         string
	Brand        string
	Unit         string
	CategoryID   *int64
	Description  string
	StandardName *string
}

// NewProductService wires the product workflow. photosDir is where photo
// files are stored. The remaining deps power the normalization layer:
// mappings (memory), normalizer (text-only AI calls), providers (connector
// resolution, default_for_bills policy), prompts (managed
// product_normalization prompt) and the per-call timeout. A nil normalizer
// or providers disables the backfill job; a nil mappings disables the
// normalization endpoints. A job left "running" by a previous process is
// marked failed at boot — the user simply runs it again.
func NewProductService(
	products ProductStore,
	categories CategoryStore,
	photosDir string,
	mappings ProductMappingStore,
	normalizer TextNormalizer,
	providers *SettingsService,
	prompts PromptResolver,
	timeout time.Duration,
	log *slog.Logger,
) *ProductService {
	if log == nil {
		log = slog.Default()
	}
	s := &ProductService{
		products:   products,
		categories: categories,
		photosDir:  photosDir,
		mappings:   mappings,
		normalizer: normalizer,
		providers:  providers,
		prompts:    prompts,
		timeout:    timeout,
		batchSize:  normalizationBatch,
		log:        log,
	}
	if s.mappings != nil {
		if job, err := s.mappings.GetJob(context.Background()); err == nil && job.Status == domain.ProductNormalizationRunning {
			job.Status = domain.ProductNormalizationFailed
			job.Error = "interrupted by a restart — run the analysis again"
			if err := s.mappings.UpdateJob(context.Background(), job); err != nil {
				log.Warn("reset stale product normalization job", "error", err)
			}
		}
	}
	return s
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

// Update rewrites a product's editable fields on the products row only;
// historical bill/transaction lines keep their snapshot values. The photo is
// untouched (managed by SetPhoto/RemovePhoto). A submitted standard_name
// also learns the product's normalization-mapping decision (source user),
// mirroring the bill workflow: untouched saves write nothing, renames carry
// the decision to the new raw name, and clearing the field records identity.
func (s *ProductService) Update(ctx context.Context, id int64, in ProductInput) (domain.Product, error) {
	submitted, submittedSet := "", false
	if in.StandardName != nil {
		submitted = strings.TrimSpace(*in.StandardName)
		submittedSet = true
		if len(submitted) > 200 {
			return domain.Product{}, validationError("standard_name must be at most %d characters", 200)
		}
	}
	product, err := s.build(ctx, in)
	if err != nil {
		return domain.Product{}, err
	}
	product.ID = id

	// The pre-edit row holds what the edit form showed: the mapping join
	// (old.StandardName), the prefilled category and the raw name the
	// standard field was prefilled with.
	var old domain.Product
	haveOld := false
	if s.mappings != nil {
		if o, err := s.products.GetByID(ctx, id); err == nil {
			old, haveOld = o, true
		} else {
			s.log.Warn("read product before mapping learn", "id", id, "error", err)
		}
	}
	updated, err := s.products.Update(ctx, product)
	if err != nil {
		return domain.Product{}, err
	}
	if !haveOld || !s.learnMapping(ctx, old, updated, submitted, submittedSet, in.CategoryID) {
		return updated, nil
	}
	// The mapping write refreshed the standard_name join — serve the fresh
	// row (the read must not fail the already-committed save).
	fresh, err := s.products.GetByID(ctx, id)
	if err != nil {
		s.log.Warn("reread product after mapping learn", "id", id, "error", err)
		return updated, nil
	}
	return fresh, nil
}

// learnMapping records the product edit's normalization decision (source
// user), the product-side twin of the bill workflow's learnMappingOverrides.
// submitted is the trimmed standard_name; submittedSet distinguishes an
// explicitly cleared field ("" → identity) from an absent one (nil → keep
// the prior). Returns true when a mapping write was attempted, so the
// caller can re-read the row for the fresh standard_name join.
//
// Unmapped products only record an explicit new standard — the untouched
// prefill or an absent field writes nothing even on a rename, leaving the
// name open for AI suggestions. Mapped products write when the standard
// changed, when the name changed (the decision moves to the new raw key)
// or when the category changed (the memory stays in sync with the row).
func (s *ProductService) learnMapping(ctx context.Context, old, updated domain.Product, submitted string, submittedSet bool, categoryID *int64) bool {
	raw := updated.Name
	if old.StandardName == "" {
		if !submittedSet || submitted == "" || strings.EqualFold(submitted, old.Name) {
			return false
		}
	} else {
		if !submittedSet {
			submitted = old.StandardName
		}
		if submitted == "" {
			submitted = raw // cleared field: record identity, like bill lines
		}
		if strings.EqualFold(submitted, old.StandardName) &&
			strings.EqualFold(old.Name, raw) &&
			sameInt64Ptr(old.CategoryID, categoryID) {
			return false
		}
	}
	if _, err := s.mappings.Upsert(ctx, domain.ProductNameMapping{
		RawName:      raw,
		StandardName: submitted,
		CategoryID:   categoryID,
		Source:       domain.MappingSourceUser,
	}); err != nil {
		s.log.Warn("learn product mapping override", "raw", raw, "error", err)
	}
	return true
}

// Merge plan reasons, mirrored by the frontend's ProductMergeCheck type.
const (
	// MergeDifferentStores: the two products were bought at disjoint store
	// sets — same product bought in different stores.
	MergeDifferentStores = "different_stores"
	// MergeSameStore: both were bought at (at least one) shared store. A
	// differing latest price is just an updated price for the same product,
	// so this is still mergeable — the user picks which record to keep and
	// the price history combines under it.
	MergeSameStore = "same_store"
)

// ProductMergeCheck is the rename pre-check payload: the matched product and
// the merge plan the UI should confirm. Match is absent when the new name
// matches nothing (or the product itself) — then no merge is involved.
type ProductMergeCheck struct {
	Match     *domain.Product `json:"match,omitempty"`
	Mergeable bool            `json:"mergeable"`
	Reason    string          `json:"reason,omitempty"`

	// Same-store context: the shared store the comparison is taken at, plus
	// each product's latest price there (currency included, the two may have
	// been priced in different currencies).
	StoreName   string                    `json:"store_name,omitempty"`
	SourcePrice *domain.ProductStorePrice `json:"source_store_price,omitempty"`
	TargetPrice *domain.ProductStorePrice `json:"target_store_price,omitempty"`

	// Store names each product was bought at, for the different-stores dialog.
	SourceStores []string `json:"source_stores,omitempty"`
	TargetStores []string `json:"target_stores,omitempty"`
}

// ProductMergeInput drives Merge. Product carries the pending edit from the
// open modal; it is applied when the source is kept and discarded when the
// user keeps the target (the target's data wins).
type ProductMergeInput struct {
	MergeWith  int64
	KeepTarget bool
	Product    *ProductInput
}

// CheckMerge reports what would happen if the product were renamed to newName:
// no match, or a match plus the merge plan (which store situation the two
// products are in, and the comparison data the dialogs display).
func (s *ProductService) CheckMerge(ctx context.Context, id int64, newName string) (ProductMergeCheck, error) {
	source, err := s.products.GetByID(ctx, id)
	if err != nil {
		return ProductMergeCheck{}, err
	}
	name := strings.TrimSpace(newName)
	if strings.EqualFold(name, source.Name) {
		return ProductMergeCheck{}, nil // unchanged name can only match itself
	}
	target, err := s.products.FindByName(ctx, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ProductMergeCheck{}, nil
		}
		return ProductMergeCheck{}, err
	}
	if target.ID == source.ID {
		return ProductMergeCheck{}, nil
	}
	return s.mergePlan(ctx, source, target)
}

// Merge folds the source product into another one: the loser's bill items are
// redirected to the keeper (their purchase history — including prices —
// combines under the kept product) and the loser row is deleted. The keeper's
// final fields follow the keep choice; empty fields always adopt the dropped
// product's values so no information is lost.
func (s *ProductService) Merge(ctx context.Context, id int64, in ProductMergeInput) (domain.Product, error) {
	source, err := s.products.GetByID(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	dropTarget, err := s.products.GetByID(ctx, in.MergeWith)
	if err != nil {
		return domain.Product{}, err
	}
	if dropTarget.ID == source.ID {
		return domain.Product{}, validationError("cannot merge a product with itself")
	}

	// Re-run the plan: conditions may have changed since the UI's check
	// (concurrent renames/edits), so a stale confirm never merges.
	plan, err := s.mergePlan(ctx, source, dropTarget)
	if err != nil {
		return domain.Product{}, err
	}
	if !plan.Mergeable {
		return domain.Product{}, conflictError("products can no longer be merged: %s", plan.Reason)
	}

	keep, drop := source, dropTarget
	var final domain.Product
	if in.KeepTarget {
		keep, drop = dropTarget, source
		// The user chose the target's data: its stored values win, the
		// source only fills the gaps. The name stays the target's casing.
		final = fillMissing(dropTarget, source)
		final.ID = keep.ID
	} else {
		built, err := s.build(ctx, derefInput(in.Product))
		if err != nil {
			return domain.Product{}, err
		}
		// The rename target must still be the product this merge was
		// confirmed for.
		if !strings.EqualFold(built.Name, dropTarget.Name) {
			return domain.Product{}, conflictError(
				"product %q no longer matches %q; re-check the merge", built.Name, dropTarget.Name)
		}
		final = built
		final.ID = keep.ID
		final = fillMissing(final, dropTarget)
	}

	merged, err := s.products.Merge(ctx, keep.ID, drop.ID, final)
	if err != nil {
		return domain.Product{}, err
	}
	merged, err = s.reconcilePhotos(ctx, keep, drop)
	if err != nil {
		return domain.Product{}, err
	}
	return merged, nil
}

// reconcilePhotos settles the photo files after a merge: a keeper without a
// photo adopts the dropped product's file (through SetPhoto), otherwise the
// dropped product's file is deleted.
func (s *ProductService) reconcilePhotos(ctx context.Context, keep, drop domain.Product) (domain.Product, error) {
	if drop.ImagePath == "" {
		return s.products.GetByID(ctx, keep.ID)
	}
	if keep.ImagePath == "" {
		updated, err := s.products.SetPhoto(ctx, keep.ID, drop.ImagePath)
		if err != nil {
			return domain.Product{}, err
		}
		return updated, nil
	}
	if drop.ImagePath != keep.ImagePath {
		_ = os.Remove(drop.ImagePath)
	}
	return s.products.GetByID(ctx, keep.ID)
}

// mergePlan compares the two products' per-store purchase summaries and
// decides the merge kind. A nil store id (bills without a store) is its own
// store key, so two storeless products still count as sharing one.
func (s *ProductService) mergePlan(ctx context.Context, source, target domain.Product) (ProductMergeCheck, error) {
	sourceRows, err := s.products.StorePurchaseSummary(ctx, source.ID)
	if err != nil {
		return ProductMergeCheck{}, err
	}
	targetRows, err := s.products.StorePurchaseSummary(ctx, target.ID)
	if err != nil {
		return ProductMergeCheck{}, err
	}

	check := ProductMergeCheck{Match: &target, Mergeable: false}
	check.SourceStores = storeNames(sourceRows)
	check.TargetStores = storeNames(targetRows)

	targetByKey := make(map[string]domain.ProductStorePrice, len(targetRows))
	for _, row := range targetRows {
		targetByKey[storeKey(row.StoreID)] = row
	}

	// Shared stores in most-recent-first order (both lists come sorted that
	// way); the first one carries the comparison the dialog displays.
	for _, sourceRow := range sourceRows {
		targetRow, ok := targetByKey[storeKey(sourceRow.StoreID)]
		if !ok {
			continue
		}
		check.Mergeable = true
		check.Reason = MergeSameStore
		if check.StoreName == "" {
			check.StoreName = sourceRow.StoreName
			sourcePrice := sourceRow
			check.SourcePrice = &sourcePrice
			targetPrice := targetRow
			check.TargetPrice = &targetPrice
		}
	}
	if check.Mergeable {
		return check, nil
	}
	// No shared store: the same product bought in different stores — always
	// mergeable, the history simply groups under the keeper.
	check.Mergeable = true
	check.Reason = MergeDifferentStores
	return check, nil
}

// storeKey maps a nullable store id to a map key ("none" for bills without a
// store).
func storeKey(storeID *int64) string {
	if storeID == nil {
		return "none"
	}
	return fmt.Sprintf("%d", *storeID)
}

// storeNames lists the distinct store names a product was bought at.
func storeNames(rows []domain.ProductStorePrice) []string {
	names := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !seen[row.StoreName] {
			seen[row.StoreName] = true
			names = append(names, row.StoreName)
		}
	}
	return names
}

// fillMissing returns a with b's values adopted wherever a has none — brand,
// unit, category and description only; the name and identity stay a's.
func fillMissing(a, b domain.Product) domain.Product {
	if a.Brand == "" {
		a.Brand = b.Brand
	}
	if a.Unit == "" {
		a.Unit = b.Unit
	}
	if a.CategoryID == nil {
		a.CategoryID = b.CategoryID
	}
	if a.Description == "" {
		a.Description = b.Description
	}
	return a
}

// derefInput tolerates a missing pending edit (the modal always sends one,
// but the API accepts the call without it).
func derefInput(in *ProductInput) ProductInput {
	if in == nil {
		return ProductInput{}
	}
	return *in
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

// normalizationBatch is the initial bound on one AI call of the backfill
// job: small enough for any model's context window and for a single-call
// retry, large enough that a fast connector covers the catalogue in a few
// round trips. Slow local models that outlive the per-call deadline make
// the job halve its working batch size instead of failing (see batchSize).
const normalizationBatch = 40

// ProductNormalizeResult is the GET /products/normalize response: does this
// raw text already resolve to a standardized name (+ category)? matched is
// false when nothing is remembered yet — the caller keeps the raw name.
type ProductNormalizeResult struct {
	RawName      string               `json:"raw_name"`
	StandardName string               `json:"standard_name,omitempty"`
	CategoryID   *int64               `json:"category_id,omitempty"`
	CategoryName string               `json:"category_name,omitempty"`
	Source       domain.MappingSource `json:"source,omitempty"`
	Matched      bool                 `json:"matched"`
}

// NormalizeName looks a raw text up in the normalization memory. It never
// calls the AI — the lookup is the memory's whole point.
func (s *ProductService) NormalizeName(ctx context.Context, raw string) (ProductNormalizeResult, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ProductNormalizeResult{}, validationError("name must not be empty")
	}
	res := ProductNormalizeResult{RawName: name}
	if s.mappings == nil {
		return res, nil
	}
	m, err := s.mappings.FindByRawName(ctx, name)
	if errors.Is(err, domain.ErrNotFound) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	res.StandardName = m.StandardName
	res.CategoryID = m.CategoryID
	res.CategoryName = m.CategoryName
	res.Source = m.Source
	res.Matched = true
	return res, nil
}

// NormalizationStatus reports the state of the "analyze existing products"
// job — status, progress counters and the last error.
func (s *ProductService) NormalizationStatus(ctx context.Context) (domain.ProductNormalizationJob, error) {
	if s.mappings == nil {
		return domain.ProductNormalizationJob{}, validationError("product normalization is not available")
	}
	return s.mappings.GetJob(ctx)
}

// RunNormalization starts the backfill job: every product whose raw name has
// no mapping yet is sent to the AI (in batches) to be standardized, and the
// results are recorded in the normalization memory. Products themselves are
// never modified. The job runs in the background; poll NormalizationStatus.
func (s *ProductService) RunNormalization(ctx context.Context) (domain.ProductNormalizationJob, error) {
	if s.mappings == nil || s.normalizer == nil || s.providers == nil {
		return domain.ProductNormalizationJob{}, validationError("product normalization is not available")
	}
	job, err := s.mappings.GetJob(ctx)
	if err != nil {
		return domain.ProductNormalizationJob{}, err
	}
	if job.Status == domain.ProductNormalizationRunning {
		return domain.ProductNormalizationJob{}, fmt.Errorf("an analysis is already running — wait for it to finish: %w", domain.ErrConflict)
	}
	// Resolve the connector up front so the user gets an actionable error
	// instead of a job that immediately fails in the background.
	if _, err := s.providers.DefaultBillProvider(ctx); err != nil {
		return domain.ProductNormalizationJob{}, err
	}
	total, err := s.mappings.CountUnmappedProductNames(ctx)
	if err != nil {
		return domain.ProductNormalizationJob{}, err
	}
	job.Status = domain.ProductNormalizationRunning
	job.TotalNames = total
	job.ProcessedNames = 0
	job.MappedNames = 0
	job.Error = ""
	if err := s.mappings.UpdateJob(ctx, job); err != nil {
		return domain.ProductNormalizationJob{}, err
	}
	go s.runNormalizationJob()
	return job, nil
}

// runNormalizationJob drains the unmapped product names in batches until the
// catalogue is covered, then marks the job done. Any failure marks it failed
// with the error — the job is idempotent, so the user simply runs it again.
//
// Names the model leaves unanswered stay unmapped, so they would come back in
// the next batch forever (one AI call per round trip). Every raw text gets a
// single ask per run: `asked` excludes them from later batches, and the job
// finishes once everything still unmapped has already been asked.
func (s *ProductService) runNormalizationJob() {
	ctx := context.Background()
	job, err := s.mappings.GetJob(ctx)
	if err != nil {
		s.log.Error("product normalization job lost its status row", "error", err)
		return
	}
	asked := make(map[string]bool)
	for {
		remaining, err := s.mappings.CountUnmappedProductNames(ctx)
		if err != nil {
			s.failNormalizationJob(ctx, job, err)
			return
		}
		// Nothing left, or everything left was asked (and skipped by the
		// model) in an earlier batch of this run.
		if remaining == 0 || remaining <= int64(len(asked)) {
			job.Status = domain.ProductNormalizationDone
			job.Error = ""
			if err := s.mappings.UpdateJob(ctx, job); err != nil {
				s.log.Error("finish product normalization job", "error", err)
			}
			s.log.Info("product normalization job done",
				"processed", job.ProcessedNames, "mapped", job.MappedNames)
			return
		}
		// Already-asked names sort ahead of fresh ones (id order), so widen
		// the limit to keep full fresh batches coming through. The limit
		// follows the working batch size, which halves when the model is
		// slower than the per-call deadline.
		names, err := s.mappings.UnmappedProductNames(ctx, s.batchSize+len(asked))
		if err != nil {
			s.failNormalizationJob(ctx, job, err)
			return
		}
		var fresh []domain.Product
		for _, p := range names {
			key := strings.ToLower(p.Name)
			if asked[key] {
				continue
			}
			asked[key] = true
			fresh = append(fresh, p)
		}
		if err := s.normalizeBatch(ctx, fresh, &job); err != nil {
			s.failNormalizationJob(ctx, job, err)
			return
		}
	}
}

// normalizeBatch standardizes one batch of product names through the AI and
// records the results (source 'ai'). Progress counters are persisted after
// every successful call so the status endpoint can show live progress.
//
// A call that outlives the per-call deadline — a slow local model can take
// longer to emit a full batch than a receipt read — is retried on half the
// batch, down to a single name, and the halved size is remembered for the
// rest of the process. Only a timeout is retried; a batch whose single
// remaining name still times out fails the job with an actionable error.
func (s *ProductService) normalizeBatch(ctx context.Context, products []domain.Product, job *domain.ProductNormalizationJob) error {
	// A batch larger than the working size is split up front: a slow model
	// already taught the job the deadline-fit size (the second half of a
	// timeout-driven split), so don't burn another timeout on it.
	if len(products) > s.batchSize && len(products) > 1 {
		mid := len(products) / 2
		if err := s.normalizeBatch(ctx, products[:mid], job); err != nil {
			return err
		}
		return s.normalizeBatch(ctx, products[mid:], job)
	}
	provider, err := s.providers.DefaultBillProvider(ctx)
	if err != nil {
		return err
	}
	prompt, err := s.resolveNormalizationPrompt(ctx)
	if err != nil {
		return err
	}

	answers, err := s.normalizeCall(ctx, provider, prompt, products)
	if err == nil {
		return s.recordNormalized(ctx, products, answers, job)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if len(products) == 1 {
		return fmt.Errorf("AI call timed out after %s even for a single name — use a faster model or raise LLM_TIMEOUT: %w", s.timeout, err)
	}
	mid := len(products) / 2
	s.batchSize = mid
	if err := s.normalizeBatch(ctx, products[:mid], job); err != nil {
		return err
	}
	return s.normalizeBatch(ctx, products[mid:], job)
}

// normalizeCall runs one prompt-only AI call for the given products and
// returns the model's raw→standard answers. The whole call — prompt,
// generation and transport — is bounded by the service timeout.
func (s *ProductService) normalizeCall(ctx context.Context, provider domain.AIProvider, prompt string, products []domain.Product) ([]domain.ProductNameMapping, error) {
	rawNames := make([]string, len(products))
	for i, p := range products {
		rawNames[i] = p.Name
	}
	encoded, err := json.Marshal(rawNames)
	if err != nil {
		return nil, fmt.Errorf("encode product names: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.normalizer.NormalizeNames(callCtx, provider, prompt+string(encoded))
}

// recordNormalized stores the answers of one successful call (Create, not
// Upsert: a mapping that appeared while the job was queued — a scan, a
// manual entry — already holds a reviewed decision) and persists the
// progress counters.
func (s *ProductService) recordNormalized(ctx context.Context, products []domain.Product, answers []domain.ProductNameMapping, job *domain.ProductNormalizationJob) error {
	byRaw := make(map[string]string, len(answers))
	for _, a := range answers {
		byRaw[strings.ToLower(a.RawName)] = a.StandardName
	}
	mapped := int64(0)
	for _, p := range products {
		standard, ok := byRaw[strings.ToLower(p.Name)]
		if !ok {
			continue
		}
		_, err := s.mappings.Create(ctx, domain.ProductNameMapping{
			RawName:      p.Name,
			StandardName: standard,
			CategoryID:   p.CategoryID,
			Source:       domain.MappingSourceAI,
		})
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			s.log.Warn("record normalized product name", "raw", p.Name, "error", err)
			continue
		}
		mapped++
	}
	job.ProcessedNames += int64(len(products))
	job.MappedNames += mapped
	return s.mappings.UpdateJob(ctx, *job)
}

// resolveNormalizationPrompt returns the product-normalization prompt head:
// the managed ai_prompts row when the resolver is wired, the built-in
// default otherwise (tests).
func (s *ProductService) resolveNormalizationPrompt(ctx context.Context) (string, error) {
	if s.prompts != nil {
		prompt, err := s.prompts.ResolvePrompt(ctx, domain.PromptKeyProductNormalization)
		if err != nil {
			return "", fmt.Errorf("resolve product normalization prompt: %w", err)
		}
		return prompt, nil
	}
	return defaultPrompt(domain.PromptKeyProductNormalization), nil
}

// failNormalizationJob records a terminal job failure with the last progress.
func (s *ProductService) failNormalizationJob(ctx context.Context, job domain.ProductNormalizationJob, cause error) {
	job.Status = domain.ProductNormalizationFailed
	job.Error = cause.Error()
	if err := s.mappings.UpdateJob(ctx, job); err != nil {
		s.log.Error("fail product normalization job", "cause", cause, "error", err)
	}
	s.log.Warn("product normalization job failed", "processed", job.ProcessedNames, "error", cause)
}
