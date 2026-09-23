package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
)

// DesktopOwner is reserved for the desktop process's private stdio controller.
// It must never be exposed through HTTP or MCP. Local filesystem access already
// grants control over this database; no owner credential leaves the process.
func (manager *Manager) DesktopOwner(ctx context.Context) (state.Actor, error) {
	if err := ctx.Err(); err != nil {
		return state.Actor{}, err
	}
	row := struct {
		ID          string `db:"id"`
		DisplayName string `db:"display_name"`
		DeviceName  string `db:"device_name"`
	}{}
	err := manager.app.DB().NewQuery(`SELECT id, display_name, device_name FROM state_actors WHERE kind = 'owner'`).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		credential, err := manager.BootstrapOwner(ctx, manager.bootstrapToken, OwnerBootstrapRequest{
			DisplayName: "State Server", DeviceName: "Mac",
		})
		return credential.Actor, err
	}
	if err != nil {
		return state.Actor{}, err
	}
	return state.Actor{ID: row.ID, Kind: state.ActorKindOwner, DisplayName: row.DisplayName, DeviceName: row.DeviceName}, nil
}

// DesktopPairingAvailable lets the local UI discard expired or consumed codes.
func (manager *Manager) DesktopPairingAvailable(ctx context.Context, owner state.Actor, code string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if owner.Kind != state.ActorKindOwner {
		return false, state.ErrForbidden
	}
	row := struct {
		Count int `db:"count"`
	}{}
	err := manager.app.DB().NewQuery(`SELECT COUNT(*) AS count FROM state_pairing_codes
		WHERE code_hash = {:hash} AND created_by = {:owner} AND used_at IS NULL AND expires_at > {:now}`).Bind(dbx.Params{
		"hash": hashSecret(normalizePairingCode(code)), "owner": owner.ID, "now": formatTime(manager.clock()),
	}).One(&row)
	return row.Count == 1, err
}
