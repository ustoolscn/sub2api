package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	aliyunSMSEndpoint = "https://dysmsapi.aliyuncs.com/"
	aliyunSMSVersion  = "2017-05-25"
	aliyunSMSAction   = "SendSms"
	aliyunSMSRegion   = "cn-hangzhou"
)

// PhoneSMSMessage is the payload handed to a PhoneSMSSender.
type PhoneSMSMessage struct {
	E164         string
	Code         string
	SignName     string
	TemplateCode string
	ParamKey     string
}

// PhoneSMSSender delivers a verification SMS. Tests replace this with a fake.
type PhoneSMSSender interface {
	Send(ctx context.Context, cred PhoneSMSCredentials, msg PhoneSMSMessage) error
}

// PhoneSMSCredentials holds Aliyun Dysmsapi keys. Secrets never leave the service layer.
type PhoneSMSCredentials struct {
	AccessKeyID     string
	AccessKeySecret string
}

// DefaultPhoneSMSTemplateParamKey returns the Aliyun template variable name.
// Empty values fall back to "code", matching the common SMS_xxxx ${code} template.
func DefaultPhoneSMSTemplateParamKey(raw string) string {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "code"
	}
	return key
}

type aliyunPhoneSMSSender struct {
	httpClient *http.Client
	now        func() time.Time
	nonce      func() string
}

func newAliyunPhoneSMSSender() *aliyunPhoneSMSSender {
	return &aliyunPhoneSMSSender{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		now:        time.Now,
		nonce: func() string {
			n, err := randomHexString(16)
			if err != nil {
				return fmt.Sprintf("%d", time.Now().UnixNano())
			}
			return n
		},
	}
}

func (s *aliyunPhoneSMSSender) Send(ctx context.Context, cred PhoneSMSCredentials, msg PhoneSMSMessage) error {
	if s == nil {
		return ErrSMSNotConfigured
	}
	if strings.TrimSpace(cred.AccessKeyID) == "" || strings.TrimSpace(cred.AccessKeySecret) == "" {
		return ErrSMSNotConfigured
	}
	paramKey := DefaultPhoneSMSTemplateParamKey(msg.ParamKey)
	templateParam, err := json.Marshal(map[string]string{paramKey: msg.Code})
	if err != nil {
		return fmt.Errorf("marshal sms template param: %w", err)
	}

	params := map[string]string{
		"AccessKeyId":      cred.AccessKeyID,
		"Action":           aliyunSMSAction,
		"Format":           "JSON",
		"PhoneNumbers":     PhoneNationalNumber(msg.E164),
		"RegionId":         aliyunSMSRegion,
		"SignName":         msg.SignName,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   s.nonce(),
		"SignatureVersion": "1.0",
		"TemplateCode":     msg.TemplateCode,
		"TemplateParam":    string(templateParam),
		"Timestamp":        s.now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          aliyunSMSVersion,
	}
	params["Signature"] = aliyunRPCSignature("POST", cred.AccessKeySecret, params)

	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aliyunSMSEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build sms request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send sms: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	var parsed struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		logger.LegacyPrintf("service.phone_sms", "[SMS] aliyun non-json response status=%d body=%s", resp.StatusCode, truncateSMSLog(string(body), 200))
		return ErrSMSSendFailed
	}
	if !strings.EqualFold(parsed.Code, "OK") {
		logger.LegacyPrintf("service.phone_sms", "[SMS] aliyun rejected code=%s message=%s", parsed.Code, parsed.Message)
		return ErrSMSSendFailed
	}
	return nil
}

func aliyunRPCSignature(method, accessKeySecret string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key == "Signature" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		canonical = append(canonical, aliyunPercentEncode(key)+"="+aliyunPercentEncode(params[key]))
	}
	stringToSign := method + "&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(strings.Join(canonical, "&"))
	mac := hmac.New(sha1.New, []byte(accessKeySecret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func truncateSMSLog(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
