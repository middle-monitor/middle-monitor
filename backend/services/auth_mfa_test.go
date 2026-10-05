package services

import (
	"database/sql"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// Two-factor authentication is the last line in front of an account, so the
// question every test here asks is the same: can something that is not the
// legitimate second factor get through?

func recoveryCodeRows(codes ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "code_hash"})
	for i, code := range codes {
		hash, _ := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
		rows.AddRow(int64(i+1), string(hash))
	}
	return rows
}

// A valid TOTP code passes without touching the recovery codes at all: every
// normal login must not pay for a table scan and a bcrypt compare per row.
func TestVerifySecondFactorAcceptsAValidTOTPCode(t *testing.T) {
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	secret, err := totp.Generate(totp.GenerateOpts{Issuer: "middle-monitor", AccountName: "a@b.c"})
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	code, err := totp.GenerateCode(secret.Secret(), time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	if !svc.verifySecondFactor(1, secret.Secret(), code) {
		t.Fatal("a valid TOTP code must be accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("recovery codes should not have been read: %v", err)
	}
}

// Authenticator apps display the code as "123 456" and people paste what they
// see. Rejecting the spacing turns a correct code into a failed login.
func TestVerifySecondFactorToleratesGroupedDigits(t *testing.T) {
	db, _ := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	secret, _ := totp.Generate(totp.GenerateOpts{Issuer: "middle-monitor", AccountName: "a@b.c"})
	code, _ := totp.GenerateCode(secret.Secret(), time.Now())

	if !svc.verifySecondFactor(1, secret.Secret(), code[:3]+" "+code[3:]) {
		t.Fatal("a TOTP code with a space must be accepted")
	}
}

// An empty submission must be refused before anything is compared. The stored
// row here hashes the empty string, so a blank second factor would match it if
// the guard ever went away: the test proves the guard, not that the database
// happened to be unavailable.
func TestVerifySecondFactorRejectsAnEmptyCode(t *testing.T) {
	for _, code := range []string{"", "   ", "\t"} {
		t.Run(quoted(code), func(t *testing.T) {
			db, mock := newDB(t)
			t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
			svc := NewAuthService(db)

			mock.ExpectQuery("FROM user_recovery_codes").WillReturnRows(recoveryCodeRows(""))
			mock.ExpectExec("UPDATE user_recovery_codes").WillReturnResult(sqlmock.NewResult(0, 1))

			if svc.verifySecondFactor(1, "SOMESECRET", code) {
				t.Fatal("a blank second factor must never be accepted")
			}
		})
	}
}

func quoted(s string) string { return strconv.Quote(s) }

// A user who lost their phone falls back to a recovery code, which must work
// even though it is not a TOTP code at all.
func TestVerifySecondFactorFallsBackToARecoveryCode(t *testing.T) {
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	mock.ExpectQuery("FROM user_recovery_codes").WillReturnRows(recoveryCodeRows("aaaa-bbbb"))
	mock.ExpectExec("UPDATE user_recovery_codes").WillReturnResult(sqlmock.NewResult(0, 1))

	if !svc.verifySecondFactor(7, "", "aaaa-bbbb") {
		t.Fatal("a valid recovery code must be accepted")
	}
}

// Single use is the whole point of a recovery code. Consuming it has to mark it
// used in the same call that accepts it, or a leaked code stays valid forever.
func TestConsumeRecoveryCodeMarksItUsed(t *testing.T) {
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	mock.ExpectQuery("FROM user_recovery_codes").WillReturnRows(recoveryCodeRows("wrong-one", "aaaa-bbbb"))
	// The second row is the match, so that id is the one marked used.
	mock.ExpectExec("UPDATE user_recovery_codes SET used_at").
		WithArgs(int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if !svc.consumeRecoveryCode(7, "aaaa-bbbb") {
		t.Fatal("expected the matching code to be accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the matching code must be the one marked used: %v", err)
	}
}

// If the code cannot be marked used, accepting the login would leave a reusable
// code behind. Failing closed is the only safe answer.
func TestConsumeRecoveryCodeRefusesWhenItCannotMarkItUsed(t *testing.T) {
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	mock.ExpectQuery("FROM user_recovery_codes").WillReturnRows(recoveryCodeRows("aaaa-bbbb"))
	mock.ExpectExec("UPDATE user_recovery_codes").WillReturnError(sql.ErrConnDone)

	if svc.consumeRecoveryCode(7, "aaaa-bbbb") {
		t.Fatal("a code that could not be marked used must not grant access")
	}
}

// A wrong code must not match any row, and must not consume one either.
func TestConsumeRecoveryCodeRejectsAWrongCode(t *testing.T) {
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	mock.ExpectQuery("FROM user_recovery_codes").WillReturnRows(recoveryCodeRows("aaaa-bbbb", "cccc-dddd"))

	if svc.consumeRecoveryCode(7, "eeee-ffff") {
		t.Fatal("a code that matches nothing must be rejected")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("nothing should have been marked used: %v", err)
	}
}

// A database failure while reading the codes must not be read as "no codes, let
// them in". It has to fail closed.
func TestConsumeRecoveryCodeFailsClosedOnADatabaseError(t *testing.T) {
	db, mock := newDB(t)
	t.Setenv("JWT_SECRET", "test-jwt-secret-32-chars-minimum!!")
	svc := NewAuthService(db)

	mock.ExpectQuery("FROM user_recovery_codes").WillReturnError(sql.ErrConnDone)

	if svc.consumeRecoveryCode(7, "aaaa-bbbb") {
		t.Fatal("a lookup failure must not grant access")
	}
}

// The codes are what a user writes down, so the shape is part of the contract,
// and every code in a batch has to be distinct.
func TestGenerateRecoveryCodeShapeAndUniqueness(t *testing.T) {
	shape := regexp.MustCompile(`^[a-zA-Z0-9]{4}-[a-zA-Z0-9]{4}$`)

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code := generateRecoveryCode()
		if !shape.MatchString(code) {
			t.Fatalf("got %q, want the xxxx-xxxx shape", code)
		}
		if seen[code] {
			t.Fatalf("generated %q twice in 200 draws", code)
		}
		seen[code] = true
	}
}
