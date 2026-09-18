package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type PhoneSendCodeRequest struct {
	Phone                 string `json:"phone" binding:"required"`
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

type PhoneLoginRequest struct {
	Phone                 string `json:"phone" binding:"required"`
	Code                  string `json:"code" binding:"required"`
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
	PromoCode             string `json:"promo_code"`
	InvitationCode        string `json:"invitation_code"`
	AffCode               string `json:"aff_code"`
}

// SendPhoneVerifyCode sends an SMS verification code for phone login/register.
// POST /api/v1/auth/phone/send-code
func (h *AuthHandler) SendPhoneVerifyCode(c *gin.Context) {
	var req PhoneSendCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	result, err := h.authService.SendPhoneVerifyCode(c.Request.Context(), req.Phone)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, SendVerifyCodeResponse{
		Message:   "Verification code sent successfully",
		Countdown: result.Countdown,
	})
}

// PhoneLogin logs in or registers with a phone number + SMS code.
// POST /api/v1/auth/phone/login
func (h *AuthHandler) PhoneLogin(c *gin.Context) {
	var req PhoneLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	user, err := h.authService.LoginOrRegisterPhone(
		c.Request.Context(),
		req.Phone,
		req.Code,
		req.InvitationCode,
		req.AffCode,
		req.PromoCode,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if h.totpService != nil && h.settingSvc.IsTotpEnabled(c.Request.Context()) && user.TotpEnabled {
		tempToken, err := h.totpService.CreateLoginSession(c.Request.Context(), user.ID, user.Email)
		if err != nil {
			response.InternalError(c, "Failed to create 2FA session")
			return
		}
		response.Success(c, TotpLoginResponse{
			Requires2FA:     true,
			TempToken:       tempToken,
			UserEmailMasked: service.MaskEmail(user.Email),
		})
		return
	}

	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	h.respondWithTokenPair(c, user)
}
