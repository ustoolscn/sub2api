package service

import (
	"regexp"
	"strings"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	phoneAuthProviderType = "phone"
	phoneAuthProviderKey  = "phone"
)

var (
	ErrInvalidPhoneNumber = infraerrors.BadRequest("INVALID_PHONE", "invalid phone number")
	ErrPhoneLoginDisabled = infraerrors.Forbidden("PHONE_LOGIN_DISABLED", "phone login is currently disabled")
	ErrSMSNotConfigured   = infraerrors.ServiceUnavailable("SMS_NOT_CONFIGURED", "sms service not configured")
	ErrPhoneAlreadyBound  = infraerrors.Conflict("PHONE_ALREADY_BOUND", "phone number already bound to another account")
	ErrSMSSendFailed      = infraerrors.ServiceUnavailable("SMS_SEND_FAILED", "failed to send verification sms")
)

var cnMobileNumber = regexp.MustCompile(`^1[3-9]\d{9}$`)

// NormalizePhone accepts a China mainland mobile number in common writings
// (13800138000 / +86 138 0013 8000 / 0086-13800138000) and returns E.164.
func NormalizePhone(raw string) (string, error) {
	digits := make([]rune, 0, len(raw))
	for _, r := range raw {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
	}
	if len(digits) == 0 {
		return "", ErrInvalidPhoneNumber
	}
	national := string(digits)
	switch {
	case strings.HasPrefix(national, "0086"):
		national = strings.TrimPrefix(national, "0086")
	case strings.HasPrefix(national, "86") && len(national) == 13:
		national = strings.TrimPrefix(national, "86")
	}
	if !cnMobileNumber.MatchString(national) {
		return "", ErrInvalidPhoneNumber
	}
	return "+86" + national, nil
}

// PhoneNationalNumber returns the 11-digit CN national number from an E.164 value.
func PhoneNationalNumber(e164 string) string {
	return strings.TrimPrefix(strings.TrimSpace(e164), "+86")
}

// MaskPhone hides the middle digits of a CN mobile number, e.g. 138****8000.
func MaskPhone(e164 string) string {
	national := PhoneNationalNumber(e164)
	if !cnMobileNumber.MatchString(national) {
		return maskOpaqueIdentity(e164)
	}
	return national[:3] + "****" + national[7:]
}

// PhoneSyntheticEmail is the reserved mailbox used to satisfy the users.email
// NOT NULL unique constraint for phone-only accounts.
func PhoneSyntheticEmail(e164 string) string {
	digits := strings.TrimPrefix(strings.TrimSpace(e164), "+")
	return digits + PhoneConnectSyntheticEmailDomain
}

func phoneCacheKey(e164 string) string {
	return "phone:" + strings.ToLower(strings.TrimSpace(e164))
}
