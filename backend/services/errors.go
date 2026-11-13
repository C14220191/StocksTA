// Package services berisi logika aplikasi dan query SQL: cache policy, panggilan AI, price feed,
// metrik evaluasi, akun pengguna. Handler HTTP (controllers) hanya membaca request, memanggil
// service, lalu menulis respons; struct tabel ada di package models.
package services

import (
	"errors"
	"net/http"
)

var (
	ErrNotFound      = errors.New("tidak ditemukan")
	ErrExists        = errors.New("sudah ada")
	ErrNotEnoughData = errors.New("data OHLCV belum cukup")
)

// Error: kesalahan yang sudah tahu bagaimana dilaporkan ke pengguna. Controller menulisnya sebagai
// JSON {"error": Message, "code"?: Code, "fields"?: Fields, "retry_after"?: RetryAfter} dengan status
// Status; error lain menjadi 500 "internal error".
type Error struct {
	Status     int
	Code       string // kosong = tanpa field "code"
	Message    string
	Fields     FieldErrors // nil = tanpa field "fields"
	RetryAfter int         // > 0 = field "retry_after" (detik)
}

func (e *Error) Error() string { return e.Message }

func newError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// badRequest: 400 dengan pesan error apa adanya.
func badRequest(err error) *Error { return &Error{Status: http.StatusBadRequest, Message: err.Error()} }

// invalid: 422 dengan kesalahan per isian form.
func invalid(f FieldErrors) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Message: msgCheckFields, Fields: f}
}
