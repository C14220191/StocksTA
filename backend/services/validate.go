package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Validasi isian akun. Setiap fungsi mengembalikan pesan kesalahan untuk pengguna ("" = valid), dan
// handler mengumpulkannya per field: 422 {"error", "fields": {"email": "...", ...}}.

type FieldErrors map[string]string

// add menyimpan kesalahan pertama per field (pesan "" diabaikan).
func (f FieldErrors) add(field, msg string) {
	if _, ok := f[field]; !ok && msg != "" {
		f[field] = msg
	}
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// karakter yang sah di bagian lokal email (atext RFC 5322) selain huruf dan angka
const emailLocalSymbols = ".!#$%&'*+/=?^_`{|}~-"

// emailFormatError memeriksa bentuk email yang sudah dinormalisasi. Sengaja lebih ketat dari RFC
// (tanpa quoted string, IP literal, atau karakter non-ASCII) supaya alamat yang lolos memang bisa
// dikirimi email oleh server SMTP umum.
func emailFormatError(email string) string {
	if email == "" {
		return "Email wajib diisi."
	}
	if len(email) > 254 {
		return "Email terlalu panjang (maksimal 254 karakter)."
	}
	if strings.Count(email, "@") != 1 {
		return "Email harus berisi tepat satu tanda @, contoh: nama@gmail.com."
	}
	local, domain, _ := strings.Cut(email, "@")
	switch {
	case local == "":
		return "Bagian sebelum @ kosong, contoh: nama@gmail.com."
	case len(local) > 64:
		return "Bagian sebelum @ terlalu panjang (maksimal 64 karakter)."
	case strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, ".."):
		return "Titik sebelum @ tidak boleh di awal, di akhir, atau berurutan."
	}
	for _, r := range local {
		if r > unicode.MaxASCII || !(isAlnum(r) || strings.ContainsRune(emailLocalSymbols, r)) {
			return fmt.Sprintf("Karakter %q tidak diizinkan di email.", r)
		}
	}
	if domain == "" {
		return "Domain setelah @ kosong, contoh: nama@gmail.com."
	}
	if msg := domainFormatError(domain); msg != "" {
		return msg
	}
	return ""
}

func isAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func domainFormatError(domain string) string {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return fmt.Sprintf("Domain %q tidak lengkap, contoh: gmail.com atau kampus.ac.id.", domain)
	}
	if len(domain) > 253 {
		return "Domain email terlalu panjang."
	}
	for _, l := range labels {
		if l == "" {
			return fmt.Sprintf("Domain %q tidak valid: ada titik di awal, di akhir, atau berurutan.", domain)
		}
		if len(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return fmt.Sprintf("Domain %q tidak valid.", domain)
		}
		for _, r := range l {
			if !isAlnum(r) && r != '-' {
				return fmt.Sprintf("Domain %q berisi karakter yang tidak diizinkan.", domain)
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || (!strings.HasPrefix(tld, "xn--") && strings.IndexFunc(tld, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0) {
		return fmt.Sprintf("Akhiran domain %q tidak valid, contoh: .com, .id, .ac.id.", "."+tld)
	}
	return ""
}

// DomainChecker: memastikan domain email benar-benar ada dan menerima email. "" = lolos.
type DomainChecker func(ctx context.Context, domain string) string

// checkEmailDomainDNS: domain harus punya MX (atau A/AAAA, RFC 5321 implicit MX) dan bukan null MX
// (RFC 7505, "domain ini tidak menerima email"). Gangguan DNS sementara tidak menggagalkan
// pendaftaran: link verifikasi tetap menjadi bukti akhir bahwa email itu milik pendaftar.
func checkEmailDomainDNS(ctx context.Context, domain string) string {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	r := net.DefaultResolver
	mx, err := r.LookupMX(ctx, domain)
	if err == nil && len(mx) > 0 {
		if len(mx) == 1 && (mx[0].Host == "." || mx[0].Host == "") {
			return fmt.Sprintf("Domain %s tidak menerima email. Gunakan alamat email lain.", domain)
		}
		return ""
	}
	if err != nil && !isNotFound(err) {
		slog.Warn("cek DNS email dilewati", "domain", domain, "err", err)
		return ""
	}
	if _, err := r.LookupHost(ctx, domain); err != nil {
		if isNotFound(err) {
			return fmt.Sprintf("Domain %s tidak ditemukan. Periksa ejaan email Anda (mis. gmail.com, bukan gmial.com).", domain)
		}
		slog.Warn("cek DNS email dilewati", "domain", domain, "err", err)
	}
	return ""
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func nameError(name string) string {
	n := utf8.RuneCountInString(name)
	switch {
	case n == 0:
		return "Nama wajib diisi."
	case n < 2:
		return "Nama minimal 2 karakter."
	case n > 80:
		return "Nama maksimal 80 karakter."
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return "Nama berisi karakter yang tidak diizinkan."
	}
	return ""
}

// passwordError: minimal 8 karakter, maksimal 72 byte (batas bcrypt), berisi huruf dan angka, dan
// tidak sama dengan email.
func passwordError(pw, email string) string {
	switch {
	case pw == "":
		return "Kata sandi wajib diisi."
	case utf8.RuneCountInString(pw) < 8:
		return "Kata sandi minimal 8 karakter."
	case len(pw) > 72:
		return "Kata sandi maksimal 72 byte (sekitar 72 karakter latin)."
	case strings.IndexFunc(pw, unicode.IsLetter) < 0 || strings.IndexFunc(pw, unicode.IsDigit) < 0:
		return "Kata sandi harus berisi huruf dan angka."
	}
	if email != "" {
		local, _, _ := strings.Cut(email, "@")
		if l := strings.ToLower(pw); l == email || l == local {
			return "Kata sandi tidak boleh sama dengan email."
		}
	}
	return ""
}

// checkNewPassword: kata sandi baru + konfirmasinya (register, reset, ganti kata sandi).
func checkNewPassword(f FieldErrors, pw, confirm, email string) {
	f.add("password", passwordError(pw, email))
	if confirm == "" {
		f.add("password_confirm", "Ulangi kata sandi untuk konfirmasi.")
	} else if confirm != pw {
		f.add("password_confirm", "Konfirmasi kata sandi tidak sama.")
	}
}
