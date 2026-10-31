package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// oauthStateTTL is how long a connect link stays valid.
const oauthStateTTL = 10 * time.Minute

// DriveRow is the one Google Drive connection. RefreshTokenEnc is the sealed
// token ("v1:<base64>"); the store never sees it in the clear. FolderID and
// FolderName are empty until a folder is chosen (stored NULL). State is
// "connected" or "expired".
type DriveRow struct {
	RefreshTokenEnc, Email, FolderID, FolderName, State, ConnectedBy string
	ConnectedAt                                                      time.Time
}

// Drive returns the connection; ok is false when none is stored.
func (s *Store) Drive(ctx context.Context) (row DriveRow, ok bool, err error) {
	var (
		folderID, folderName sql.NullString
		at                   string
	)
	err = s.db.QueryRowContext(ctx,
		`SELECT refresh_token_enc, email, folder_id, folder_name, state, connected_by, connected_at FROM drive WHERE id = 1`).
		Scan(&row.RefreshTokenEnc, &row.Email, &folderID, &folderName, &row.State, &row.ConnectedBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return DriveRow{}, false, nil
	}
	if err != nil {
		return DriveRow{}, false, fmt.Errorf("store: read drive: %w", err)
	}
	row.FolderID, row.FolderName = folderID.String, folderName.String
	if row.ConnectedAt, err = parseTime(at); err != nil {
		return DriveRow{}, false, fmt.Errorf("store: drive connected_at: %w", err)
	}
	return row, true, nil
}

// SaveDrive stores the connection, replacing the one there is.
func (s *Store) SaveDrive(ctx context.Context, r DriveRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO drive (id, refresh_token_enc, email, folder_id, folder_name, state, connected_by, connected_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET refresh_token_enc = excluded.refresh_token_enc, email = excluded.email,
		   folder_id = excluded.folder_id, folder_name = excluded.folder_name, state = excluded.state,
		   connected_by = excluded.connected_by, connected_at = excluded.connected_at`,
		r.RefreshTokenEnc, r.Email, nullString(r.FolderID), nullString(r.FolderName), r.State, r.ConnectedBy, formatTime(r.ConnectedAt))
	if err != nil {
		return fmt.Errorf("store: save drive: %w", err)
	}
	return nil
}

// SetDriveFolder records the destination folder. ErrNotFound without a
// connection.
func (s *Store) SetDriveFolder(ctx context.Context, id, name string) error {
	return s.updateDrive(ctx, `UPDATE drive SET folder_id = ?, folder_name = ? WHERE id = 1`, nullString(id), nullString(name))
}

// SetDriveState sets "connected" or "expired". ErrNotFound without a
// connection.
func (s *Store) SetDriveState(ctx context.Context, state string) error {
	return s.updateDrive(ctx, `UPDATE drive SET state = ? WHERE id = 1`, state)
}

func (s *Store) updateDrive(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: update drive: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: update drive: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDrive removes the connection. It is not an error when there is none.
func (s *Store) DeleteDrive(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drive WHERE id = 1`); err != nil {
		return fmt.Errorf("store: delete drive: %w", err)
	}
	return nil
}

// PutOAuthState stores a connect link's state with the actor it was issued to,
// where to return to and the redirect URI used for the code exchange. It first
// deletes the states older than ten minutes (at is now).
func (s *Store) PutOAuthState(ctx context.Context, state, actor, returnTo, redirect string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: put oauth state: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// A connect that never came back leaves its state: drop the expired ones.
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_oauth_states WHERE created_at < ?`, formatTime(at.Add(-oauthStateTTL))); err != nil {
		return fmt.Errorf("store: put oauth state: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO drive_oauth_states (state, actor, return_to, redirect, created_at) VALUES (?, ?, ?, ?, ?)`,
		state, actor, returnTo, redirect, formatTime(at)); err != nil {
		return fmt.Errorf("store: put oauth state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: put oauth state: %w", err)
	}
	return nil
}

// TakeOAuthState uses a state once: it deletes states older than ten minutes,
// then returns and deletes the row. ok is false for an unknown, used or
// expired state.
func (s *Store) TakeOAuthState(ctx context.Context, state string, now time.Time) (actor, returnTo, redirect string, ok bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", false, fmt.Errorf("store: take oauth state: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// RFC 3339 UTC text sorts as time does.
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_oauth_states WHERE created_at < ?`, formatTime(now.Add(-oauthStateTTL))); err != nil {
		return "", "", "", false, fmt.Errorf("store: take oauth state: %w", err)
	}
	err = tx.QueryRowContext(ctx, `SELECT actor, return_to, redirect FROM drive_oauth_states WHERE state = ?`, state).Scan(&actor, &returnTo, &redirect)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, tx.Commit()
	}
	if err != nil {
		return "", "", "", false, fmt.Errorf("store: take oauth state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_oauth_states WHERE state = ?`, state); err != nil {
		return "", "", "", false, fmt.Errorf("store: take oauth state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", "", "", false, fmt.Errorf("store: take oauth state: %w", err)
	}
	return actor, returnTo, redirect, true, nil
}
