package controllers

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"ta-backend/services"
)

// Manajemen pengguna (/admin/users). Penjaganya di services/admin_users.go.

// userID: :id di path; false = bukan angka (dijawab 404 seperti akun yang tidak ada).
func userID(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil
}

func userNotFound(c echo.Context) error {
	return jsonError(c, http.StatusNotFound, "not_found", "Pengguna tidak ditemukan.")
}

// AdminUsers: GET /api/admin/users?q=&role=admin|user&limit=50&offset=0
func (h *Controller) AdminUsers(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	out, err := h.Auth.ListUsers(c.Request().Context(), c.QueryParam("q"), c.QueryParam("role"), limit, offset)
	return respond(c, http.StatusOK, out, err)
}

// AdminCreateUser: POST /api/admin/users {"name", "email", "password", "role"} -> 201.
func (h *Controller) AdminCreateUser(c echo.Context) error {
	var body services.CreateUserInput
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.CreateUser(c.Request().Context(), currentUser(c), body)
	return respond(c, http.StatusCreated, out, err)
}

// AdminUpdateUser: PATCH /api/admin/users/:id {"name"?, "role"?, "disabled"?, "email_verified": true?}
func (h *Controller) AdminUpdateUser(c echo.Context) error {
	id, ok := userID(c)
	if !ok {
		return userNotFound(c)
	}
	var body services.UserPatch
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.UpdateUser(c.Request().Context(), currentUser(c), id, body)
	return respond(c, http.StatusOK, out, err)
}

// AdminSendPasswordReset: POST /api/admin/users/:id/password-reset
func (h *Controller) AdminSendPasswordReset(c echo.Context) error {
	id, ok := userID(c)
	if !ok {
		return userNotFound(c)
	}
	out, err := h.Auth.SendPasswordReset(c.Request().Context(), currentUser(c), id)
	return respond(c, http.StatusOK, out, err)
}

// AdminDeleteUser: DELETE /api/admin/users/:id
func (h *Controller) AdminDeleteUser(c echo.Context) error {
	id, ok := userID(c)
	if !ok {
		return userNotFound(c)
	}
	out, err := h.Auth.DeleteUser(c.Request().Context(), currentUser(c), id)
	return respond(c, http.StatusOK, out, err)
}
