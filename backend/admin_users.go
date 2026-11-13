package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// Manajemen pengguna di halaman admin (/admin/users). Penjaga: admin tidak bisa menurunkan,
// menonaktifkan, atau menghapus akunnya sendiri (jadi selalu tersisa minimal satu admin aktif: yang
// sedang bertindak); akun admin bawaan (ADMIN_EMAIL) tidak bisa diturunkan / dinonaktifkan / dihapus
// karena dibuat ulang dari .env setiap backend start.

func validRole(r string) bool { return r == roleAdmin || r == roleUser }

// AdminUsers: GET /api/admin/users?q=&role=admin|user&limit=50&offset=0
func (a *App) AdminUsers(c echo.Context) error {
	role := c.QueryParam("role")
	if role != "" && !validRole(role) {
		return jsonError(c, http.StatusBadRequest, "bad_request", "role harus admin atau user")
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	offset = max(offset, 0)
	users, total, err := a.store.ListUsers(c.Request().Context(), strings.TrimSpace(c.QueryParam("q")), role, limit, offset)
	if err != nil {
		return a.fail(c, err)
	}
	for _, u := range users {
		a.auth.present(u)
	}
	return c.JSON(http.StatusOK, map[string]any{"users": users, "total": total, "limit": limit, "offset": offset})
}

// AdminCreateUser: POST /api/admin/users {"name", "email", "password", "role"}. Email divalidasi
// seperti pendaftaran (format + domain), akun langsung terverifikasi karena dibuat admin.
func (a *App) AdminCreateUser(c echo.Context) error {
	var body struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	ctx := c.Request().Context()
	name, email := strings.TrimSpace(body.Name), normalizeEmail(body.Email)
	if body.Role == "" {
		body.Role = roleUser
	}
	f := fieldErrors{}
	f.add("name", nameError(name))
	f.add("email", emailFormatError(email))
	if f["email"] == "" && a.auth.checkDomain != nil {
		f.add("email", a.auth.checkDomain(ctx, domainOf(email)))
	}
	f.add("password", passwordError(body.Password, email))
	if !validRole(body.Role) {
		f.add("role", "Peran harus admin atau user.")
	}
	if len(f) > 0 {
		return invalidFields(c, f)
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		return a.fail(c, err)
	}
	u, err := a.store.CreateUser(ctx, newUser{Email: email, Name: name, PasswordHash: hash, Role: body.Role, Verified: true})
	if errors.Is(err, ErrExists) {
		return emailTaken(c)
	}
	if err != nil {
		return a.fail(c, err)
	}
	slog.Info("admin membuat akun", "admin_id", currentUser(c).ID, "user_id", u.ID, "role", u.Role)
	return c.JSON(http.StatusCreated, map[string]any{"user": a.auth.present(u)})
}

// targetUser: akun di :id, atau respons 404 yang sudah dikirim.
func (a *App) targetUser(c echo.Context) (*User, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return nil, jsonError(c, http.StatusNotFound, "not_found", "Pengguna tidak ditemukan.")
	}
	u, err := a.store.UserByID(c.Request().Context(), id)
	if errors.Is(err, ErrNotFound) {
		return nil, jsonError(c, http.StatusNotFound, "not_found", "Pengguna tidak ditemukan.")
	}
	if err != nil {
		return nil, a.fail(c, err)
	}
	return a.auth.present(u), nil
}

// guardAdminLoss: boleh menurunkan / menonaktifkan / menghapus target? false = respons 409 sudah
// dikirim, kembalikan err-nya apa adanya.
func (a *App) guardAdminLoss(c echo.Context, target *User, action string) (bool, error) {
	me := currentUser(c)
	if target.ID == me.ID {
		return false, jsonError(c, http.StatusConflict, "self", "Tidak bisa "+action+" akun Anda sendiri.")
	}
	if target.Role != roleAdmin {
		return true, nil
	}
	if target.Managed {
		return false, jsonError(c, http.StatusConflict, "managed_account",
			"Akun admin bawaan (ADMIN_EMAIL) dibuat ulang dari .env setiap backend start, jadi tidak bisa "+action+".")
	}
	return true, nil
}

// AdminUpdateUser: PATCH /api/admin/users/:id {"name"?, "role"?, "disabled"?, "email_verified": true?}
func (a *App) AdminUpdateUser(c echo.Context) error {
	target, err := a.targetUser(c)
	if target == nil {
		return err
	}
	var body struct {
		Name          *string `json:"name"`
		Role          *string `json:"role"`
		Disabled      *bool   `json:"disabled"`
		EmailVerified *bool   `json:"email_verified"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	f := fieldErrors{}
	if body.Name != nil {
		*body.Name = strings.TrimSpace(*body.Name)
		f.add("name", nameError(*body.Name))
	}
	if body.Role != nil && !validRole(*body.Role) {
		f.add("role", "Peran harus admin atau user.")
	}
	if body.EmailVerified != nil && !*body.EmailVerified {
		f.add("email_verified", "Email hanya bisa ditandai terverifikasi, tidak bisa dibatalkan.")
	}
	if len(f) > 0 {
		return invalidFields(c, f)
	}
	if body.Role != nil && *body.Role != roleAdmin && target.Role == roleAdmin {
		if ok, err := a.guardAdminLoss(c, target, "menurunkan peran"); !ok {
			return err
		}
	}
	if body.Disabled != nil && *body.Disabled && !target.Disabled {
		if ok, err := a.guardAdminLoss(c, target, "menonaktifkan"); !ok {
			return err
		}
	}
	ctx := c.Request().Context()
	if body.Name != nil {
		err = a.store.SetUserName(ctx, target.ID, *body.Name)
	}
	if err == nil && body.Role != nil && *body.Role != target.Role {
		err = a.store.SetUserRole(ctx, target.ID, *body.Role)
	}
	if err == nil && body.Disabled != nil && *body.Disabled != target.Disabled {
		err = a.store.SetUserDisabled(ctx, target.ID, *body.Disabled)
	}
	if err == nil && body.EmailVerified != nil && !target.EmailVerified {
		err = a.store.MarkEmailVerified(ctx, target.ID)
	}
	if err != nil {
		return a.fail(c, err)
	}
	slog.Info("admin mengubah akun", "admin_id", currentUser(c).ID, "user_id", target.ID)
	u, err := a.store.UserByID(ctx, target.ID)
	if err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"user": a.auth.present(u)})
}

// AdminSendPasswordReset: POST /api/admin/users/:id/password-reset, kirim link reset ke email akun.
func (a *App) AdminSendPasswordReset(c echo.Context) error {
	target, err := a.targetUser(c)
	if target == nil {
		return err
	}
	if target.Managed {
		return jsonError(c, http.StatusConflict, "managed_account", "Kata sandi akun bawaan diatur lewat .env / run.env.")
	}
	if target.Disabled {
		return jsonError(c, http.StatusConflict, "account_disabled", "Akun ini nonaktif. Aktifkan dulu sebelum mengirim link reset.")
	}
	if err := a.sendLink(c.Request().Context(), target, purposeResetPassword); err != nil {
		return a.fail(c, err)
	}
	slog.Info("admin mengirim link reset", "admin_id", currentUser(c).ID, "user_id", target.ID)
	return c.JSON(http.StatusOK, map[string]any{"message": "Link reset kata sandi dikirim ke " + target.Email + ".", "mail": a.auth.mail.kind()})
}

// AdminDeleteUser: DELETE /api/admin/users/:id (sesi dan token ikut terhapus, ON DELETE CASCADE).
func (a *App) AdminDeleteUser(c echo.Context) error {
	target, err := a.targetUser(c)
	if target == nil {
		return err
	}
	if ok, err := a.guardAdminLoss(c, target, "menghapus"); !ok {
		return err
	}
	if err := a.store.DeleteUser(c.Request().Context(), target.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return a.fail(c, err)
	}
	slog.Info("admin menghapus akun", "admin_id", currentUser(c).ID, "user_id", target.ID)
	return c.JSON(http.StatusOK, map[string]any{"deleted": target.ID})
}
