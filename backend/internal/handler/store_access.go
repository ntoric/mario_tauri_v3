package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"cafe-backend/internal/middleware"
	"cafe-backend/internal/models"
)

// canAccessStore reports whether the caller may read or write data belonging
// to storeID: superadmin → all stores, business_owner → stores in
// user_stores, business_admin/staff → only their assigned store.
func (h *Handler) canAccessStore(ctx context.Context, claims *middleware.UserClaims, storeID string) bool {
	switch claims.Role {
	case "superadmin":
		return true
	case "business_owner":
		stores, err := h.Repo.User.GetUserStores(ctx, claims.ID)
		if err != nil {
			return false
		}
		for _, s := range stores {
			if s.ID == storeID {
				return true
			}
		}
		return false
	default: // business_admin, staff
		return storeID != "" && claims.StoreID == storeID
	}
}

// resolveTargetStore resolves the effective store for a request — the
// requested storeId, falling back to the caller's assigned store — and
// verifies the caller may access it. Writes the error response and returns
// ok=false when the store is missing or out of scope.
func (h *Handler) resolveTargetStore(w http.ResponseWriter, r *http.Request, claims *middleware.UserClaims, requested string) (string, bool) {
	target := requested
	if target == "" {
		target = claims.StoreID
	}
	if target == "" {
		h.writeError(w, http.StatusBadRequest, "Store ID required")
		return "", false
	}
	if !h.canAccessStore(r.Context(), claims, target) {
		h.writeError(w, http.StatusForbidden, "Access denied to this store")
		return "", false
	}
	return target, true
}

// checkEntityAccess verifies the row identified by table/id belongs to a
// store the caller can access. `table` must be a fixed literal (never user
// input). Writes 404/403 and returns false on failure.
func (h *Handler) checkEntityAccess(w http.ResponseWriter, r *http.Request, claims *middleware.UserClaims, table, id string) bool {
	sid, err := h.Repo.Store.EntityStoreID(r.Context(), table, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.writeError(w, http.StatusNotFound, "Not found")
		} else {
			h.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return false
	}
	if sid == "" || !h.canAccessStore(r.Context(), claims, sid) {
		h.writeError(w, http.StatusForbidden, "Access denied to this store")
		return false
	}
	return true
}

// refInStore verifies a referenced entity exists AND belongs to the given
// store — prevents cross-store foreign keys (e.g. store A's order pointing
// at store B's table or items).
func (h *Handler) refInStore(ctx context.Context, table, id, storeID string) bool {
	sid, err := h.Repo.Store.EntityStoreID(ctx, table, id)
	return err == nil && sid == storeID
}

// checkOrderRefs validates that the table and every item an order write
// references belong to the same store as the order. Writes the error and
// returns false on failure.
func (h *Handler) checkOrderRefs(w http.ResponseWriter, r *http.Request, storeID, tableID string, items []models.OrderItem) bool {
	if tableID != "" && !h.refInStore(r.Context(), "tables", tableID, storeID) {
		h.writeError(w, http.StatusBadRequest, "The selected table does not belong to this store")
		return false
	}
	for _, it := range items {
		if it.ItemID != "" && !h.refInStore(r.Context(), "items", it.ItemID, storeID) {
			h.writeError(w, http.StatusBadRequest, "A menu item in this order belongs to another store")
			return false
		}
	}
	return true
}

// canManageUser reports whether the caller may modify/delete the target
// user: superadmin → any, business_owner → users in their stores (or
// themselves, or users sharing a user_stores membership), business_admin →
// users in their own store, staff → nobody.
func (h *Handler) canManageUser(ctx context.Context, claims *middleware.UserClaims, target *models.User) bool {
	switch claims.Role {
	case "superadmin":
		return true
	case "business_admin":
		return target.StoreID != "" && target.StoreID == claims.StoreID
	case "business_owner":
		if target.ID == claims.ID {
			return true
		}
		stores, err := h.Repo.User.GetUserStores(ctx, claims.ID)
		if err != nil {
			return false
		}
		owned := make(map[string]bool, len(stores))
		for _, s := range stores {
			owned[s.ID] = true
		}
		if target.StoreID != "" && owned[target.StoreID] {
			return true
		}
		shared, err := h.Repo.User.SharesStore(ctx, target.ID, claims.ID)
		return err == nil && shared
	default:
		return false
	}
}

// callerStoreIDs returns the store ids a business owner may assign users to.
func (h *Handler) callerStoreIDs(ctx context.Context, userID string) map[string]bool {
	stores, err := h.Repo.User.GetUserStores(ctx, userID)
	set := make(map[string]bool, len(stores))
	if err != nil {
		return set
	}
	for _, s := range stores {
		set[s.ID] = true
	}
	return set
}
