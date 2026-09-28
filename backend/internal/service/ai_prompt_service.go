package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// promptKeyPattern constrains managed prompt keys: lowercase letters,
// digits and underscores, starting with a letter.
var promptKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// maxPromptContent caps a stored prompt body. The built-in defaults are
// around 6 KB; 20 KB leaves generous room for custom prompts.
const maxPromptContent = 20000

// AIPromptService manages the instruction texts sent to AI connectors.
// Content is resolved by the background processes (bill scanning, offer
// search) through ResolvePrompt; an empty or missing row falls back to the
// built-in default, so deleting a seeded prompt never breaks a process.
type AIPromptService struct {
	prompts    AIPromptStore
	categories CategoryStore
}

// NewAIPromptService wires the prompt workflow. The category store supplies
// the product taxonomy injected into the {{categories}} placeholder.
func NewAIPromptService(prompts AIPromptStore, categories CategoryStore) *AIPromptService {
	return &AIPromptService{prompts: prompts, categories: categories}
}

// AIPromptInput is the user-facing payload for updating a prompt. The key is
// deliberately absent: processes resolve prompts by key, so it is immutable
// after create.
type AIPromptInput struct {
	Name        string
	Description string
	// Content may be empty — resolve then uses the built-in default.
	Content string
}

// AIPromptCreateInput adds the (immutable) key for creation.
type AIPromptCreateInput struct {
	Key         string
	Name        string
	Description string
	Content     string
}

// AIPromptView is a prompt plus the process-agnostic extras the management UI
// needs: the raw built-in default ({{categories}} NOT expanded — the user
// edits the template, not a rendered snapshot) for the reset affordance, and
// whether the row currently resolves to it.
type AIPromptView struct {
	domain.AIPrompt
	DefaultContent string `json:"default_content"`
	UsesDefault    bool   `json:"uses_default"`
}

// List returns every prompt as a view.
func (s *AIPromptService) List(ctx context.Context) ([]AIPromptView, error) {
	prompts, err := s.prompts.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]AIPromptView, 0, len(prompts))
	for _, p := range prompts {
		views = append(views, s.view(p))
	}
	return views, nil
}

// Get returns one prompt as a view.
func (s *AIPromptService) Get(ctx context.Context, id int64) (AIPromptView, error) {
	p, err := s.prompts.GetByID(ctx, id)
	if err != nil {
		return AIPromptView{}, err
	}
	return s.view(p), nil
}

// Create validates and stores a new prompt.
func (s *AIPromptService) Create(ctx context.Context, in AIPromptCreateInput) (AIPromptView, error) {
	p, err := s.build(domain.AIPrompt{Key: in.Key, Name: in.Name, Description: in.Description, Content: in.Content})
	if err != nil {
		return AIPromptView{}, err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return AIPromptView{}, validationError("key is required")
	}
	if !promptKeyPattern.MatchString(key) {
		return AIPromptView{}, validationError("key %q must be 2-64 chars: lowercase letters, digits or underscores, starting with a letter", key)
	}
	p.Key = key
	created, err := s.prompts.Create(ctx, p)
	if err != nil {
		return AIPromptView{}, err
	}
	return s.view(created), nil
}

// Update rewrites name/description/content; the key is immutable.
func (s *AIPromptService) Update(ctx context.Context, id int64, in AIPromptInput) (AIPromptView, error) {
	p, err := s.build(domain.AIPrompt{Name: in.Name, Description: in.Description, Content: in.Content})
	if err != nil {
		return AIPromptView{}, err
	}
	p.ID = id
	updated, err := s.prompts.Update(ctx, p)
	if err != nil {
		return AIPromptView{}, err
	}
	return s.view(updated), nil
}

// Delete removes a prompt. Deleting a seeded key is safe: resolve falls back
// to the built-in default.
func (s *AIPromptService) Delete(ctx context.Context, id int64) error {
	return s.prompts.Delete(ctx, id)
}

// ResolvePrompt returns the prompt content for a process key: the stored
// content when present and non-empty, the built-in default otherwise, with
// {{categories}} expanded from the live product-category taxonomy. A missing
// row is a fallback, not an error; store errors propagate.
func (s *AIPromptService) ResolvePrompt(ctx context.Context, key string) (string, error) {
	content := ""
	if p, err := s.prompts.GetByKey(ctx, key); err == nil {
		content = strings.TrimSpace(p.Content)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("load prompt %q: %w", key, err)
	}
	if content == "" {
		content = defaultPrompt(key)
	}
	return renderPrompt(ctx, s.categories, content)
}

// renderPrompt expands the {{categories}} placeholder (if present) with the
// live product-kind category names. A nil category store leaves the content
// untouched (tests without a taxonomy).
func renderPrompt(ctx context.Context, categories CategoryStore, content string) (string, error) {
	if !strings.Contains(content, "{{categories}}") || categories == nil {
		return content, nil
	}
	list, err := categories.List(ctx)
	if err != nil {
		return "", fmt.Errorf("load categories for prompt: %w", err)
	}
	return strings.ReplaceAll(content, "{{categories}}", productCategoryList(list)), nil
}

// productCategoryList renders the {{categories}} placeholder content: the
// product-kind category names, comma-joined, with comma-containing names
// wrapped in double quotes (e.g. "Pasta, Rice & Grains") so the model reads
// them as single categories.
func productCategoryList(categories []domain.Category) string {
	parts := make([]string, 0, len(categories))
	for _, c := range categories {
		if c.Kind != "product" {
			continue
		}
		if strings.Contains(c.Name, ",") {
			parts = append(parts, `"`+c.Name+`"`)
		} else {
			parts = append(parts, c.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// view decorates a stored prompt with its built-in default and the
// uses-default flag. Rows for keys without a built-in (custom keys) get an
// empty default.
func (s *AIPromptService) view(p domain.AIPrompt) AIPromptView {
	def := defaultPrompt(p.Key)
	return AIPromptView{
		AIPrompt:       p,
		DefaultContent: def,
		UsesDefault:    strings.TrimSpace(p.Content) == "" && def != "",
	}
}

// build validates and normalizes the user-editable fields of a prompt.
func (s *AIPromptService) build(p domain.AIPrompt) (domain.AIPrompt, error) {
	if err := validateRequiredString(p.Name, "name", 100); err != nil {
		return domain.AIPrompt{}, err
	}
	description := strings.TrimSpace(p.Description)
	if len(description) > 500 {
		return domain.AIPrompt{}, validationError("description must be at most 500 characters")
	}
	content := p.Content
	if len(content) > maxPromptContent {
		return domain.AIPrompt{}, validationError("content must be at most %d characters", maxPromptContent)
	}
	return domain.AIPrompt{
		Key:         p.Key,
		Name:        strings.TrimSpace(p.Name),
		Description: description,
		Content:     content,
	}, nil
}
