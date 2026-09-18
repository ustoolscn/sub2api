//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "13800138000", want: "+8613800138000"},
		{in: "+86 138-0013-8000", want: "+8613800138000"},
		{in: "008613800138000", want: "+8613800138000"},
		{in: "8613800138000", want: "+8613800138000"},
		{in: "12345", wantErr: true},
		{in: "23800138000", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizePhone(tc.in)
		if tc.wantErr {
			require.Error(t, err, tc.in)
			continue
		}
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
	}
}

func TestMaskPhone(t *testing.T) {
	t.Parallel()
	require.Equal(t, "138****8000", MaskPhone("+8613800138000"))
	require.Equal(t, "8613800138000@phone.invalid", PhoneSyntheticEmail("+8613800138000"))
}

func TestAliyunPercentEncodeAndSignature(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a%20b", aliyunPercentEncode("a b"))
	require.Equal(t, "%2A", aliyunPercentEncode("*"))
	require.Equal(t, "~", aliyunPercentEncode("~"))

	params := map[string]string{
		"AccessKeyId": "testid",
		"Action":      "SendSms",
		"Format":      "JSON",
	}
	sig := aliyunRPCSignature("POST", "testsecret", params)
	require.NotEmpty(t, sig)
	require.NotEqual(t, sig, aliyunRPCSignature("GET", "testsecret", params))
}

type fakePhoneSMSSender struct {
	sent []PhoneSMSMessage
	err  error
}

func (f *fakePhoneSMSSender) Send(_ context.Context, _ PhoneSMSCredentials, msg PhoneSMSMessage) error {
	f.sent = append(f.sent, msg)
	return f.err
}

type phoneEmailCacheStub struct {
	codes map[string]*VerificationCodeData
}

func (s *phoneEmailCacheStub) GetVerificationCode(_ context.Context, email string) (*VerificationCodeData, error) {
	if data, ok := s.codes[email]; ok {
		return data, nil
	}
	return nil, errors.New("cache miss")
}

func (s *phoneEmailCacheStub) SetVerificationCode(_ context.Context, email string, data *VerificationCodeData, _ time.Duration) error {
	if s.codes == nil {
		s.codes = map[string]*VerificationCodeData{}
	}
	copied := *data
	s.codes[email] = &copied
	return nil
}

func (s *phoneEmailCacheStub) DeleteVerificationCode(_ context.Context, email string) error {
	delete(s.codes, email)
	return nil
}

func (s *phoneEmailCacheStub) GetNotifyVerifyCode(context.Context, string) (*VerificationCodeData, error) {
	return nil, nil
}
func (s *phoneEmailCacheStub) SetNotifyVerifyCode(context.Context, string, *VerificationCodeData, time.Duration) error {
	return nil
}
func (s *phoneEmailCacheStub) DeleteNotifyVerifyCode(context.Context, string) error { return nil }
func (s *phoneEmailCacheStub) GetPasswordResetToken(context.Context, string) (*PasswordResetTokenData, error) {
	return nil, nil
}
func (s *phoneEmailCacheStub) SetPasswordResetToken(context.Context, string, *PasswordResetTokenData, time.Duration) error {
	return nil
}
func (s *phoneEmailCacheStub) DeletePasswordResetToken(context.Context, string) error { return nil }
func (s *phoneEmailCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}
func (s *phoneEmailCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}
func (s *phoneEmailCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}
func (s *phoneEmailCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

type phoneSettingRepoStub struct {
	values map[string]string
}

func (s *phoneSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}
func (s *phoneSettingRepoStub) GetValue(context.Context, string) (string, error) {
	return "", ErrSettingNotFound
}
func (s *phoneSettingRepoStub) Set(context.Context, string, string) error { return nil }
func (s *phoneSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}
func (s *phoneSettingRepoStub) SetMultiple(context.Context, map[string]string) error { return nil }
func (s *phoneSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}
func (s *phoneSettingRepoStub) Delete(context.Context, string) error { return nil }

func readyPhoneSettings() map[string]string {
	return map[string]string{
		SettingKeyPhoneLoginEnabled:             "true",
		SettingKeyPhoneSMSAliyunAccessKeyID:     "ak",
		SettingKeyPhoneSMSAliyunAccessKeySecret: "sk",
		SettingKeyPhoneSMSAliyunSignName:        "测试",
		SettingKeyPhoneSMSAliyunTemplateCode:    "SMS_123",
	}
}

func TestSendPhoneVerifyCode_CooldownAndSend(t *testing.T) {
	cache := &phoneEmailCacheStub{codes: map[string]*VerificationCodeData{}}
	svc := &AuthService{
		emailService:   &EmailService{cache: cache},
		settingService: NewSettingService(&phoneSettingRepoStub{values: readyPhoneSettings()}, &config.Config{}),
	}
	fake := &fakePhoneSMSSender{}
	svc.SetPhoneSMSSender(fake)

	result, err := svc.SendPhoneVerifyCode(context.Background(), "13800138000")
	require.NoError(t, err)
	require.Equal(t, 60, result.Countdown)
	require.Len(t, fake.sent, 1)
	require.Equal(t, "+8613800138000", fake.sent[0].E164)
	require.Len(t, fake.sent[0].Code, 6)

	_, err = svc.SendPhoneVerifyCode(context.Background(), "13800138000")
	require.ErrorIs(t, err, ErrVerifyCodeTooFrequent)
}

func TestSendPhoneVerifyCode_Disabled(t *testing.T) {
	svc := &AuthService{
		emailService:   &EmailService{cache: &phoneEmailCacheStub{codes: map[string]*VerificationCodeData{}}},
		settingService: NewSettingService(&phoneSettingRepoStub{values: map[string]string{}}, &config.Config{}),
	}
	_, err := svc.SendPhoneVerifyCode(context.Background(), "13800138000")
	require.ErrorIs(t, err, ErrPhoneLoginDisabled)
}

func TestVerifyPhoneCode(t *testing.T) {
	cache := &phoneEmailCacheStub{codes: map[string]*VerificationCodeData{
		phoneCacheKey("+8613800138000"): {
			Code:      "123456",
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Minute),
		},
	}}
	svc := &AuthService{emailService: &EmailService{cache: cache}}
	require.ErrorIs(t, svc.verifyPhoneCode(context.Background(), "+8613800138000", "000000"), ErrInvalidVerifyCode)
	require.NoError(t, svc.verifyPhoneCode(context.Background(), "+8613800138000", "123456"))
}

func TestGetPublicSettings_ExposesPhoneLoginOnlyWhenSMSConfigured(t *testing.T) {
	repo := &phoneSettingRepoStub{values: readyPhoneSettings()}
	svc := NewSettingService(repo, &config.Config{})
	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.PhoneLoginEnabled)

	repo.values[SettingKeyPhoneSMSAliyunTemplateCode] = ""
	settings, err = svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.PhoneLoginEnabled)
}
