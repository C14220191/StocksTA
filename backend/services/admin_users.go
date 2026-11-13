package services

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"ta-backend/models"
)

// Manajemen pengguna di halaman admin (/admin/users). Penjaga: admin tidak bisa menurunkan,
// menonaktifkan, atau menghapus akunnya sendiri (jadi selalu tersisa minimal satu admin aktif: yang
// sedang bertindak); akun admin bawaan (ADMIN_EMAIL) tidak bisa diturunkan / dinonaktifkan / dihapus
// karena dibuat ulang dari .env setiap backend start.

func validRole(r string) bool { return r == roleAdmin || r == roleUser }

var errUserNotFound = newError(http.StatusNotFound, "not_found", "Pengguna tidak ditemukan.")

// ListUsers: GET /api/admin/users?q=&role=admin|user&limit=50&offset=0
func (a *AuthService) ListUsers(ctx context.Context, q, role string, limit, offset int) (map[string]any, error) {
	if role != "" && !validRole(role) {
		return nil, newError(http.StatusBadRequest, "bad_request", "role harus admin atau user")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset = max(offset, 0)
	users, total, err := a.store.ListUsers(ctx, strings.TrimSpace(q), role, limit, offset)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		a.present(u)
	}
	return map[string]any{"users": users, "total": total, "limit": limit, "offset": offset}, nil
}

// CreateUserInput: body POST /api/admin/users.
type CreateUserInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// CreateUser: email divalidasi seperti pendaftaran (format + domain), akun langsung terverifikasi
// karena dibuat admin.
func (a *AuthService) CreateUser(ctx context.Context, actor *models.User, in CreateUserInput) (map[string]any, error) {
	name, email := strings.TrimSpace(in.Name), normalizeEmail(in.Email)
	if in.Role == "" {
		in.Role = roleUser
	}
	f := FieldErrors{}
	f.add("name", nameError(name))
	f.add("email", emailFormatError(email))
	if f["email"] == "" && a.cfg.CheckDomain != nil {
		f.add("email", a.cfg.CheckDomain(ctx, domainOf(email)))
	}
	f.add("password", passwordError(in.Password, email))
	if !validRole(in.Role) {
		f.add("role", "Peran harus admin atau user.")
	}
	if len(f) > 0 {
		return nil, invalid(f)
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u, err := a.store.CreateUser(ctx, newUser{Email: email, Name: name, PasswordHash: hash, Role: in.Role, Verified: true})
	if errors.Is(err, ErrExists) {
		return nil, emailTaken()
	}
	if err != nil {
		return nil, err
	}
	slog.Info("admin membuat akun", "admin_id", actor.ID, "user_id", u.ID, "role", u.Role)
	return map[string]any{"user": a.present(u)}, nil
}

// targetUser: akun id, atau 404 not_found.
func (a *AuthService) targetUser(ctx context.Context, id int64) (*models.User, error) {
	u, err := a.store.UserByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, errUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return a.present(u), nil
}

// guardAdminLoss: boleh menurunkan / menonaktifkan / menghapus target? nil = boleh, selain itu 409.
func guardAdminLoss(actor, target *models.User, action string) error {
	if target.ID == actor.ID {
		return newError(http.StatusConflict, "self", "Tidak bisa "+action+" akun Anda sendiri.")
	}
	if target.Role == roleAdmin && target.Managed {
		return newError(http.StatusConflict, "managed_account",
			"Akun admin bawaan (ADMIN_EMAIL) dibuat ulang dari .env setiap backend start, jadi tidak bisa "+action+".")
	}
	return nil
}

// UserPatch: body PATCH /api/admin/users/:id (field kosong = tidak diubah).
type UserPatch struct {
	Name          *string `json:"name"`
	Role          *string `json:"role"`
	Disabled      *bool   `json:"disabled"`
	EmailVerified *bool   `json:"email_verified"`
}

// UpdateUser: nama, peran, aktif / nonaktif (mencabut semua sesinya), tandai email terverifikasi.
func (a *AuthService) UpdateUser(ctx context.Context, actor *models.User, id int64, in UserPatch) (map[string]any, error) {
	target, err := a.targetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	f := FieldErrors{}
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
		f.add("name", nameError(*in.Name))
	}
	if in.Role != nil && !validRole(*in.Role) {
		f.add("role", "Peran harus admin atau user.")
	}
	if in.EmailVerified != nil && !*in.EmailVerified {
		f.add("email_verified", "Email hanya bisa ditandai terverifikasi, tidak bisa dibatalkan.")
	}
	if len(f) > 0 {
		return nil, invalid(f)
	}
	if in.Role != nil && *in.Role != roleAdmin && target.Role == roleAdmin {
		if err := guardAdminLoss(actor, target, "menurunkan peran"); err != nil {
			return nil, err
		}
	}
	if in.Disabled != nil && *in.Disabled && !target.Disabled {
		if err := guardAdminLoss(actor, target, "menonaktifkan"); err != nil {
			return nil, err
		}
	}
	if in.Name != nil {
		err = a.store.SetUserName(ctx, target.ID, *in.Name)
	}
	if err == nil && in.Role != nil && *in.Role != target.Role {
		err = a.store.SetUserRole(ctx, target.ID, *in.Role)
	}
	if err == nil && in.Disabled != nil && *in.Disabled != target.Disabled {
		err = a.store.SetUserDisabled(ctx, target.ID, *in.Disabled)
	}
	if err == nil && in.EmailVerified != nil && !target.EmailVerified {
		err = a.store.MarkEmailVerified(ctx, target.ID)
	}
	if err != nil {
		return nil, err
	}
	slog.Info("admin mengubah akun", "admin_id", actor.ID, "user_id", target.ID)
	u, err := a.store.UserByID(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"user": a.present(u)}, nil
}

// SendPasswordReset: kirim link reset ke email akun id.
func (a *AuthService) SendPasswordReset(ctx context.Context, actor *models.User, id int64) (map[string]any, error) {
	target, err := a.targetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if target.Managed {
		return nil, newError(http.StatusConflict, "managed_account", "Kata sandi akun bawaan diatur lewat .env / run.env.")
	}
	if target.Disabled {
		return nil, newError(http.StatusConflict, "account_disabled", "Akun ini nonaktif. Aktifkan dulu sebelum mengirim link reset.")
	}
	if err := a.sendLink(ctx, target, purposeResetPassword); err != nil {
		return nil, err
	}
	slog.Info("admin mengirim link reset", "admin_id", actor.ID, "user_id", target.ID)
	return map[string]any{"message": "Link reset kata sandi dikirim ke " + target.Email + ".", "mail": a.cfg.Mailer.Kind()}, nil
}

// DeleteUser: hapus akun beserta sesi dan tokennya (ON DELETE CASCADE).
func (a *AuthService) DeleteUser(ctx context.Context, actor *models.User, id int64) (map[string]any, error) {
	target, err := a.targetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := guardAdminLoss(actor, target, "menghapus"); err != nil {
		return nil, err
	}
	if err := a.store.DeleteUser(ctx, target.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	slog.Info("admin menghapus akun", "admin_id", actor.ID, "user_id", target.ID)
	return map[string]any{"deleted": target.ID}, nil
}
