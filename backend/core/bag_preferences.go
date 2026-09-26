package core

import (
	"context"
	"errors"
	"slices"
)

// Bag preferences belong to the authenticated user, never to the shared bag.
// The existing family-scoped record store persists them in both backends.
type BagPreferences struct {
	FamilyID     string   `json:"family_id"`
	UserID       string   `json:"user_id"`
	PinnedBagIDs []string `json:"pinned_bag_ids"`
}

type SetBagPinParams struct {
	RequestID string `json:"request_id"`
	BagID     string `json:"bag_id"`
	Pinned    *bool  `json:"pinned"`
}

func loadBagPreferences(tx Tx, actor Principal) (BagPreferences, error) {
	prefs := BagPreferences{FamilyID: actor.FamilyID, UserID: actor.UserID, PinnedBagIDs: []string{}}
	err := tx.Get("bag_preferences", actor.UserID, &prefs)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	return prefs, err
}

func (c *Core) getBagPreferences(ctx context.Context, _ EmptyParams) (any, error) {
	return c.read(ctx, func(tx Tx, actor Principal) (any, error) {
		return loadBagPreferences(tx, actor)
	})
}

func (c *Core) setBagPin(ctx context.Context, p SetBagPinParams) (any, error) {
	return c.mutate(ctx, "set_bag_pin", p.RequestID, p, nil, func(tx Tx, actor Principal) (any, string, []string, error) {
		if p.Pinned == nil {
			return nil, "", nil, E("validation_error", "pinned is required")
		}
		if _, err := loadBag(tx, p.BagID, *p.Pinned); err != nil {
			return nil, "", nil, err
		}
		prefs, err := loadBagPreferences(tx, actor)
		if err != nil {
			return nil, "", nil, err
		}
		index := slices.Index(prefs.PinnedBagIDs, p.BagID)
		if *p.Pinned && index < 0 {
			prefs.PinnedBagIDs = append(prefs.PinnedBagIDs, p.BagID)
		} else if !*p.Pinned && index >= 0 {
			prefs.PinnedBagIDs = slices.Delete(prefs.PinnedBagIDs, index, index+1)
		}
		if err := tx.Put("bag_preferences", actor.UserID, prefs); err != nil {
			return nil, "", nil, err
		}
		return prefs, p.BagID, nil, nil
	})
}
