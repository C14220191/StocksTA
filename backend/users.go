package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Akun pengguna, sesi login, dan token di link email (migrations/011_users.sql).

const (
	roleAdmin = "admin"
	roleUser  = "user"

	purposeVerifyEmail   = "verify_email"
	purposeResetPassword = "reset_password"
)

var (
	errTokenInvalid = errors.New("token tidak dikenal")
	errTokenUsed    = errors.New("token sudah dipakai")
	errTokenExpired = errors.New("token kedaluwarsa")
)

type User struct {
	ID            int64      `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	EmailVerified bool       `json:"email_verified"`
	HasPassword   bool       `json:"has_password"`
	GoogleLinked  bool       `json:"google_linked"`
	Disabled      bool       `json:"disabled"`
	CreatedAt     time.Time  `json:"created_at"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	// Managed: akun bawaan dari .env/run.env (ADMIN_EMAIL, DEMO_USER_EMAIL). Kata sandinya diatur dari
	// sana dan disamakan lagi setiap backend start, jadi tidak bisa diganti lewat UI.
	Managed bool `json:"managed"`

	passwordHash string
}

const userCols = `id, email, name, role, email_verified_at IS NOT NULL, coalesce(password_hash, ''),
	google_sub IS NOT NULL, disabled_at IS NOT NULL, created_at, last_login_at`

// scanUser membaca userCols, lalu kolom tambahan (extra) kalau query memilih lebih banyak.
func scanUser(row pgx.Row, extra ...any) (*User, error) {
	var u User
	err := row.Scan(append([]any{&u.ID, &u.Email, &u.Name, &u.Role, &u.EmailVerified, &u.passwordHash,
		&u.GoogleLinked, &u.Disabled, &u.CreatedAt, &u.LastLoginAt}, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.HasPassword = u.passwordHash != ""
	return &u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// UsersTableReady: migrasi 011 sudah dijalankan.
func (s *Store) UsersTableReady(ctx context.Context) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT to_regclass('users') IS NOT NULL AND to_regclass('auth_tokens') IS NOT NULL`).Scan(&ok)
	return ok, err
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, email))
}

// UserByGoogle: akun yang sudah ditautkan ke akun Google dengan sub ini.
func (s *Store) UserByGoogle(ctx context.Context, sub string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE google_sub = $1`, sub))
}

type newUser struct {
	Email, Name, PasswordHash, Role, GoogleSub string
	Verified                                   bool
}

// CreateUser: ErrExists kalau email (atau akun Google) sudah terdaftar.
func (s *Store) CreateUser(ctx context.Context, n newUser) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, google_sub, email_verified_at)
		VALUES ($1, $2, nullif($3, ''), $4, nullif($5, ''), CASE WHEN $6 THEN now() END)
		RETURNING `+userCols,
		n.Email, n.Name, n.PasswordHash, n.Role, n.GoogleSub, n.Verified))
	if isUniqueViolation(err) {
		return nil, ErrExists
	}
	return u, err
}

// updateOne: UPDATE satu akun; ErrNotFound kalau akunnya tidak ada.
func (s *Store) updateOne(ctx context.Context, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) SetUserName(ctx context.Context, id int64, name string) error {
	return s.updateOne(ctx, `UPDATE users SET name = $2, updated_at = now() WHERE id = $1`, id, name)
}

func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	return s.updateOne(ctx, `UPDATE users SET role = $2, updated_at = now() WHERE id = $1`, id, role)
}

func (s *Store) MarkEmailVerified(ctx context.Context, id int64) error {
	return s.updateOne(ctx, `UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now() WHERE id = $1`, id)
}

func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	return s.updateOne(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
}

// SetUserDisabled: menonaktifkan juga mencabut semua sesi akun itu.
func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET disabled_at = CASE WHEN $2 THEN coalesce(disabled_at, now()) END,
			updated_at = now() WHERE id = $1`, id, disabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if disabled {
			_, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, id)
		}
		return err
	})
}

// SetUserPassword: ganti hash dan cabut semua sesi akun itu kecuali keepSession (sesi yang sedang
// dipakai untuk mengganti kata sandi; nil = cabut semua).
func (s *Store) SetUserPassword(ctx context.Context, id int64, hash string, keepSession []byte) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1 AND token_hash IS DISTINCT FROM $2`, id, keepSession)
		return err
	})
}

// LinkGoogle menautkan akun ke akun Google (email-nya sudah diverifikasi Google). Kalau email akun
// lokal belum pernah diverifikasi, kata sandinya dihapus dan sesinya dicabut: bisa saja akun itu
// didaftarkan orang lain memakai email ini, dan pemilik email sebenarnya baru membuktikannya sekarang.
func (s *Store) LinkGoogle(ctx context.Context, id int64, sub string, wasVerified bool) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE users SET google_sub = $2, email_verified_at = coalesce(email_verified_at, now()),
			password_hash = CASE WHEN $3 THEN password_hash END, updated_at = now() WHERE id = $1`, id, sub, wasVerified)
		if err != nil || wasVerified {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, id)
		return err
	})
	if isUniqueViolation(err) {
		return ErrExists
	}
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ListUsers: pencarian nama/email (q), filter role, terbaru dulu.
func (s *Store) ListUsers(ctx context.Context, q, role string, limit, offset int) ([]*User, int, error) {
	where := `($1 = '' OR email ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%') AND ($2 = '' OR role = $2)`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE `+where, q, role).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE `+where+
		` ORDER BY created_at DESC, id DESC LIMIT $3 OFFSET $4`, q, role, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

// ---- sesi ----

func (s *Store) CreateSession(ctx context.Context, userID int64, hash []byte, exp time.Time, ua, ip string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO user_sessions (token_hash, user_id, expires_at, user_agent, ip)
		VALUES ($1, $2, $3, $4, $5)`, hash, userID, exp, truncate(ua, 300), ip)
	return err
}

// SessionUser: pemilik sesi yang masih berlaku dan akunnya aktif.
func (s *Store) SessionUser(ctx context.Context, hash []byte) (*User, time.Time, error) {
	var exp time.Time
	u, err := scanUser(s.pool.QueryRow(ctx, `
		WITH s AS (SELECT user_id, expires_at AS session_exp FROM user_sessions WHERE token_hash = $1 AND expires_at > now())
		SELECT `+userCols+`, s.session_exp FROM users JOIN s ON s.user_id = users.id WHERE users.disabled_at IS NULL`, hash), &exp)
	return u, exp, err
}

func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE token_hash = $1`, hash)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, userID)
	return err
}

// PurgeExpired: bersih-bersih sesi dan token kedaluwarsa (dipanggil sesekali saat login).
func (s *Store) PurgeExpired(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_tokens WHERE expires_at < now() - interval '7 days'`)
	return err
}

// ---- token link email ----

// CreateAuthToken membatalkan token lama dengan tujuan yang sama (hanya link terbaru yang berlaku).
func (s *Store) CreateAuthToken(ctx context.Context, userID int64, purpose string, hash []byte, exp time.Time) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE auth_tokens SET used_at = now()
			WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`, userID, purpose); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO auth_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
			hash, userID, purpose, exp)
		return err
	})
}

// LastAuthTokenAt: kapan link terakhir dengan tujuan ini dibuat (jeda kirim ulang email).
func (s *Store) LastAuthTokenAt(ctx context.Context, userID int64, purpose string) (time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx, `SELECT max(created_at) FROM auth_tokens WHERE user_id = $1 AND purpose = $2`,
		userID, purpose).Scan(&t)
	if t == nil {
		return time.Time{}, err
	}
	return *t, err
}

// consumeToken (di dalam transaksi): tandai token terpakai; errTokenInvalid / Used / Expired kalau tidak bisa.
func consumeToken(ctx context.Context, tx pgx.Tx, purpose string, hash []byte) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `UPDATE auth_tokens SET used_at = now()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now() RETURNING user_id`,
		hash, purpose).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	var used bool
	err = tx.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM auth_tokens WHERE token_hash = $1 AND purpose = $2`,
		hash, purpose).Scan(&used)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, errTokenInvalid
	case err != nil:
		return 0, err
	case used:
		return 0, errTokenUsed
	default:
		return 0, errTokenExpired
	}
}

// PeekAuthToken: pemilik token yang masih bisa dipakai, tanpa memakainya (errTokenInvalid / Used / Expired).
func (s *Store) PeekAuthToken(ctx context.Context, purpose string, hash []byte) (int64, error) {
	var id int64
	var used, expired bool
	err := s.pool.QueryRow(ctx, `SELECT user_id, used_at IS NOT NULL, expires_at <= now() FROM auth_tokens
		WHERE token_hash = $1 AND purpose = $2`, hash, purpose).Scan(&id, &used, &expired)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, errTokenInvalid
	case err != nil:
		return 0, err
	case used:
		return 0, errTokenUsed
	case expired:
		return 0, errTokenExpired
	}
	return id, nil
}

// VerifyEmailToken: pakai token verifikasi, tandai email terverifikasi. Mengembalikan pemilik token.
func (s *Store) VerifyEmailToken(ctx context.Context, hash []byte) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if id, err = consumeToken(ctx, tx, purposeVerifyEmail, hash); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
			WHERE id = $1`, id)
		return err
	})
	return id, err
}

// ResetPasswordToken: pakai token reset, set kata sandi baru, cabut semua sesi. Membuka link dari
// email juga membuktikan kepemilikan email, jadi email ikut ditandai terverifikasi.
func (s *Store) ResetPasswordToken(ctx context.Context, hash []byte, passwordHash string) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if id, err = consumeToken(ctx, tx, purposeResetPassword, hash); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET password_hash = $2,
			email_verified_at = coalesce(email_verified_at, now()), updated_at = now() WHERE id = $1`, id, passwordHash); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, id)
		return err
	})
	return id, err
}
