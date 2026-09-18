package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *AuthService) SetPhoneSMSSender(sender PhoneSMSSender) {
	if s == nil {
		return
	}
	s.phoneSMSSender = sender
}

func (s *AuthService) phoneSMSSenderOrDefault() PhoneSMSSender {
	if s != nil && s.phoneSMSSender != nil {
		return s.phoneSMSSender
	}
	return newAliyunPhoneSMSSender()
}

func (s *AuthService) phoneLoginSettings(ctx context.Context) (enabled bool, cred PhoneSMSCredentials, signName, templateCode, paramKey string) {
	if s == nil || s.settingService == nil {
		return false, PhoneSMSCredentials{}, "", "", ""
	}
	settings, err := s.settingService.GetAllSettings(ctx)
	if err != nil || settings == nil {
		return false, PhoneSMSCredentials{}, "", "", ""
	}
	return settings.PhoneLoginEnabled,
		PhoneSMSCredentials{
			AccessKeyID:     strings.TrimSpace(settings.PhoneSMSAliyunAccessKeyID),
			AccessKeySecret: strings.TrimSpace(settings.PhoneSMSAliyunAccessKeySecret),
		},
		strings.TrimSpace(settings.PhoneSMSAliyunSignName),
		strings.TrimSpace(settings.PhoneSMSAliyunTemplateCode),
		DefaultPhoneSMSTemplateParamKey(settings.PhoneSMSAliyunTemplateParamKey)
}

func phoneSMSConfigured(cred PhoneSMSCredentials, signName, templateCode string) bool {
	return strings.TrimSpace(cred.AccessKeyID) != "" &&
		strings.TrimSpace(cred.AccessKeySecret) != "" &&
		strings.TrimSpace(signName) != "" &&
		strings.TrimSpace(templateCode) != ""
}

func (s *AuthService) IsPhoneLoginReady(ctx context.Context) bool {
	enabled, cred, signName, templateCode, _ := s.phoneLoginSettings(ctx)
	return enabled && phoneSMSConfigured(cred, signName, templateCode)
}

// SendPhoneVerifyCode stores a 6-digit code and delivers it via Aliyun SMS.
func (s *AuthService) SendPhoneVerifyCode(ctx context.Context, rawPhone string) (*SendVerifyCodeResult, error) {
	if s == nil || s.emailService == nil || s.emailService.cache == nil {
		return nil, ErrServiceUnavailable
	}
	enabled, cred, signName, templateCode, paramKey := s.phoneLoginSettings(ctx)
	if !enabled {
		return nil, ErrPhoneLoginDisabled
	}
	if !phoneSMSConfigured(cred, signName, templateCode) {
		return nil, ErrSMSNotConfigured
	}
	e164, err := NormalizePhone(rawPhone)
	if err != nil {
		return nil, err
	}

	cacheKey := phoneCacheKey(e164)
	existing, err := s.emailService.cache.GetVerificationCode(ctx, cacheKey)
	if err == nil && existing != nil && time.Since(existing.CreatedAt) < verifyCodeCooldown {
		return nil, ErrVerifyCodeTooFrequent
	}

	code, err := s.emailService.GenerateVerifyCode()
	if err != nil {
		return nil, fmt.Errorf("generate phone code: %w", err)
	}
	data := &VerificationCodeData{
		Code:      code,
		Attempts:  0,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(verifyCodeTTL),
	}
	if err := s.emailService.cache.SetVerificationCode(ctx, cacheKey, data, verifyCodeTTL); err != nil {
		return nil, fmt.Errorf("save phone verify code: %w", err)
	}

	if err := s.phoneSMSSenderOrDefault().Send(ctx, cred, PhoneSMSMessage{
		E164:         e164,
		Code:         code,
		SignName:     signName,
		TemplateCode: templateCode,
		ParamKey:     paramKey,
	}); err != nil {
		_ = s.emailService.cache.DeleteVerificationCode(ctx, cacheKey)
		return nil, err
	}

	return &SendVerifyCodeResult{Countdown: int(verifyCodeCooldown.Seconds())}, nil
}

func (s *AuthService) verifyPhoneCode(ctx context.Context, e164, code string) error {
	if s == nil || s.emailService == nil {
		return ErrServiceUnavailable
	}
	return s.emailService.VerifyCode(ctx, phoneCacheKey(e164), code)
}

// LoginOrRegisterPhone logs in an existing phone identity or registers a new
// account when registration is enabled. Invitation / promo / affiliate codes
// apply only to first-time signup, matching OAuth.
func (s *AuthService) LoginOrRegisterPhone(ctx context.Context, rawPhone, code, invitationCode, affiliateCode, promoCode string) (*User, error) {
	if s == nil {
		return nil, ErrServiceUnavailable
	}
	if !s.IsPhoneLoginReady(ctx) {
		enabled, _, _, _, _ := s.phoneLoginSettings(ctx)
		if !enabled {
			return nil, ErrPhoneLoginDisabled
		}
		return nil, ErrSMSNotConfigured
	}
	e164, err := NormalizePhone(rawPhone)
	if err != nil {
		return nil, err
	}
	if err := s.verifyPhoneCode(ctx, e164, strings.TrimSpace(code)); err != nil {
		return nil, err
	}

	existing, err := s.findPhoneIdentityOwner(ctx, e164)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if !existing.IsActive() {
			return nil, ErrUserNotActive
		}
		s.postAuthUserBootstrap(ctx, existing, "phone", true)
		return existing, nil
	}

	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		return nil, ErrRegDisabled
	}

	var invitationRedeemCode *RedeemCode
	if s.settingService.IsInvitationCodeEnabled(ctx) {
		if strings.TrimSpace(invitationCode) == "" {
			return nil, ErrOAuthInvitationRequired
		}
		redeemCode, err := s.redeemRepo.GetByCode(ctx, invitationCode)
		if err != nil {
			return nil, ErrInvitationCodeInvalid
		}
		if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
			return nil, ErrInvitationCodeInvalid
		}
		invitationRedeemCode = redeemCode
	}

	randomPassword, err := randomHexString(32)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to generate random password for phone signup: %v", err)
		return nil, ErrServiceUnavailable
	}
	hashedPassword, err := s.HashPassword(randomPassword)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	grantPlan := s.resolveSignupGrantPlan(ctx, "phone")
	var defaultRPMLimit int
	if s.settingService != nil {
		defaultRPMLimit = s.settingService.GetDefaultUserRPMLimit(ctx)
	}
	national := PhoneNationalNumber(e164)
	username := "手机用户" + national[len(national)-4:]

	newUser := &User{
		Email:        PhoneSyntheticEmail(e164),
		Username:     username,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		RPMLimit:     defaultRPMLimit,
		Status:       StatusActive,
		SignupSource: "phone",
	}

	if s.entClient != nil && invitationRedeemCode != nil {
		tx, err := s.entClient.Tx(ctx)
		if err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to begin transaction for phone registration: %v", err)
			return nil, ErrServiceUnavailable
		}
		defer func() { _ = tx.Rollback() }()
		txCtx := dbent.NewTxContext(ctx, tx)
		if err := s.userRepo.Create(txCtx, newUser); err != nil {
			if errors.Is(err, ErrEmailExists) {
				return nil, ErrServiceUnavailable
			}
			logger.LegacyPrintf("service.auth", "[Auth] Database error creating phone user: %v", err)
			return nil, ErrServiceUnavailable
		}
		if err := s.ensurePhoneAuthIdentity(txCtx, newUser.ID, e164); err != nil {
			return nil, err
		}
		if err := s.redeemRepo.Use(txCtx, invitationRedeemCode.ID, newUser.ID); err != nil {
			return nil, ErrInvitationCodeInvalid
		}
		if err := tx.Commit(); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to commit phone registration transaction: %v", err)
			return nil, ErrServiceUnavailable
		}
	} else {
		if err := s.userRepo.Create(ctx, newUser); err != nil {
			if errors.Is(err, ErrEmailExists) {
				return nil, ErrServiceUnavailable
			}
			logger.LegacyPrintf("service.auth", "[Auth] Database error creating phone user: %v", err)
			return nil, ErrServiceUnavailable
		}
		if err := s.ensurePhoneAuthIdentity(ctx, newUser.ID, e164); err != nil {
			return nil, err
		}
	}

	s.postAuthUserBootstrap(ctx, newUser, "phone", true)
	s.bindOAuthAffiliate(ctx, newUser.ID, affiliateCode)
	newUser = s.applyOAuthSignupPromoCode(ctx, newUser, promoCode)
	return newUser, nil
}

// BindPhoneIdentity attaches a verified phone number to the current user.
func (s *AuthService) BindPhoneIdentity(ctx context.Context, userID int64, rawPhone, code string) (*User, error) {
	if s == nil || userID <= 0 {
		return nil, ErrServiceUnavailable
	}
	if !s.IsPhoneLoginReady(ctx) {
		enabled, _, _, _, _ := s.phoneLoginSettings(ctx)
		if !enabled {
			return nil, ErrPhoneLoginDisabled
		}
		return nil, ErrSMSNotConfigured
	}
	e164, err := NormalizePhone(rawPhone)
	if err != nil {
		return nil, err
	}
	if err := s.verifyPhoneCode(ctx, e164, strings.TrimSpace(code)); err != nil {
		return nil, err
	}

	owner, err := s.findPhoneIdentityOwner(ctx, e164)
	if err != nil {
		return nil, err
	}
	if owner != nil && owner.ID != userID {
		return nil, ErrPhoneAlreadyBound
	}
	if err := s.ensurePhoneAuthIdentity(ctx, userID, e164); err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *AuthService) findPhoneIdentityOwner(ctx context.Context, e164 string) (*User, error) {
	if s == nil || s.entClient == nil {
		return nil, ErrServiceUnavailable
	}
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(phoneAuthProviderType),
			authidentity.ProviderKeyEQ(phoneAuthProviderKey),
			authidentity.ProviderSubjectEQ(e164),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	user, err := s.userRepo.GetByID(ctx, identity.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, nil
		}
		return nil, ErrServiceUnavailable
	}
	return user, nil
}

func (s *AuthService) ensurePhoneAuthIdentity(ctx context.Context, userID int64, e164 string) error {
	if s == nil || s.entClient == nil {
		return ErrServiceUnavailable
	}
	metadata := map[string]any{
		"phone":        e164,
		"phone_masked": MaskPhone(e164),
	}
	identity, err := s.entClient.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ(phoneAuthProviderType),
			authidentity.ProviderKeyEQ(phoneAuthProviderKey),
			authidentity.ProviderSubjectEQ(e164),
		).
		Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return infraerrors.InternalServer("AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect auth identity ownership").WithCause(err)
	}
	if identity != nil {
		if identity.UserID != userID {
			return ErrPhoneAlreadyBound
		}
		now := time.Now()
		_, err = s.entClient.AuthIdentity.UpdateOneID(identity.ID).
			SetMetadata(metadata).
			SetVerifiedAt(now).
			Save(ctx)
		return err
	}
	now := time.Now()
	_, err = s.entClient.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType(phoneAuthProviderType).
		SetProviderKey(phoneAuthProviderKey).
		SetProviderSubject(e164).
		SetVerifiedAt(now).
		SetMetadata(metadata).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("create phone auth identity: %w", err)
	}
	return nil
}
