-- 011: akun pengguna (register / login / lupa kata sandi / verifikasi email / Google OAuth2) dan
-- peran admin | user. Menggantikan login admin satu kata sandi: halaman /admin sekarang butuh akun
-- dengan role = 'admin'. Akun admin bawaan dibuat backend saat start dari ADMIN_EMAIL + ADMIN_PASSWORD.
-- Idempoten: .\ta-up.ps1 migrate 011_users.sql  /  ./ta-up.sh migrate 011_users.sql

CREATE TABLE IF NOT EXISTS users (
    id                bigserial PRIMARY KEY,
    email             text NOT NULL UNIQUE CHECK (email = lower(email)),  -- dinormalisasi di backend
    name              text NOT NULL,
    password_hash     text,                     -- bcrypt; NULL = hanya bisa masuk lewat Google
    role              text NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    email_verified_at timestamptz,
    google_sub        text UNIQUE,              -- klaim "sub" ID token Google (tetap walau email Google berganti)
    disabled_at       timestamptz,              -- dinonaktifkan admin: tidak bisa login, sesi dicabut
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    last_login_at     timestamptz
);

-- Sesi login. Token acak 32 byte hanya dipegang browser; database menyimpan sha256-nya, jadi isi
-- tabel ini tidak bisa dipakai untuk login walaupun bocor.
CREATE TABLE IF NOT EXISTS user_sessions (
    token_hash bytea PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    user_agent text,
    ip         text
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires ON user_sessions (expires_at);

-- Token sekali pakai di link email (verifikasi email 24 jam, reset kata sandi 1 jam). Juga disimpan
-- sebagai sha256. Membuat token baru membatalkan token lama dengan tujuan yang sama.
CREATE TABLE IF NOT EXISTS auth_tokens (
    token_hash bytea PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_user ON auth_tokens (user_id, purpose, created_at);
