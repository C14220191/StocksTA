package models

import "time"

// User: satu baris tabel users (migrations/011_users.sql), dalam bentuk yang dikirim ke UI.
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

	// PasswordHash: hash bcrypt (kosong = hanya login Google). Tidak pernah dikirim ke UI.
	PasswordHash string `json:"-"`
}
