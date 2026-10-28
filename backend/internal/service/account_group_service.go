package service

import (
	"context"
	"strings"

	"home-finance-planner/backend/internal/domain"
)

// AccountGroupService implements the folder-style account groups (Phase 4):
// presentation only, zero money semantics. Membership lives on
// accounts.group_id; deleting a group ungroups its accounts (SET NULL).
type AccountGroupService struct{ groups AccountGroupStore }

// AccountGroupInput is the user-facing create/update payload.
type AccountGroupInput struct {
	Name string
}

func (s *AccountGroupService) List(ctx context.Context) ([]domain.AccountGroup, error) {
	return s.groups.List(ctx)
}

func (s *AccountGroupService) Get(ctx context.Context, id int64) (domain.AccountGroup, error) {
	return s.groups.GetByID(ctx, id)
}

func (s *AccountGroupService) Create(ctx context.Context, in AccountGroupInput) (domain.AccountGroup, error) {
	g, err := buildAccountGroup(in)
	if err != nil {
		return domain.AccountGroup{}, err
	}
	return s.groups.Create(ctx, g)
}

func (s *AccountGroupService) Update(ctx context.Context, id int64, in AccountGroupInput) (domain.AccountGroup, error) {
	g, err := buildAccountGroup(in)
	if err != nil {
		return domain.AccountGroup{}, err
	}
	g.ID = id
	return s.groups.Update(ctx, g)
}

// Delete ungroups the group's accounts (the FK cascade); the accounts and
// their transactions are untouched.
func (s *AccountGroupService) Delete(ctx context.Context, id int64) error {
	return s.groups.Delete(ctx, id)
}

func buildAccountGroup(in AccountGroupInput) (domain.AccountGroup, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.AccountGroup{}, validationError("name is required")
	}
	if len(name) > 100 {
		return domain.AccountGroup{}, validationError("name must be at most 100 characters")
	}
	return domain.AccountGroup{Name: name}, nil
}
